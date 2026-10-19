package xisf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"
)

// buildTestFile constructs a small monolithic XISF file. It exists so the
// tests can cover formats and codecs that the real sample files do not use.
//
// extraAttrs is appended to the Image start tag (e.g. `pixelStorage="Normal"`).
func buildTestFile(t *testing.T, w, h, channels int, sf SampleFormat, pixels []byte, extraAttrs string, align int) []byte {
	t.Helper()
	img := fmt.Sprintf(`<Image id="test" geometry="%d:%d:%d" sampleFormat="%s" bounds="0:1" colorSpace="Gray"`,
		w, h, channels, sf)
	if extraAttrs != "" {
		img += " " + extraAttrs
	}

	meta := `<Metadata>` +
		`<Property id="XISF:CreationTime" type="String">2026-01-01T00:00:00Z</Property>` +
		`<Property id="XISF:CreatorApplication" type="String">xisf-test</Property>`
	if align > 1 {
		meta += fmt.Sprintf(`<Property id="XISF:BlockAlignmentSize" type="UInt16" value="%d"/>`, align)
	}
	meta += `</Metadata>`

	makeHeader := func(position, size int) string {
		return `<?xml version="1.0" encoding="UTF-8"?>` +
			`<xisf version="1.0" xmlns="http://www.pixinsight.com/xisf">` +
			img + fmt.Sprintf(` location="attachment:%d:%d"/>`, position, size) +
			meta + `</xisf>`
	}

	// The position depends on the header length, which depends on the number
	// of digits in the position. Iterate to a fixed point.
	header := makeHeader(0, len(pixels))
	position := alignUp(int64(PreambleLen+len(header)), int64(align))
	for i := 0; i < 32; i++ {
		h := makeHeader(int(position), len(pixels))
		next := alignUp(int64(PreambleLen+len(h)), int64(align))
		header = h
		if next == position {
			break
		}
		position = next
	}

	out := make([]byte, 0, int(position)+len(pixels))
	out = append(out, Signature...)
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(header)))
	out = append(out, n[:]...)
	out = append(out, 0, 0, 0, 0)
	out = append(out, header...)
	for int64(len(out)) < position {
		out = append(out, 0)
	}
	out = append(out, pixels...)
	return out
}

func testPixels(t *testing.T, sf SampleFormat, count int) []byte {
	t.Helper()
	switch sf {
	case UInt8:
		b := make([]byte, count)
		for i := range b {
			b[i] = byte(i)
		}
		return b
	case UInt16:
		b := make([]byte, count*2)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint16(b[i*2:], uint16(i*37))
		}
		return b
	case UInt32:
		b := make([]byte, count*4)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint32(b[i*4:], uint32(i*100003))
		}
		return b
	case UInt64:
		b := make([]byte, count*8)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint64(b[i*8:], uint64(i)*1e12)
		}
		return b
	case Float32:
		b := make([]byte, count*4)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(float32(i)/float32(count)))
		}
		return b
	case Float64:
		b := make([]byte, count*8)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint64(b[i*8:], math.Float64bits(float64(i)/float64(count)))
		}
		return b
	case Complex32:
		b := make([]byte, count*8)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint32(b[i*8:], math.Float32bits(float32(i)))
			binary.LittleEndian.PutUint32(b[i*8+4:], math.Float32bits(float32(-i)))
		}
		return b
	case Complex64:
		b := make([]byte, count*16)
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint64(b[i*16:], math.Float64bits(float64(i)))
			binary.LittleEndian.PutUint64(b[i*16+8:], math.Float64bits(float64(-i)))
		}
		return b
	}
	t.Fatalf("unknown format")
	return nil
}

func TestRoundTripSampleFormats(t *testing.T) {
	formats := []SampleFormat{UInt8, UInt16, UInt32, UInt64, Float32, Float64, Complex32, Complex64}
	for _, sf := range formats {
		t.Run(sf.String(), func(t *testing.T) {
			const w, h, ch = 16, 8, 2
			raw := testPixels(t, sf, w*h*ch)
			file := buildTestFile(t, w, h, ch, sf, raw, "", 0)
			f, err := Parse(file)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(f.Images()) != 1 {
				t.Fatalf("got %d images", len(f.Images()))
			}
			im := f.Images()[0]
			if im.Width() != w || im.Height() != h || im.Channels() != ch {
				t.Fatalf("geometry %dx%dx%d", im.Width(), im.Height(), im.Channels())
			}

			// An untouched file must be reproduced byte for byte.
			out, err := f.Bytes(nil)
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			if !bytes.Equal(out, file) {
				t.Fatalf("clean round trip differs: %d vs %d bytes", len(out), len(file))
			}

			// Editing one sample must survive the write.
			if sf == Complex32 || sf == Complex64 {
				im.SetComplex(3, 2, 1, complex(1.5, -2.5))
			} else {
				im.Set(3, 2, 1, 42)
			}
			out, err = f.Bytes(nil)
			if err != nil {
				t.Fatalf("write edited: %v", err)
			}
			f2, err := Parse(out)
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			im2 := f2.Images()[0]
			if sf == Complex32 || sf == Complex64 {
				if got := im2.AtComplex(3, 2, 1); got != complex(1.5, -2.5) {
					t.Fatalf("complex sample = %v", got)
				}
			} else if got := im2.At(3, 2, 1); got != 42 {
				t.Fatalf("sample = %v", got)
			}
		})
	}
}

func TestCompressionMatrix(t *testing.T) {
	codecs := []struct {
		c     Codec
		shuf  bool
		item  int
		level int
	}{
		{CodecZlib, false, 0, 0},
		{CodecZlib, true, 4, 0},
		{CodecLZ4, false, 0, 0},
		{CodecLZ4, true, 4, 0},
		{CodecLZ4HC, false, 0, 0},
		{CodecLZ4HC, true, 4, 0},
		{CodecZstd, false, 0, 0},
		{CodecZstd, true, 4, 0},
	}
	const w, h, ch = 64, 64, 1
	// Smooth data compresses; a little noise keeps it honest.
	raw := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		v := float32(i%w) / float32(w)
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(v))
	}
	file := buildTestFile(t, w, h, ch, Float32, raw, "", 0)

	for _, tc := range codecs {
		name := string(tc.c)
		if tc.shuf {
			name += "+sh"
		}
		t.Run(name, func(t *testing.T) {
			f, err := Parse(file)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			im := f.Images()[0]
			im.MarkDirty()
			comp := &Compression{Codec: tc.c, Shuffled: tc.shuf, ItemSize: tc.item, UncompressedSize: int64(len(raw))}
			opts := &WriteOptions{Compression: comp, CompressionLevel: tc.level}
			out, err := f.Bytes(opts)
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			if bytes.Equal(out, file) {
				t.Fatalf("block was not compressed")
			}
			f2, err := Parse(out)
			if err != nil {
				t.Fatalf("reparse: %v", err)
			}
			im2 := f2.Images()[0]
			for _, p := range [][3]int{{0, 0, 0}, {5, 7, 0}, {63, 63, 0}} {
				if im.At(p[0], p[1], p[2]) != im2.At(p[0], p[1], p[2]) {
					t.Fatalf("pixel %v differs", p)
				}
			}
			// Write the reopened file back unchanged: it must be identical.
			out2, err := f2.Bytes(nil)
			if err != nil {
				t.Fatalf("second write: %v", err)
			}
			if !bytes.Equal(out, out2) {
				t.Fatalf("compressed file did not round trip")
			}
		})
	}
}

func TestNormalStorage(t *testing.T) {
	const w, h, ch = 4, 3, 3
	// Stored interleaved: pixel by pixel, channels consecutive.
	raw := make([]byte, w*h*ch*2)
	for p := 0; p < w*h; p++ {
		for c := 0; c < ch; c++ {
			binary.LittleEndian.PutUint16(raw[(p*ch+c)*2:], uint16(p*10+c))
		}
	}
	file := buildTestFile(t, w, h, ch, UInt16, raw, `pixelStorage="Normal"`, 0)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	for p := 0; p < w*h; p++ {
		for c := 0; c < ch; c++ {
			x, y := p%w, p/w
			if got := im.At(x, y, c); got != float64(p*10+c) {
				t.Fatalf("(%d,%d,%d) = %v, want %d", x, y, c, got, p*10+c)
			}
		}
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(out, file) {
		t.Fatalf("Normal-storage round trip differs")
	}
}

func TestBigEndian(t *testing.T) {
	const w, h, ch = 4, 4, 1
	count := w * h * ch
	raw := make([]byte, count*2)
	for i := 0; i < count; i++ {
		binary.BigEndian.PutUint16(raw[i*2:], uint16(i*100))
	}
	file := buildTestFile(t, w, h, ch, UInt16, raw, `byteOrder="big"`, 0)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	if got := im.At(2, 1, 0); got != float64((1*w+2)*100) {
		t.Fatalf("got %v", got)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(out, file) {
		t.Fatalf("big-endian round trip differs")
	}
}

func TestChecksumVerifyAndRecompute(t *testing.T) {
	const w, h, ch = 8, 8, 1
	raw := testPixels(t, Float32, w*h*ch)
	file := buildTestFile(t, w, h, ch, Float32, raw, "", 0)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := f.Images()[0]
	im.Set(1, 1, 0, 1)
	alg := SHA256
	out, err := f.Bytes(&WriteOptions{Checksum: &alg})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if !strings.Contains(string(f2.HeaderXML()), "checksum=\"sha-256:") {
		t.Fatalf("checksum attribute not written")
	}

	// Corrupting the stored block must be detected before decompression.
	idx := bytes.Index(out, []byte("checksum=\"sha-256:"))
	if idx < 0 {
		t.Fatal("no checksum")
	}
	corrupt := append([]byte(nil), out...)
	// The block ends the file; flip its last byte.
	corrupt[len(corrupt)-1] ^= 0xff
	if _, err := Parse(corrupt); err == nil {
		t.Fatal("corruption was not detected")
	}
}

func TestAlignment(t *testing.T) {
	const w, h, ch = 16, 16, 1
	raw := testPixels(t, UInt8, w*h*ch)
	file := buildTestFile(t, w, h, ch, UInt8, raw, "", 4096)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	loc := strings.Index(string(out), "location=\"attachment:")
	if loc < 0 {
		t.Fatal("no location")
	}
	// Extract the position and check it is a multiple of the alignment.
	var pos int64
	if _, err := fmt.Sscanf(string(out[loc:]), "location=\"attachment:%d:", &pos); err != nil {
		t.Fatalf("parse location: %v", err)
	}
	if pos%4096 != 0 {
		t.Fatalf("block position %d is not 4096-aligned", pos)
	}
	if !bytes.Equal(out, file) {
		t.Fatalf("aligned round trip differs")
	}
}

func TestPropertyAccess(t *testing.T) {
	file := buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0)
	f, err := Parse(file)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p, ok := f.Property("XISF:CreatorApplication"); !ok || p.Value != "xisf-test" {
		t.Fatalf("metadata property = %+v, %v", p, ok)
	}
	if f.Version() != "1.0" {
		t.Fatalf("version %q", f.Version())
	}
}

func TestTruncatedAndMalformed(t *testing.T) {
	good := buildTestFile(t, 2, 2, 1, UInt8, []byte{1, 2, 3, 4}, "", 0)
	for n := 0; n < len(good); n++ {
		if _, err := Parse(good[:n]); err == nil {
			// A prefix that happens to be a complete header but no block is
			// not valid either; only the empty prefix is excused.
			t.Fatalf("truncation at %d accepted", n)
		}
	}
}
