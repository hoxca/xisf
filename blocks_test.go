package xisf

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

// buildBlocks fills a header template with attachment locations for the given
// payloads. Each payload's location is written at {{LOC0}}, {{LOC1}}, ...
// The template is re-rendered until the positions stop moving.
func buildBlocks(t *testing.T, tmpl string, payloads [][]byte, align int) []byte {
	t.Helper()
	positions := make([]int64, len(payloads))
	var header string
	converged := false
	for iter := 0; iter < 64; iter++ {
		header = tmpl
		for i := range payloads {
			header = strings.ReplaceAll(header,
				fmt.Sprintf("{{LOC%d}}", i),
				fmt.Sprintf("attachment:%d:%d", positions[i], len(payloads[i])))
		}
		next := make([]int64, len(payloads))
		p := alignUp(int64(PreambleLen+len(header)), int64(align))
		for i, payload := range payloads {
			next[i] = p
			p = alignUp(p+int64(len(payload)), int64(align))
		}
		if slices.Equal(next, positions) {
			converged = true
			break
		}
		positions = next
	}
	if !converged {
		t.Fatal("block layout did not converge")
	}

	out := make([]byte, 0, int(positions[len(positions)-1])+len(payloads[len(payloads)-1]))
	out = append(out, Signature...)
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(header)))
	out = append(out, n[:]...)
	out = append(out, 0, 0, 0, 0)
	out = append(out, header...)
	for i, payload := range payloads {
		for int64(len(out)) < positions[i] {
			out = append(out, 0)
		}
		out = append(out, payload...)
	}
	return out
}

func TestMultipleBlocksAndAncillaryData(t *testing.T) {
	const align = 4096
	imgRaw := testPixels(t, UInt16, 8*8)
	thumbRaw := testPixels(t, UInt8, 4*4)
	icc := []byte("fake ICC profile bytes")
	meta := []byte("property payload")

	tmpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<xisf version="1.0" xmlns="http://www.pixinsight.com/xisf">` +
		`<Image id="main" geometry="8:8:1" sampleFormat="UInt16" bounds="0:65535" location="{{LOC0}}">` +
		`<Thumbnail geometry="4:4:1" sampleFormat="UInt8" location="{{LOC1}}"/>` +
		`<ICCProfile location="{{LOC2}}"/>` +
		`</Image>` +
		`<Metadata>` +
		`<Property id="XISF:BlockAlignmentSize" type="UInt16" value="4096"/>` +
		`<Property id="XISF:Payload" type="ByteArray" location="{{LOC3}}"/>` +
		`</Metadata>` +
		`</xisf>`
	file := buildBlocks(t, tmpl, [][]byte{imgRaw, thumbRaw, icc, meta}, align)

	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	if im.Thumbnail == nil || im.Thumbnail.Width() != 4 {
		t.Fatalf("thumbnail not parsed: %+v", im.Thumbnail)
	}
	if !bytes.Equal(im.ICCProfile, icc) {
		t.Fatalf("ICC profile = %q", im.ICCProfile)
	}

	// Edit the main image and the thumbnail, then write everything back.
	im.Set(1, 2, 0, 321)
	im.Thumbnail.Set(3, 3, 0, 77)
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	im2 := f2.Images()[0]
	if got := im2.At(1, 2, 0); got != 321 {
		t.Fatalf("main pixel = %v", got)
	}
	if im2.Thumbnail == nil {
		t.Fatal("thumbnail lost")
	}
	if got := im2.Thumbnail.At(3, 3, 0); got != 77 {
		t.Fatalf("thumbnail pixel = %v", got)
	}
	if !bytes.Equal(im2.ICCProfile, icc) {
		t.Fatalf("ICC profile changed: %q", im2.ICCProfile)
	}

	// Every attachment must be aligned, in order, and non-overlapping.
	var positions []int64
	for _, b := range f2.blocks {
		if b.loc.Kind == LocAttachment {
			if b.loc.Position%align != 0 {
				t.Fatalf("block at %d is not aligned", b.loc.Position)
			}
			positions = append(positions, b.loc.Position)
		}
	}
	if len(positions) != 4 {
		t.Fatalf("got %d attachment blocks, want 4", len(positions))
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] <= positions[i-1] {
			t.Fatalf("blocks out of order: %v", positions)
		}
	}

	// The metadata payload must be found by the generic block walk.
	if p, ok := f2.Property("XISF:Payload"); !ok || p.Location == nil {
		t.Fatalf("payload property missing: %+v", p)
	}
}

func TestEmbeddedImage(t *testing.T) {
	const w, h = 8, 8
	raw := testPixels(t, Float32, w*h)
	enc := base64.StdEncoding.EncodeToString(raw)

	header := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<xisf version="1.0" xmlns="http://www.pixinsight.com/xisf">` +
		`<Image id="embedded" geometry="8:8:1" sampleFormat="Float32" bounds="0:1" location="embedded">` +
		`<Data encoding="base64">` + enc + `</Data>` +
		`</Image>` +
		`<Metadata><Property id="XISF:CreationTime" type="String">2026-01-01T00:00:00Z</Property></Metadata>` +
		`</xisf>`
	file := make([]byte, 0, PreambleLen+len(header))
	file = append(file, Signature...)
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(header)))
	file = append(file, n[:]...)
	file = append(file, 0, 0, 0, 0)
	file = append(file, header...)

	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	im.Set(2, 3, 0, 0.25)

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := f2.Images()[0].At(2, 3, 0); got != 0.25 {
		t.Fatalf("pixel = %v", got)
	}

	// Now compress the embedded block. The Data element must carry the
	// compression attribute and the payload must still decode.
	im2 := f2.Images()[0]
	im2.Set(0, 0, 0, 0.5)
	out2, err := f2.Bytes(&WriteOptions{
		Compression: &Compression{Codec: CodecZlib, UncompressedSize: int64(len(raw))},
	})
	if err != nil {
		t.Fatalf("compressed write: %v", err)
	}
	end := PreambleLen + 4096
	if end > len(out2) {
		end = len(out2)
	}
	if !bytes.Contains(out2[:end], []byte(`compression="zlib:`)) {
		t.Fatalf("compression attribute not written to <Data>")
	}
	f3, err := Parse(out2)
	if err != nil {
		t.Fatalf("compressed reparse: %v", err)
	}
	if got := f3.Images()[0].At(0, 0, 0); got != 0.5 {
		t.Fatalf("compressed pixel = %v", got)
	}
}

func TestRawSingleSubblockWithCompression(t *testing.T) {
	const w, h = 16, 8
	raw := testPixels(t, UInt16, w*h)
	// A compression attribute with no subblocks and stored bytes of exactly
	// the uncompressed size is a single uncompressed subblock. Decoders must
	// accept it, as earlier revisions could produce it.
	tmpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<xisf version="1.0" xmlns="http://www.pixinsight.com/xisf">` +
		`<Image geometry="16:8:1" sampleFormat="UInt16" bounds="0:65535" location="{{LOC0}}" compression="zlib:` +
		fmt.Sprint(len(raw)) + `"/>` +
		`<Metadata><Property id="XISF:CreationTime" type="String">2026-01-01T00:00:00Z</Property></Metadata>` +
		`</xisf>`
	file := buildBlocks(t, tmpl, [][]byte{raw}, 0)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	for i := 0; i < w*h; i++ {
		if got := im.At(i%w, i/w, 0); got != float64(i*37) {
			t.Fatalf("pixel %d = %v", i, got)
		}
	}
}

func TestIncompressibleBlock(t *testing.T) {
	const w, h = 64, 64
	smooth := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		binary.LittleEndian.PutUint32(smooth[i*4:], math.Float32bits(float32(i%w)/float32(w)))
	}
	file := buildTestFile(t, w, h, 1, Float32, smooth, "", 0)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	f.Images()[0].MarkDirty()
	compressed, err := f.Bytes(&WriteOptions{
		Compression: &Compression{Codec: CodecZlib, UncompressedSize: int64(len(smooth))},
	})
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	// Fill the already-compressed block with data no codec can shrink.
	f2, err := Parse(compressed)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	im2 := f2.Images()[0]
	px, err := Pixels[float32](im2)
	if err != nil {
		t.Fatal(err)
	}
	state := uint32(0x12345678)
	for i := range px {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		px[i] = math.Float32frombits(state)
	}
	im2.MarkDirty()
	out, err := f2.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f3, err := Parse(out)
	if err != nil {
		t.Fatalf("third parse: %v", err)
	}
	comp := f3.Images()[0].block.compression
	if comp == nil {
		t.Fatal("compression attribute was dropped from a compressed block")
	}
	if len(comp.Subblocks) != 1 || comp.Subblocks[0].Compressed != comp.Subblocks[0].Uncompressed {
		t.Fatalf("incompressible block was not stored raw: %+v", comp.Subblocks)
	}
	for i := range px {
		got := f3.Images()[0].At(i%w, i/w, 0)
		want := float64(px[i])
		if math.IsNaN(want) {
			if !math.IsNaN(got) {
				t.Fatalf("pixel %d = %v, want NaN", i, got)
			}
			continue
		}
		if got != want {
			t.Fatalf("pixel %d = %v, want %v", i, got, want)
		}
	}
}

func TestBigEndianCompressedShuffled(t *testing.T) {
	const w, h, ch = 32, 16, 3
	count := w * h * ch
	raw := make([]byte, count*2)
	for i := 0; i < count; i++ {
		binary.BigEndian.PutUint16(raw[i*2:], uint16(i*13))
	}
	file := buildTestFile(t, w, h, ch, UInt16, raw, `byteOrder="big" pixelStorage="Normal"`, 4096)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	im.MarkDirty()
	out, err := f.Bytes(&WriteOptions{
		Compression: &Compression{
			Codec:            CodecLZ4HC,
			Shuffled:         true,
			ItemSize:         2,
			UncompressedSize: int64(len(raw)),
		},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	im2 := f2.Images()[0]
	if im2.PixelStorage != Normal || im2.block.byteOrder != BigEndian {
		t.Fatalf("storage or byte order changed")
	}
	for i := 0; i < count; i++ {
		x, y, c := i%w, (i/w)%h, i/(w*h)
		want := float64(((y*w+x)*ch + c) * 13)
		if got := im2.At(x, y, c); got != want {
			t.Fatalf("(%d,%d,%d) = %v, want %v", x, y, c, got, want)
		}
	}
}

func TestUnknownCodecPreserved(t *testing.T) {
	const w, h = 8, 4
	raw := testPixels(t, UInt8, w*h)
	tmpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<xisf version="1.0" xmlns="http://www.pixinsight.com/xisf">` +
		`<Image geometry="8:4:1" sampleFormat="UInt8" bounds="0:255" location="{{LOC0}}" compression="brotli:` +
		fmt.Sprint(len(raw)) + `"/>` +
		`<Metadata><Property id="XISF:CreationTime" type="String">2026-01-01T00:00:00Z</Property></Metadata>` +
		`</xisf>`
	file := buildBlocks(t, tmpl, [][]byte{raw}, 0)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	if im.Width() != w || im.SampleFormat != UInt8 {
		t.Fatalf("metadata not available: %+v", im)
	}
	if _, err := Pixels[uint8](im); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Pixels error = %v, want ErrUnsupported", err)
	}
	// The unavailable block must still be preserved.
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(out, file) {
		t.Fatal("file with an unknown codec was not preserved")
	}
}

func TestUnknownChecksumPreserved(t *testing.T) {
	const w, h = 8, 4
	raw := testPixels(t, UInt8, w*h)
	tmpl := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<xisf version="1.0" xmlns="http://www.pixinsight.com/xisf">` +
		`<Image geometry="8:4:1" sampleFormat="UInt8" bounds="0:255" location="{{LOC0}}" checksum="crc32:12345678"/>` +
		`<Metadata><Property id="XISF:CreationTime" type="String">2026-01-01T00:00:00Z</Property></Metadata>` +
		`</xisf>`
	file := buildBlocks(t, tmpl, [][]byte{raw}, 0)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := f.Images()[0].At(1, 1, 0); got != float64(1*w+1) {
		t.Fatalf("pixel = %v", got)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(out, file) {
		t.Fatal("file with an unknown checksum was not preserved")
	}
}

func TestCreationTimeAndCreator(t *testing.T) {
	file := buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := f.Bytes(&WriteOptions{
		UpdateCreationTime: true,
		CreatorApplication: "xisf-test-v2",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if p, ok := f2.Property("XISF:CreatorApplication"); !ok || p.Value != "xisf-test-v2" {
		t.Fatalf("creator = %+v", p)
	}
	if p, ok := f2.Property("XISF:CreationTime"); !ok || p.Value == "2026-01-01T00:00:00Z" {
		t.Fatalf("creation time not updated: %+v", p)
	}
}

func TestSubblockLayoutPreserved(t *testing.T) {
	const w, h = 64, 64
	raw := testPixels(t, Float32, w*h)
	file := buildTestFile(t, w, h, 1, Float32, raw, "", 0)

	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	im.MarkDirty()
	comp := &Compression{
		Codec:            CodecZlib,
		UncompressedSize: int64(len(raw)),
		Subblocks:        []Subblock{{Compressed: 0, Uncompressed: int64(len(raw) / 2)}, {Compressed: 0, Uncompressed: int64(len(raw) / 2)}},
	}
	out, err := f.Bytes(&WriteOptions{Compression: comp})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got := f2.Images()[0].block.compression; got == nil || len(got.Subblocks) != 2 {
		t.Fatalf("subblocks not written: %+v", got)
	}

	// A subsequent unchanged write must keep the subblock table.
	out2, err := f2.Bytes(nil)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	f3, err := Parse(out2)
	if err != nil {
		t.Fatalf("second reparse: %v", err)
	}
	if got := f3.Images()[0].block.compression; got == nil || len(got.Subblocks) != 2 {
		t.Fatalf("subblocks lost on rewrite: %+v", got)
	}
}
