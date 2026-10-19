// Package xisf reads and writes monolithic XISF (Extensible Image
// Serialization Format) files as produced by PixInsight.
//
// The package is built around two ideas:
//
// Preserve what you do not touch. The XML header is parsed into a byte-span
// DOM rather than a typed model, so unknown elements, attribute order,
// comments, namespaces and the exact escaping of large properties survive a
// round trip. Rendering copies untouched byte ranges and rebuilds only the
// nodes that changed. Data blocks whose pixels were not modified are copied
// back with their original stored bytes, and the original positions are
// reused when nothing changed, so an opened-and-closed file is byte-identical.
//
// Stay exact about pixels. Samples are exposed as typed slices in planar
// order, one Go type per XISF sample format, with no conversion through
// float64. Normal (interleaved) storage and big-endian byte order are
// converted on read and restored on write. Editing a block re-encodes it with
// its original codec, shuffle item size, subblock layout and checksum
// algorithm unless WriteOptions overrides them.
package xisf

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// File is an open monolithic XISF unit.
type File struct {
	doc       *xmlDoc
	src       []byte
	version   string
	images    []*Image
	allImages []*Image // includes thumbnails
	blocks    []*dataBlock
	alignment int
	dataEnd   int
	metaDirty bool
}

type dataBlock struct {
	elem        *xmlNode
	dataNode    *xmlNode // the <Data> child of an embedded block
	loc         Location
	encoding    TextEncoding
	compression *Compression
	checksum    *Checksum
	byteOrder   ByteOrder

	stored []byte // bytes as stored in the file, before decompression

	// Write state.
	newStored      []byte
	newCompression *Compression
	newSubblocks   []Subblock
	newChecksum    *Checksum
	dirty          bool

	// unavailable is set when the block cannot be decoded because it uses a
	// codec or checksum this package does not implement. The block is still
	// preserved byte for byte.
	unavailable string
}

// WriteOptions controls how a file is written. The zero value preserves the
// source header and compression exactly: clean blocks are copied byte for
// byte and dirty image blocks are re-encoded with their original codec,
// shuffle item size, subblock layout and checksum algorithm.
type WriteOptions struct {
	// UpdateCreationTime replaces XISF:CreationTime with the current time.
	UpdateCreationTime bool
	// CreatorApplication replaces XISF:CreatorApplication when non-empty.
	CreatorApplication string
	// Compression overrides the codec used for dirty image blocks. Nil keeps
	// each block's original compression.
	Compression *Compression
	// CompressionLevel is the abstract 1..100 level passed to the codec. Zero
	// uses the original XISF:CompressionLevel property when present.
	CompressionLevel int
	// Checksum overrides the checksum algorithm for dirty image blocks. Nil
	// preserves the block's original algorithm.
	Checksum *ChecksumAlgorithm
}

// Open reads a monolithic .xisf file.
func Open(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse reads a monolithic XISF unit from memory.
func Parse(data []byte) (*File, error) {
	header, _, end, err := scan(data)
	if err != nil {
		return nil, err
	}
	if !validUTF8(header) {
		return nil, errf(ErrHeader, "header is not valid UTF-8")
	}
	doc, err := parseXML(header)
	if err != nil {
		return nil, err
	}
	if doc.root.localName() != "xisf" {
		return nil, errf(ErrHeader, "root element is <%s>, expected <xisf>", doc.root.name)
	}
	version, _ := doc.root.attrValue("version")
	if version == "" {
		version = "1.0"
	}
	f := &File{doc: doc, src: data, version: version, dataEnd: end}
	f.alignment = f.alignmentFromMetadata()

	for _, n := range doc.root.children() {
		switch n.localName() {
		case "Image":
			im, err := f.parseImage(n)
			if err != nil {
				return nil, err
			}
			f.images = append(f.images, im)
		}
	}

	// Collect any remaining location-bearing elements (metadata properties
	// with byte-array payloads, for example). Images and their children have
	// already been bound.
	seen := make(map[*xmlNode]bool, len(f.blocks))
	for _, b := range f.blocks {
		seen[b.elem] = true
	}
	var walk func(*xmlNode) error
	walk = func(n *xmlNode) error {
		for _, c := range n.children() {
			if _, ok := c.attrValue("location"); ok && !seen[c] {
				b, err := f.parseBlock(c)
				if err != nil {
					return err
				}
				if err := f.loadBlock(b); err != nil {
					return err
				}
				seen[c] = true
				f.blocks = append(f.blocks, b)
			}
			if err := walk(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(doc.root); err != nil {
		return nil, err
	}
	return f, nil
}

// Version is the value of the root element's version attribute.
func (f *File) Version() string { return f.version }

// Images returns the top-level images, in document order.
func (f *File) Images() []*Image { return f.images }

// ImageByID returns the image with the given id attribute.
func (f *File) ImageByID(id string) (*Image, bool) {
	for _, im := range f.images {
		if im.ID == id {
			return im, true
		}
	}
	return nil, false
}

// HeaderXML returns a snapshot of the XML header exactly as it was read from
// the file. It is the original text, not the text that will be written after
// modifications.
func (f *File) HeaderXML() []byte { return append([]byte(nil), f.doc.src...) }

func (f *File) parseBlock(n *xmlNode) (*dataBlock, error) {
	locText, ok := n.attrValue("location")
	if !ok {
		return nil, errf(ErrHeader, "<%s> has no location attribute", n.name)
	}
	loc, err := parseLocation(locText)
	if err != nil {
		return nil, err
	}
	b := &dataBlock{elem: n, loc: loc, byteOrder: LittleEndian, encoding: loc.Encoding}

	effective := n
	if loc.Kind == LocEmbedded {
		b.dataNode = n.child("Data")
		if b.dataNode == nil {
			return nil, errf(ErrHeader, "embedded block has no <Data> element")
		}
		effective = b.dataNode
		if enc, ok := b.dataNode.attrValue("encoding"); ok {
			switch enc {
			case "base64":
				b.encoding = EncBase64
			case "hex":
				b.encoding = EncHex
			default:
				return nil, errf(ErrHeader, "unknown data encoding %q", enc)
			}
		}
	}

	attrFrom := func(name string) (string, bool) {
		if v, ok := effective.attrValue(name); ok {
			return v, true
		}
		if effective != n {
			return n.attrValue(name)
		}
		return "", false
	}

	if v, ok := attrFrom("compression"); ok {
		c, err := parseCompression(v)
		switch {
		case err == nil:
			if v, ok := attrFrom("subblocks"); ok {
				subs, err := parseSubblocks(v)
				if err != nil {
					return nil, err
				}
				c.Subblocks = subs
			}
			b.compression = c
		case errors.Is(err, ErrUnsupported):
			b.unavailable = err.Error()
		default:
			return nil, err
		}
	}
	if v, ok := attrFrom("checksum"); ok {
		c, err := parseChecksum(v)
		switch {
		case err == nil:
			b.checksum = c
		case errors.Is(err, ErrUnsupported):
			// A checksum we cannot compute is not fatal: the block is
			// preserved and the checksum is not verified.
		default:
			return nil, err
		}
	}
	if v, ok := n.attrValue("byteOrder"); ok {
		o, err := parseByteOrder(v)
		if err != nil {
			return nil, err
		}
		b.byteOrder = o
	}
	return b, nil
}

func (f *File) parseImage(n *xmlNode) (*Image, error) {
	geom, ok := n.attrValue("geometry")
	if !ok {
		return nil, errf(ErrHeader, "<%s> has no geometry attribute", n.name)
	}
	dims, channels, err := parseGeometry(geom)
	if err != nil {
		return nil, err
	}
	sf, ok := n.attrValue("sampleFormat")
	if !ok {
		return nil, errf(ErrHeader, "<%s> has no sampleFormat attribute", n.name)
	}
	format, err := parseSampleFormat(sf)
	if err != nil {
		return nil, err
	}

	im := &Image{
		dims:         dims,
		channels:     channels,
		SampleFormat: format,
		ColorSpace:   Gray,
		PixelStorage: Planar,
		elem:         n,
	}
	im.ID, _ = n.attrValue("id")
	im.UUID, _ = n.attrValue("uuid")
	im.ImageType, _ = n.attrValue("imageType")
	im.Orientation, _ = n.attrValue("orientation")
	if v, ok := n.attrValue("colorSpace"); ok {
		cs, err := parseColorSpace(v)
		if err != nil {
			return nil, err
		}
		im.ColorSpace = cs
	}
	if v, ok := n.attrValue("pixelStorage"); ok {
		ps, err := parsePixelStorage(v)
		if err != nil {
			return nil, err
		}
		im.PixelStorage = ps
	}
	if v, ok := n.attrValue("bounds"); ok {
		lo, hi, ok := strings.Cut(v, ":")
		if !ok {
			return nil, errf(ErrHeader, "malformed bounds %q", v)
		}
		l, err1 := strconv.ParseFloat(strings.TrimSpace(lo), 64)
		h, err2 := strconv.ParseFloat(strings.TrimSpace(hi), 64)
		if err1 != nil || err2 != nil {
			return nil, errf(ErrHeader, "malformed bounds %q", v)
		}
		im.Bounds = [2]float64{l, h}
		im.HasBounds = true
	}
	if v, ok := n.attrValue("offset"); ok {
		o, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, errf(ErrHeader, "malformed offset %q", v)
		}
		im.Offset, im.HasOffset = o, true
	}
	for _, c := range n.children() {
		if c.localName() == "FITSKeyword" {
			kw := FITSKeyword{}
			kw.Name, _ = c.attrValue("name")
			kw.Value, _ = c.attrValue("value")
			kw.Comment, _ = c.attrValue("comment")
			im.FITSKeywords = append(im.FITSKeywords, kw)
		}
	}

	block, err := f.parseBlock(n)
	if err != nil {
		return nil, err
	}
	switch block.loc.Kind {
	case LocInline:
		return nil, errf(ErrHeader, "an Image block cannot be inline")
	case LocPath, LocURL:
		return nil, errf(ErrUnsupported, "external image data blocks are not supported")
	}
	im.block = block
	im.byteOrder = block.byteOrder
	f.blocks = append(f.blocks, block)

	if err := f.loadBlock(block); err != nil {
		return nil, err
	}
	if block.unavailable != "" {
		im.importErr = errf(ErrUnsupported, "%s", block.unavailable)
	} else {
		plain, err := decodeCompression(block.stored, block.compression)
		if err != nil {
			return nil, err
		}
		if err := im.decodePixels(plain); err != nil {
			return nil, err
		}
	}

	// Ancillary blocks and objects owned by this image.
	for _, c := range n.children() {
		switch c.localName() {
		case "Thumbnail":
			thumb, err := f.parseImage(c)
			if err != nil {
				return nil, err
			}
			im.Thumbnail = thumb
		case "ICCProfile":
			b, err := f.parseBlock(c)
			if err != nil {
				return nil, err
			}
			f.blocks = append(f.blocks, b)
			if err := f.loadBlock(b); err != nil {
				return nil, err
			}
			if b.unavailable != "" {
				break
			}
			plain, err := decodeCompression(b.stored, b.compression)
			if err != nil {
				return nil, err
			}
			im.ICCProfile = plain
		default:
			if _, ok := c.attrValue("location"); ok {
				b, err := f.parseBlock(c)
				if err != nil {
					return nil, err
				}
				f.blocks = append(f.blocks, b)
				if err := f.loadBlock(b); err != nil {
					return nil, err
				}
			}
		}
	}
	f.allImages = append(f.allImages, im)
	return im, nil
}

// loadBlock resolves a block's stored bytes and verifies its checksum. The
// checksum is checked before decompression, as the specification requires.
func (f *File) loadBlock(b *dataBlock) error {
	stored, err := f.readStored(b)
	if err != nil {
		return err
	}
	if err := verifyChecksum(stored, b.checksum); err != nil {
		return err
	}
	b.stored = stored
	return nil
}

func (f *File) readStored(b *dataBlock) ([]byte, error) {
	switch b.loc.Kind {
	case LocAttachment:
		start := b.loc.Position
		size := b.loc.Size
		if start < 0 || size < 0 || start < int64(f.dataEnd) {
			return nil, errf(ErrCorrupt, "block at %d overlaps the header, which ends at %d", start, f.dataEnd)
		}
		end := start + size
		if end < start || end > int64(len(f.src)) {
			return nil, errf(ErrTruncated, "block [%d,%d) runs past the end of the file", start, end)
		}
		return f.src[start:end], nil
	case LocInline:
		return decodeText(b.elem.textValue(f.doc.src), b.loc.Encoding)
	case LocEmbedded:
		return decodeText(b.dataNode.textValue(f.doc.src), b.encoding)
	default:
		return nil, errf(ErrUnsupported, "external data blocks are not supported")
	}
}

func decodeText(s string, enc TextEncoding) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		}
		return r
	}, s)
	switch enc {
	case EncHex:
		return hex.DecodeString(s)
	default:
		return base64.StdEncoding.DecodeString(s)
	}
}

// MarkDirty records that the image's samples were modified through a slice
// returned by Pixels or Plane, which the library cannot observe.
func (im *Image) MarkDirty() { im.dirty = true }

// --- writing ---------------------------------------------------------------

// WriteFile writes the unit to path atomically: a temporary file in the same
// directory is renamed into place.
func (f *File) WriteFile(path string, opts *WriteOptions) error {
	data, err := f.Bytes(opts)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".xisf-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Bytes renders the complete monolithic file.
func (f *File) Bytes(opts *WriteOptions) ([]byte, error) {
	changesMetadata := opts != nil && (opts.UpdateCreationTime || opts.CreatorApplication != "")
	if !changesMetadata && f.isClean() {
		return append([]byte(nil), f.src...), nil
	}
	if opts != nil {
		if opts.UpdateCreationTime {
			f.setMetadataText("XISF:CreationTime", time.Now().UTC().Format("2006-01-02T15:04:05Z"))
		}
		if opts.CreatorApplication != "" {
			f.setMetadataText("XISF:CreatorApplication", opts.CreatorApplication)
		}
	}
	if err := f.prepare(opts); err != nil {
		return nil, err
	}

	// Embedded blocks put their bytes in the header, so this has to happen
	// before the header length is computed.
	for _, b := range f.blocks {
		if !b.dirty || b.loc.Kind != LocEmbedded {
			continue
		}
		b.dataNode.setText(encodeText(b.newStored, b.encoding))
		b.dataNode.setAttr("encoding", b.encoding.String())
	}

	attached := make([]*dataBlock, 0, len(f.blocks))
	for _, b := range f.blocks {
		if b.loc.Kind == LocAttachment {
			attached = append(attached, b)
		}
	}

	alignment := f.alignment
	assumed := len(f.doc.render())
	var header []byte
	converged := false
	for i := 0; i < 64; i++ {
		pos := int64(PreambleLen + assumed)
		if alignment > 1 {
			pos = alignUp(pos, int64(alignment))
		}
		for _, b := range attached {
			b.loc.Position = pos
			b.loc.Size = int64(len(b.newStored))
			b.elem.setAttr("location", b.loc.String())
			pos += int64(len(b.newStored))
			if alignment > 1 {
				pos = alignUp(pos, int64(alignment))
			}
		}
		header = f.doc.render()
		if len(header) == assumed {
			converged = true
			break
		}
		assumed = len(header)
	}
	if !converged {
		return nil, errf(ErrHeader, "header length did not converge while laying out data blocks")
	}
	if len(header) > int(^uint32(0)) {
		return nil, errf(ErrUnsupported, "header is larger than the format's 32-bit length field")
	}

	total := int64(PreambleLen + len(header))
	for _, b := range attached {
		if end := b.loc.Position + int64(len(b.newStored)); end > total {
			total = end
		}
	}
	out := make([]byte, 0, total)
	out = append(out, Signature...)
	out = append(out,
		byte(len(header)), byte(len(header)>>8), byte(len(header)>>16), byte(len(header)>>24))
	out = append(out, 0, 0, 0, 0)
	out = append(out, header...)
	for _, b := range attached {
		if int64(len(out)) > b.loc.Position {
			return nil, errf(ErrHeader, "data block at %d overlaps the header or a previous block", b.loc.Position)
		}
		for int64(len(out)) < b.loc.Position {
			out = append(out, 0)
		}
		out = append(out, b.newStored...)
	}
	return out, nil
}

func (f *File) isClean() bool {
	if f.metaDirty {
		return false
	}
	for _, im := range f.allImages {
		if im.dirty {
			return false
		}
	}
	return true
}

func (f *File) prepare(opts *WriteOptions) error {
	level := 0
	if opts != nil && opts.CompressionLevel > 0 {
		level = opts.CompressionLevel
	} else if p, ok := f.Property("XISF:CompressionLevel"); ok {
		if v, err := strconv.Atoi(strings.TrimSpace(p.Value)); err == nil {
			level = v
		}
	}

	for _, b := range f.blocks {
		if !b.dirty {
			b.newStored = b.stored
			continue
		}
	}
	for _, im := range f.allImages {
		if !im.dirty {
			continue
		}
		if err := im.require2D(); err != nil {
			return err
		}
		b := im.block
		if b == nil {
			return errf(ErrCorrupt, "image has no data block")
		}
		b.dirty = true
		plain, err := im.bytesNative()
		if err != nil {
			return err
		}

		comp := b.compression
		if opts != nil && opts.Compression != nil {
			comp = opts.Compression
		}
		b.newStored = plain
		b.newCompression = nil
		b.newSubblocks = nil

		if comp != nil {
			c := *comp
			c.UncompressedSize = int64(len(plain))
			if c.Shuffled && c.ItemSize <= 0 {
				c.ItemSize = im.SampleFormat.UnitSize()
			}
			// A geometry change invalidates the old subblock table.
			var subTotal int64
			for _, s := range c.Subblocks {
				subTotal += s.Uncompressed
			}
			if subTotal != int64(len(plain)) {
				c.Subblocks = nil
			}
			wasCompressed := b.compression != nil
			stored, subs, err := encodeCompression(plain, &c, level)
			if err != nil {
				return err
			}
			if !wasCompressed && len(stored) >= len(plain) {
				// Compression was requested but does not help; store plain.
				b.newStored = plain
			} else {
				b.newStored = stored
				b.newCompression = &c
				// Keep the original subblock layout. A single raw subblock
				// also needs a table, otherwise the decoder would try to
				// decompress stored bytes.
				anyRaw := false
				for _, s := range subs {
					if s.Compressed == s.Uncompressed {
						anyRaw = true
					}
				}
				if len(c.Subblocks) > 0 || len(subs) > 1 || anyRaw {
					b.newSubblocks = subs
				}
			}
		}

		alg := ChecksumAlgorithm("")
		if b.checksum != nil {
			alg = b.checksum.Algorithm
		}
		if opts != nil && opts.Checksum != nil {
			alg = *opts.Checksum
		}
		if alg != "" {
			d, err := hashDigest(alg, b.newStored)
			if err != nil {
				return err
			}
			b.newChecksum = &Checksum{Algorithm: alg, Digest: d}
		}

		im.writeAttributes()
		im.writeBlockAttrs()
	}
	return nil
}

func (im *Image) writeAttributes() {
	n := im.elem
	if cur, _ := n.attrValue("geometry"); cur != formatGeometry(im.dims, im.channels) {
		n.setAttr("geometry", formatGeometry(im.dims, im.channels))
	}
	if cur, _ := n.attrValue("sampleFormat"); cur != im.SampleFormat.String() {
		n.setAttr("sampleFormat", im.SampleFormat.String())
	}
	if cur, ok := n.attrValue("colorSpace"); (ok && cur != im.ColorSpace.String()) || (!ok && im.ColorSpace != Gray) {
		n.setAttr("colorSpace", im.ColorSpace.String())
	}
	if cur, ok := n.attrValue("pixelStorage"); (ok && cur != im.PixelStorage.String()) || (!ok && im.PixelStorage != Planar) {
		n.setAttr("pixelStorage", im.PixelStorage.String())
	}
	writeOptionalAttr(n, "id", im.ID)
	writeOptionalAttr(n, "uuid", im.UUID)
	writeOptionalAttr(n, "imageType", im.ImageType)
	writeOptionalAttr(n, "orientation", im.Orientation)
	if im.HasOffset {
		v := formatFloat(im.Offset)
		if cur, ok := n.attrValue("offset"); !ok || cur != v {
			n.setAttr("offset", v)
		}
	} else {
		n.removeAttr("offset")
	}
	if im.HasBounds {
		v := formatFloat(im.Bounds[0]) + ":" + formatFloat(im.Bounds[1])
		if cur, ok := n.attrValue("bounds"); !ok || cur != v {
			n.setAttr("bounds", v)
		}
	} else {
		n.removeAttr("bounds")
	}
}

func writeOptionalAttr(n *xmlNode, name, value string) {
	cur, ok := n.attrValue(name)
	if value == "" {
		if ok {
			n.removeAttr(name)
		}
		return
	}
	if !ok || cur != value {
		n.setAttr(name, value)
	}
}

func (im *Image) writeBlockAttrs() {
	b := im.block
	effective := b.elem
	if b.loc.Kind == LocEmbedded {
		effective = b.dataNode
	}
	if b.newCompression != nil {
		effective.setAttr("compression", b.newCompression.String())
		if len(b.newSubblocks) > 0 {
			effective.setAttr("subblocks", formatSubblocks(b.newSubblocks))
		} else {
			effective.removeAttr("subblocks")
		}
	} else {
		effective.removeAttr("compression")
		effective.removeAttr("subblocks")
	}
	if b.newChecksum != nil {
		effective.setAttr("checksum", b.newChecksum.String())
	}
}

func (f *File) setMetadataText(id, value string) {
	for _, n := range f.doc.root.children() {
		if n.localName() != "Metadata" {
			continue
		}
		for _, p := range n.children() {
			if p.localName() != "Property" {
				continue
			}
			if pid, _ := p.attrValue("id"); pid != id {
				continue
			}
			typ, _ := p.attrValue("type")
			if typ == "String" || typ == "" {
				p.setText(value)
			} else {
				p.setAttr("value", value)
			}
			f.metaDirty = true
			return
		}
	}
}

func encodeText(b []byte, enc TextEncoding) string {
	if enc == EncHex {
		return hex.EncodeToString(b)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func alignUp(v, align int64) int64 {
	if align <= 1 {
		return v
	}
	rem := v % align
	if rem == 0 {
		return v
	}
	return v + align - rem
}
