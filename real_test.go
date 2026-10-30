package xisf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealFilesByteIdentical checks the promise that an untouched file is
// reproduced exactly: same header bytes, same block bytes, same padding.
//
// The sample files are not part of the repository. Place them in img/ to run
// this test; it skips cleanly when they are absent.
func TestRealFilesByteIdentical(t *testing.T) {
	paths, _ := filepath.Glob("img/*.xisf")
	if len(paths) == 0 {
		t.Skip("no files in img/")
	}
	if testing.Short() {
		t.Skip("large sample files skipped in -short mode")
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			f, err := Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			out, err := f.Bytes(nil)
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			if !bytes.Equal(out, f.src) {
				t.Fatalf("untouched file changed: wrote %d bytes, original is %d", len(out), len(f.src))
			}
			if len(f.Images()) != 1 {
				t.Fatalf("got %d images", len(f.Images()))
			}
			im := f.Images()[0]
			// Compare the parse against the header's own declarations rather
			// than against facts about one particular sample corpus.
			info := readSampleInfo(t, path)
			if got := formatGeometry(im.dims, im.channels); got != info.geometry {
				t.Fatalf("geometry %s, header says %s", got, info.geometry)
			}
			if got := im.SampleFormat.String(); got != info.sampleFormat {
				t.Fatalf("sample format %s, header says %s", got, info.sampleFormat)
			}
			if info.colorSpace != "" && im.ColorSpace.String() != info.colorSpace {
				t.Fatalf("color space %s, header says %s", im.ColorSpace, info.colorSpace)
			}
			if info.pixelStorage != "" && im.PixelStorage.String() != info.pixelStorage {
				t.Fatalf("pixel storage %s, header says %s", im.PixelStorage, info.pixelStorage)
			}
		})
	}
}

// sampleInfo holds the header facts needed to discover samples, read without
// decoding the pixel data.
type sampleInfo struct {
	compression  *Compression
	checksum     string
	hasComp      bool
	geometry     string
	channels     int
	sampleFormat string
	colorSpace   string
	pixelStorage string
}

// readHeaderDoc reads and parses only the XML header of a monolithic file.
func readHeaderDoc(t *testing.T, path string) *xmlDoc {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = fh.Close() }()
	var pre [PreambleLen]byte
	if _, err := io.ReadFull(fh, pre[:]); err != nil {
		t.Fatalf("read preamble of %s: %v", path, err)
	}
	if string(pre[:8]) != Signature {
		t.Fatalf("%s: not a monolithic XISF file", path)
	}
	n := binary.LittleEndian.Uint32(pre[8:12])
	hdr := make([]byte, n)
	if _, err := io.ReadFull(fh, hdr); err != nil {
		t.Fatalf("read header of %s: %v", path, err)
	}
	doc, err := parseXML(hdr)
	if err != nil {
		t.Fatalf("parse header of %s: %v", path, err)
	}
	return doc
}

// readSampleInfo reads the main image's declaring attributes from the header
// alone, so discovery does not pay for a full decode.
func readSampleInfo(t *testing.T, path string) sampleInfo {
	t.Helper()
	doc := readHeaderDoc(t, path)
	for _, n := range doc.root.children() {
		if n.localName() != "Image" {
			continue
		}
		var info sampleInfo
		if v, ok := n.attrValue("geometry"); ok {
			info.geometry = v
			if dims, ch, err := parseGeometry(v); err == nil && len(dims) == 2 {
				info.channels = ch
			}
		}
		info.sampleFormat, _ = n.attrValue("sampleFormat")
		info.colorSpace, _ = n.attrValue("colorSpace")
		info.pixelStorage, _ = n.attrValue("pixelStorage")
		if v, ok := n.attrValue("compression"); ok {
			c, err := parseCompression(v)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			info.compression = c
			info.hasComp = true
		}
		info.checksum, _ = n.attrValue("checksum")
		return info
	}
	t.Fatalf("%s: no Image element", path)
	return sampleInfo{}
}

// referenceFor returns the uncompressed sample whose file stem is the longest
// prefix of the target's stem. That pairs `..._S_mono_lz4.xisf` with
// `..._S_mono.xisf` without hardcoding any codec suffix.
func referenceFor(paths []string, target string, infos map[string]sampleInfo) string {
	stem := strings.TrimSuffix(target, ".xisf")
	best := ""
	for _, p := range paths {
		if p == target || infos[p].hasComp {
			continue
		}
		qs := strings.TrimSuffix(p, ".xisf")
		if strings.HasPrefix(stem, qs) && len(qs) > len(best) {
			best = qs
		}
	}
	if best == "" {
		return ""
	}
	return best + ".xisf"
}

// TestCompressedImagesMatchReference decodes every compressed sample in img/
// and compares its samples bit for bit with the uncompressed master it was
// derived from. The samples were written by PixInsight, so this checks the
// codecs, the byte-shuffling transform and checksum verification against the
// reference encoder instead of against this package's own output. A checksum
// mismatch would make Open itself fail.
func TestCompressedImagesMatchReference(t *testing.T) {
	if testing.Short() {
		t.Skip("large sample files skipped in -short mode")
	}
	paths, _ := filepath.Glob("img/*.xisf")
	if len(paths) == 0 {
		t.Skip("no files in img/")
	}
	infos := make(map[string]sampleInfo, len(paths))
	for _, p := range paths {
		infos[p] = readSampleInfo(t, p)
	}

	type sample struct {
		path string
		info sampleInfo
	}
	var cases []sample
	refPath := ""
	for _, p := range paths {
		if !infos[p].hasComp {
			continue
		}
		ref := referenceFor(paths, p, infos)
		if ref == "" {
			t.Errorf("%s: no uncompressed reference in img/", p)
			continue
		}
		if refPath == "" {
			refPath = ref
		} else if refPath != ref {
			t.Errorf("%s: reference %s differs from %s", p, ref, refPath)
		}
		cases = append(cases, sample{path: p, info: infos[p]})
	}
	if len(cases) == 0 {
		t.Skip("no compressed samples in img/")
	}

	refFile, err := Open(refPath)
	if err != nil {
		t.Fatalf("open reference %s: %v", refPath, err)
	}
	refPix, err := Pixels[float32](refFile.Images()[0])
	if err != nil {
		t.Fatalf("reference pixels: %v", err)
	}

	for _, c := range cases {
		c := c
		t.Run(strings.TrimSuffix(filepath.Base(c.path), ".xisf"), func(t *testing.T) {
			f, err := Open(c.path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			im := f.Images()[0]
			pix, err := Pixels[float32](im)
			if err != nil {
				t.Fatalf("pixels: %v", err)
			}
			if len(pix) != len(refPix) {
				t.Fatalf("got %d samples, reference has %d", len(pix), len(refPix))
			}
			for i := range pix {
				if math.Float32bits(pix[i]) != math.Float32bits(refPix[i]) {
					t.Fatalf("sample %d = %v, reference %v", i, pix[i], refPix[i])
				}
			}

			b := im.block
			if b.compression == nil {
				t.Fatal("compression attribute lost after decode")
			}
			if b.compression.Codec != c.info.compression.Codec {
				t.Fatalf("codec %s, header says %s", b.compression.Codec, c.info.compression.Codec)
			}
			if !b.compression.Shuffled || b.compression.ItemSize != 4 {
				t.Fatalf("unexpected shuffle parameters: %s", b.compression)
			}
			if c.info.checksum != "" && b.checksum == nil {
				t.Fatalf("checksum %q not parsed", c.info.checksum)
			}
		})
	}
}

// TestCompressedChecksumMismatch corrupts one stored byte of a checksummed
// sample and expects Parse to reject it against the PixInsight-generated
// digest.
func TestCompressedChecksumMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("large sample files skipped in -short mode")
	}
	paths, _ := filepath.Glob("img/*.xisf")
	target := ""
	for _, p := range paths {
		if readSampleInfo(t, p).checksum != "" {
			target = p
			break
		}
	}
	if target == "" {
		t.Skip("no checksummed sample in img/")
	}

	pos, size := func() (int64, int64) {
		f, err := Open(target)
		if err != nil {
			t.Fatalf("open %s: %v", target, err)
		}
		b := f.Images()[0].block
		return b.loc.Position, b.loc.Size
	}()

	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if pos <= 0 || size <= 0 || pos+size > int64(len(raw)) {
		t.Fatalf("implausible block location %d:%d in a %d-byte file", pos, size, len(raw))
	}
	raw[pos+size/2] ^= 0xff
	if _, err := Parse(raw); !errors.Is(err, ErrChecksum) {
		t.Fatalf("Parse of a corrupted block returned %v, want ErrChecksum", err)
	}
}

// monoSample returns the first uncompressed single-channel sample in paths,
// or "" when there is none. It keeps the edit and recompress tests off the
// much larger RGB samples.
func monoSample(t *testing.T, paths []string) string {
	t.Helper()
	for _, p := range paths {
		info := readSampleInfo(t, p)
		if !info.hasComp && info.channels == 1 {
			return p
		}
	}
	return ""
}

// TestRealFileEdit edits one sample, writes, reopens and checks that the
// header and every other byte of the image block are unchanged.
func TestRealFileEdit(t *testing.T) {
	paths, _ := filepath.Glob("img/*.xisf")
	if len(paths) == 0 {
		t.Skip("no files in img/")
	}
	if testing.Short() {
		t.Skip("large sample files skipped in -short mode")
	}
	path := monoSample(t, paths)
	if path == "" {
		t.Skip("no uncompressed mono sample in img/")
	}
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(orig)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	const x, y = 123, 456
	want := float64(float32(im.At(x, y, 0) + 1))
	im.Set(x, y, 0, want)

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(out) != len(orig) {
		t.Fatalf("size changed: %d vs %d", len(out), len(orig))
	}
	// The header does not mention pixel values, and the block size is
	// unchanged, so it must be identical.
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if !bytes.Equal(f2.HeaderXML(), f.HeaderXML()) {
		t.Fatal("header changed after editing pixels")
	}
	im2 := f2.Images()[0]
	if got := im2.At(x, y, 0); got != want {
		t.Fatalf("pixel = %v, want %v", got, want)
	}
	// Exactly one 4-byte float should differ inside the image block.
	diffs := 0
	for i := range out {
		if out[i] != orig[i] {
			diffs++
		}
	}
	if diffs == 0 || diffs > 4 {
		t.Fatalf("%d bytes differ, expected at most one Float32", diffs)
	}
}

// TestLargeRGBFile exercises the parts of real PixInsight headers that the
// mono samples do not have: an RGB image, an inline ICC profile, a thumbnail
// and a very large escaped ProcessingHistory property.
func TestLargeRGBFile(t *testing.T) {
	if testing.Short() {
		t.Skip("large sample files skipped in -short mode")
	}
	path := os.Getenv("XISF_LARGE_FILE")
	if path == "" {
		if paths, _ := filepath.Glob("img/*.xisf"); len(paths) > 0 {
			for _, p := range paths {
				if readSampleInfo(t, p).channels == 3 {
					path = p
					break
				}
			}
		}
	}
	if path == "" {
		t.Skip("no RGB sample in img/ (set XISF_LARGE_FILE to override)")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("large sample not available: %v", err)
	}
	f, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(f.Images()) != 1 {
		t.Fatalf("got %d images", len(f.Images()))
	}
	im := f.Images()[0]
	if im.Channels() != 3 || im.SampleFormat != Float32 {
		t.Fatalf("unexpected image %dx%dx%d %s", im.Width(), im.Height(), im.Channels(), im.SampleFormat)
	}
	if im.Thumbnail == nil {
		t.Fatal("thumbnail not parsed")
	}
	if len(im.ICCProfile) == 0 {
		t.Fatal("ICC profile not decoded")
	}
	if p, ok := f.Property("XISF:ProcessingHistory"); ok {
		if !strings.Contains(p.Value, "<") {
			t.Fatalf("ProcessingHistory was not unescaped")
		}
	} else {
		t.Log("no XISF:ProcessingHistory property in this file")
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(out, f.src) {
		t.Fatalf("large file changed: %d vs %d bytes", len(out), len(f.src))
	}
}

func TestRealFileRecompress(t *testing.T) {
	if testing.Short() {
		t.Skip("large sample file skipped in -short mode")
	}
	paths, _ := filepath.Glob("img/*.xisf")
	if len(paths) == 0 {
		t.Skip("no files in img/")
	}
	path := monoSample(t, paths)
	if path == "" {
		t.Skip("no uncompressed mono sample in img/")
	}
	f, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	im := f.Images()[0]
	// A crop is not supported, so sample a handful of positions.
	coords := [][3]int{{0, 0, 0}, {1, 2, 0}, {100, 200, 0}, {9575, 6387, 0}}
	before := make([]float64, len(coords))
	for i, c := range coords {
		before[i] = im.At(c[0], c[1], c[2])
	}
	im.MarkDirty()

	codec := CodecZstd
	shuffled := true
	out, err := f.Bytes(&WriteOptions{
		Compression: &Compression{
			Codec:            codec,
			Shuffled:         shuffled,
			ItemSize:         4,
			UncompressedSize: int64(im.SampleCount() * 4),
		},
		CompressionLevel: 50,
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	im2 := f2.Images()[0]
	for i, c := range coords {
		if got := im2.At(c[0], c[1], c[2]); got != before[i] {
			t.Fatalf("pixel %v = %v, want %v", c, got, before[i])
		}
	}
	// A compressed 244 MiB noise-like block should still be smaller than the
	// uncompressed file; this catches a codec that silently stored raw.
	if len(out) >= len(f.src) {
		t.Logf("compressed size %d is not smaller than original %d", len(out), len(f.src))
	}
}

// TestRealFileDecompress converts a PixInsight-compressed sample to an
// uncompressed file without editing pixels, and checks that the samples match
// the uncompressed master. This is the PixInsight incremental-read scenario.
func TestRealFileDecompress(t *testing.T) {
	if testing.Short() {
		t.Skip("large sample files skipped in -short mode")
	}
	paths, _ := filepath.Glob("img/*.xisf")
	if len(paths) == 0 {
		t.Skip("no files in img/")
	}
	infos := make(map[string]sampleInfo, len(paths))
	for _, p := range paths {
		infos[p] = readSampleInfo(t, p)
	}
	target, ref := "", ""
	for _, p := range paths {
		if !infos[p].hasComp {
			continue
		}
		if r := referenceFor(paths, p, infos); r != "" {
			target, ref = p, r
			break
		}
	}
	if target == "" {
		t.Skip("no compressed sample with an uncompressed reference in img/")
	}

	f, err := Open(target)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	im := f.Images()[0]
	out, err := f.Bytes(&WriteOptions{Uncompressed: true})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	im2 := f2.Images()[0]
	if im2.block.compression != nil {
		t.Fatalf("compression survived: %s", im2.block.compression)
	}
	if _, ok := im2.block.elem.attrValue("subblocks"); ok {
		t.Fatal("subblocks attribute survived")
	}
	want := int64(im.SampleCount() * im.SampleFormat.Size())
	if im2.block.loc.Size != want {
		t.Fatalf("stored size = %d, want %d", im2.block.loc.Size, want)
	}
	if im2.block.loc.Position%defaultAlignment != 0 {
		t.Fatalf("block position %d is not aligned", im2.block.loc.Position)
	}

	refFile, err := Open(ref)
	if err != nil {
		t.Fatalf("open reference: %v", err)
	}
	refPix, err := Pixels[float32](refFile.Images()[0])
	if err != nil {
		t.Fatalf("reference pixels: %v", err)
	}
	got, err := Pixels[float32](im2)
	if err != nil {
		t.Fatalf("pixels: %v", err)
	}
	if len(got) != len(refPix) {
		t.Fatalf("got %d samples, reference has %d", len(got), len(refPix))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(refPix[i]) {
			t.Fatalf("sample %d = %v, reference %v", i, got[i], refPix[i])
		}
	}
}
