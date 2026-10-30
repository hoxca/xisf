package xisf

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestNewImageMonoFloat32(t *testing.T) {
	const w, h = 5, 3
	pixels := make([]float32, w*h)
	for i := range pixels {
		pixels[i] = float32(i) / 10
	}

	f, err := NewImage(ImageSpec{
		Width:     w,
		Height:    h,
		Channels:  1,
		ID:        "mono",
		ImageType: "MasterLight",
	}, pixels)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	im := f.Images()[0]
	if im.Width() != w || im.Height() != h || im.Channels() != 1 {
		t.Fatalf("geometry = %d×%d×%d", im.Width(), im.Height(), im.Channels())
	}
	if im.ColorSpace != Gray || im.SampleFormat != Float32 {
		t.Fatalf("format = %s %s", im.ColorSpace, im.SampleFormat)
	}
	if !im.HasBounds || im.Bounds != [2]float64{0, 1} {
		t.Fatalf("bounds = %v, %v", im.Bounds, im.HasBounds)
	}
	if im.ID != "mono" || im.ImageType != "MasterLight" {
		t.Fatalf("image attributes = %q %q", im.ID, im.ImageType)
	}
	if p, ok := f.Property("XISF:CreationTime"); !ok || p.Type != "String" || p.Value == "" {
		t.Fatalf("creation time = %+v, %v", p, ok)
	}
	if p, ok := f.Property("XISF:CreatorApplication"); !ok || p.Value != "xisf" {
		t.Fatalf("creator application = %+v, %v", p, ok)
	}
	if p, ok := f.Property("XISF:BlockAlignmentSize"); !ok || p.Type != "UInt16" || p.Value != "4096" {
		t.Fatalf("block alignment = %+v, %v", p, ok)
	}

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p, ok := f2.Property("XISF:BlockAlignmentSize"); !ok || p.Value != "4096" {
		t.Fatalf("written block alignment = %+v, %v", p, ok)
	}
	im2 := f2.Images()[0]
	got, err := Pixels[float32](im2)
	if err != nil {
		t.Fatalf("Pixels: %v", err)
	}
	if len(got) != len(pixels) {
		t.Fatalf("got %d samples, want %d", len(got), len(pixels))
	}
	for i := range got {
		if got[i] != pixels[i] {
			t.Fatalf("pixel %d = %v, want %v", i, got[i], pixels[i])
		}
	}
	if im2.block.loc.Kind != LocAttachment {
		t.Fatalf("location kind = %v, want attachment", im2.block.loc.Kind)
	}
	if im2.block.loc.Position%defaultAlignment != 0 {
		t.Fatalf("data position %d is not aligned to %d", im2.block.loc.Position, defaultAlignment)
	}
}

func TestNewImageRGBNormal(t *testing.T) {
	const w, h = 4, 3
	pixels := make([]float32, 3*w*h)
	for i := range pixels {
		pixels[i] = float32(i) / 100
	}

	f, err := NewImage(ImageSpec{
		Width:        w,
		Height:       h,
		Channels:     3,
		PixelStorage: Normal,
		ID:           "rgb",
	}, pixels)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	im := f.Images()[0]
	if im.ColorSpace != RGB || im.Channels() != 3 || im.PixelStorage != Normal {
		t.Fatalf("format = %s ×%d %s", im.ColorSpace, im.Channels(), im.PixelStorage)
	}

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	im2 := f2.Images()[0]
	if im2.ColorSpace != RGB || im2.PixelStorage != Normal {
		t.Fatalf("parsed format = %s %s", im2.ColorSpace, im2.PixelStorage)
	}
	got, err := Pixels[float32](im2)
	if err != nil {
		t.Fatalf("Pixels: %v", err)
	}
	for i := range got {
		if got[i] != pixels[i] {
			t.Fatalf("pixel %d = %v, want %v", i, got[i], pixels[i])
		}
	}
}

func TestNewImageCIELab(t *testing.T) {
	const w, h = 2, 2
	pixels := make([]float32, 3*w*h)
	for i := range pixels {
		pixels[i] = float32(i)
	}
	f, err := NewImage(ImageSpec{
		Width:      w,
		Height:     h,
		Channels:   3,
		ColorSpace: CIELab,
	}, pixels)
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
	if got := f2.Images()[0].ColorSpace; got != CIELab {
		t.Fatalf("colour space = %s, want CIELab", got)
	}
}

func TestNewImageMonoUInt16(t *testing.T) {
	const w, h = 3, 2
	pixels := []uint16{0, 1, 2, 3, 4, 5}
	f, err := NewImage(ImageSpec{Width: w, Height: h, Channels: 1}, pixels)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	im := f.Images()[0]
	if im.SampleFormat != UInt16 {
		t.Fatalf("sample format = %s, want UInt16", im.SampleFormat)
	}
	if im.HasBounds {
		t.Fatalf("integer image has bounds %v", im.Bounds)
	}

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	im2 := f2.Images()[0]
	if im2.HasBounds {
		t.Fatalf("written integer image has bounds")
	}
	got, err := Pixels[uint16](im2)
	if err != nil {
		t.Fatalf("Pixels: %v", err)
	}
	for i := range got {
		if got[i] != pixels[i] {
			t.Fatalf("pixel %d = %d, want %d", i, got[i], pixels[i])
		}
	}
}

func TestNewImageProperties(t *testing.T) {
	f, err := NewImage(ImageSpec{Width: 2, Height: 2, Channels: 1}, []float32{0, 0, 0, 0})
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	im := f.Images()[0]
	if err := im.AddProperty(Property{
		ID:    "PCL:OverridableIdentifier",
		Type:  "Boolean",
		Value: "false",
	}); err != nil {
		t.Fatalf("AddProperty: %v", err)
	}
	if err := f.AddProperty(Property{
		ID:    "Test:Unit",
		Type:  "String",
		Value: "unit value",
	}); err != nil {
		t.Fatalf("AddProperty: %v", err)
	}

	out, err := f.Bytes(nil)
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	im2 := f2.Images()[0]
	if p, ok := im2.Property("PCL:OverridableIdentifier"); !ok || p.Type != "Boolean" || p.Value != "false" {
		t.Fatalf("image property = %+v, %v", p, ok)
	}
	if p, ok := f2.Property("Test:Unit"); !ok || p.Value != "unit value" {
		t.Fatalf("unit property = %+v, %v", p, ok)
	}
}

func TestNewImageCompressionAndChecksum(t *testing.T) {
	const w, h = 16, 8
	pixels := make([]float32, 3*w*h)
	for i := range pixels {
		pixels[i] = float32(i % 7)
	}
	f, err := NewImage(ImageSpec{
		Width:        w,
		Height:       h,
		Channels:     3,
		PixelStorage: Normal,
	}, pixels)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	out, err := f.Bytes(&WriteOptions{
		Compression: &Compression{Codec: CodecZlib},
		Checksum:    checksumPtr(SHA256),
	})
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	im2 := f2.Images()[0]
	if im2.block.compression == nil || im2.block.compression.Codec != CodecZlib {
		t.Fatalf("compression = %+v", im2.block.compression)
	}
	if im2.block.checksum == nil || im2.block.checksum.Algorithm != SHA256 {
		t.Fatalf("checksum = %+v", im2.block.checksum)
	}
	got, err := Pixels[float32](im2)
	if err != nil {
		t.Fatalf("Pixels: %v", err)
	}
	for i := range got {
		if got[i] != pixels[i] {
			t.Fatalf("pixel %d = %v, want %v", i, got[i], pixels[i])
		}
	}
}

func checksumPtr(a ChecksumAlgorithm) *ChecksumAlgorithm { return &a }

func TestNewImageWriteFile(t *testing.T) {
	const w, h = 2, 2
	pixels := []float32{1, 2, 3, 4}
	f, err := NewImage(ImageSpec{Width: w, Height: h, Channels: 1}, pixels)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	path := filepath.Join(t.TempDir(), "new.xisf")
	if err := f.WriteFile(path, nil); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f2, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := Pixels[float32](f2.Images()[0])
	if err != nil {
		t.Fatalf("Pixels: %v", err)
	}
	for i := range got {
		if got[i] != pixels[i] {
			t.Fatalf("pixel %d = %v, want %v", i, got[i], pixels[i])
		}
	}
}

func TestNewImageUsesSliceDirectly(t *testing.T) {
	pixels := []float32{1, 2, 3, 4}
	f, err := NewImage(ImageSpec{Width: 2, Height: 2, Channels: 1}, pixels)
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	got, err := Pixels[float32](f.Images()[0])
	if err != nil {
		t.Fatalf("Pixels: %v", err)
	}
	if &got[0] != &pixels[0] {
		t.Fatal("NewImage copied the pixel slice")
	}
	pixels[2] = 99
	if got[2] != 99 {
		t.Fatalf("pixel change is not visible through the image: %v", got[2])
	}
}

func TestNewImageInvalidSpec(t *testing.T) {
	good := []float32{0, 0, 0}
	tests := []struct {
		name   string
		spec   ImageSpec
		pixels []float32
	}{
		{"zero width", ImageSpec{Width: 0, Height: 1, Channels: 1}, nil},
		{"negative height", ImageSpec{Width: 1, Height: -1, Channels: 1}, nil},
		{"two channels", ImageSpec{Width: 1, Height: 1, Channels: 2}, nil},
		{"bad pixel storage", ImageSpec{Width: 1, Height: 1, Channels: 1, PixelStorage: PixelStorage(99)}, good},
		{"bad colour space", ImageSpec{Width: 1, Height: 1, Channels: 3, ColorSpace: ColorSpace(99)}, good},
		{"gray with rgb space", ImageSpec{Width: 1, Height: 1, Channels: 1, ColorSpace: RGB}, []float32{0}},
		{"wrong sample count", ImageSpec{Width: 2, Height: 2, Channels: 1}, good},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewImage(tt.spec, tt.pixels); !errors.Is(err, ErrGeometry) {
				t.Fatalf("error = %v, want ErrGeometry", err)
			}
		})
	}
}

func TestNewImageBoundsOverride(t *testing.T) {
	f, err := NewImage(ImageSpec{
		Width:     1,
		Height:    1,
		Channels:  1,
		Bounds:    [2]float64{-1, 2},
		HasBounds: true,
	}, []float32{0})
	if err != nil {
		t.Fatalf("NewImage: %v", err)
	}
	if im := f.Images()[0]; !im.HasBounds || im.Bounds != [2]float64{-1, 2} {
		t.Fatalf("bounds = %v, %v", im.Bounds, im.HasBounds)
	}
}
