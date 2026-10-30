package xisf

import (
	"bytes"
	"testing"
)

// newCompressedTestFile builds a zlib+shuffled, SHA-256 checked image and
// returns a parsed standalone copy, so tests can write it back with different
// options.
func newCompressedTestFile(t *testing.T, w, h int) (*File, []float32, []byte) {
	t.Helper()
	pixels := make([]float32, w*h)
	for i := range pixels {
		pixels[i] = float32(i) / 100
	}
	f, err := NewImage(ImageSpec{Width: w, Height: h, Channels: 1}, pixels)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	data, err := f.Bytes(&WriteOptions{
		Compression: &Compression{Codec: CodecZlib, Shuffled: true, ItemSize: 4},
		Checksum:    checksumPtr(SHA256),
	})
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f2.Images()[0].block.compression == nil {
		t.Fatal("test file is not compressed")
	}
	return f2, pixels, data
}

func checkPixels(t *testing.T, im *Image, want []float32) {
	t.Helper()
	got, err := Pixels[float32](im)
	if err != nil {
		t.Fatalf("Pixels: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d samples, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("pixel %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestUncompressedWrite converts a compressed file without touching its
// pixels: the block must be stored raw, at the full sample size.
func TestUncompressedWrite(t *testing.T) {
	const w, h = 16, 8
	f, pixels, compressed := newCompressedTestFile(t, w, h)

	out, err := f.Bytes(&WriteOptions{Uncompressed: true})
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if bytes.Equal(out, compressed) {
		t.Fatal("uncompressed output is identical to the compressed source")
	}

	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	im := f2.Images()[0]
	if im.block.compression != nil {
		t.Fatalf("compression = %+v, want none", im.block.compression)
	}
	if _, ok := im.block.elem.attrValue("subblocks"); ok {
		t.Fatal("subblocks attribute survived the conversion")
	}
	if want := int64(w * h * 4); im.block.loc.Size != want {
		t.Fatalf("stored size = %d, want %d (raw Float32 samples)", im.block.loc.Size, want)
	}
	if im.block.loc.Position%defaultAlignment != 0 {
		t.Fatalf("block position %d is not aligned to %d", im.block.loc.Position, defaultAlignment)
	}
	// The checksum algorithm survives the conversion and covers the new bytes.
	if im.block.checksum == nil || im.block.checksum.Algorithm != SHA256 {
		t.Fatalf("checksum = %+v, want SHA-256", im.block.checksum)
	}
	checkPixels(t, im, pixels)

	// A later write on the same File must not resurrect the old codec.
	again, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("second Bytes: %v", err)
	}
	f3, err := Parse(again)
	if err != nil {
		t.Fatalf("Parse second write: %v", err)
	}
	if c := f3.Images()[0].block.compression; c != nil {
		t.Fatalf("second write reintroduced compression %s", c)
	}
	checkPixels(t, f3.Images()[0], pixels)
}

// TestUncompressedOnUncompressedFileIsByteIdentical keeps the round-trip
// guarantee: the option is a no-op when nothing is compressed.
func TestUncompressedOnUncompressedFileIsByteIdentical(t *testing.T) {
	pixels := []float32{1, 2, 3, 4}
	f, err := NewImage(ImageSpec{Width: 2, Height: 2, Channels: 1}, pixels)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	again, err := f2.Bytes(&WriteOptions{Uncompressed: true})
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if !bytes.Equal(again, out) {
		t.Fatal("uncompressed rewrite of an uncompressed file is not byte-identical")
	}
}

// TestUncompressedOverridesCompression checks the documented precedence.
func TestUncompressedOverridesCompression(t *testing.T) {
	pixels := make([]float32, 8*4)
	for i := range pixels {
		pixels[i] = float32(i % 3)
	}
	f, err := NewImage(ImageSpec{Width: 8, Height: 4, Channels: 1}, pixels)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	out, err := f.Bytes(&WriteOptions{
		Compression:  &Compression{Codec: CodecZstd},
		Uncompressed: true,
	})
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	im := f2.Images()[0]
	if im.block.compression != nil {
		t.Fatalf("compression = %+v, want none", im.block.compression)
	}
	checkPixels(t, im, pixels)
}

// TestUncompressedKeepsMetadata checks that only the image block changes.
func TestUncompressedKeepsMetadata(t *testing.T) {
	const w, h = 8, 4
	f, _, _ := newCompressedTestFile(t, w, h)
	if err := f.SetProperty(Property{ID: "Test:Keep", Type: "String", Value: "value"}); err != nil {
		t.Fatalf("SetProperty: %v", err)
	}
	out, err := f.Bytes(&WriteOptions{Uncompressed: true})
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p, ok := f2.Property("Test:Keep"); !ok || p.Value != "value" {
		t.Fatalf("property = %+v, %v", p, ok)
	}
	if p, ok := f2.Property("XISF:BlockAlignmentSize"); !ok || p.Value != "4096" {
		t.Fatalf("alignment property = %+v, %v", p, ok)
	}
}
