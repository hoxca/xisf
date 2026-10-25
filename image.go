package xisf

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unsafe"
)

// SampleFormat is the type of one pixel sample.
type SampleFormat int

const (
	// UInt8 is an 8-bit unsigned integer sample.
	UInt8 SampleFormat = iota
	// UInt16 is a 16-bit unsigned integer sample.
	UInt16
	// UInt32 is a 32-bit unsigned integer sample.
	UInt32
	// UInt64 is a 64-bit unsigned integer sample.
	UInt64
	// Float32 is a single-precision floating point sample.
	Float32
	// Float64 is a double-precision floating point sample.
	Float64
	// Complex32 is a pair of Float32 components, real then imaginary.
	Complex32
	// Complex64 is a pair of Float64 components, real then imaginary.
	Complex64
)

func parseSampleFormat(s string) (SampleFormat, error) {
	switch s {
	case "UInt8":
		return UInt8, nil
	case "UInt16":
		return UInt16, nil
	case "UInt32":
		return UInt32, nil
	case "UInt64":
		return UInt64, nil
	case "Float32":
		return Float32, nil
	case "Float64":
		return Float64, nil
	case "Complex32":
		return Complex32, nil
	case "Complex64":
		return Complex64, nil
	default:
		return 0, errf(ErrUnsupported, "unknown sampleFormat %q", s)
	}
}

func (f SampleFormat) String() string {
	switch f {
	case UInt8:
		return "UInt8"
	case UInt16:
		return "UInt16"
	case UInt32:
		return "UInt32"
	case UInt64:
		return "UInt64"
	case Float32:
		return "Float32"
	case Float64:
		return "Float64"
	case Complex32:
		return "Complex32"
	case Complex64:
		return "Complex64"
	}
	return "unknown"
}

// Size is the number of bytes in one sample, including both components of a
// complex sample.
func (f SampleFormat) Size() int {
	switch f {
	case UInt8:
		return 1
	case UInt16:
		return 2
	case UInt32, Float32:
		return 4
	case UInt64, Float64, Complex32:
		return 8
	case Complex64:
		return 16
	}
	return 0
}

// UnitSize is the size of the scalar the sample is built from. It is the
// width that byte shuffling and byte order apply to: 4 bytes for Complex32
// (two Float32 components), 8 for Complex64.
func (f SampleFormat) UnitSize() int {
	switch f {
	case Complex32:
		return 4
	case Complex64:
		return 8
	}
	return f.Size()
}

func (f SampleFormat) IsFloat() bool {
	return f == Float32 || f == Float64 || f == Complex32 || f == Complex64
}

// ColorSpace is the colour model of an image.
type ColorSpace int

const (
	// Gray is a single-channel colour space.
	Gray ColorSpace = iota
	// RGB is a three-channel additive colour space.
	RGB
	// CIELab is a three-channel perceptual colour space.
	CIELab
)

func parseColorSpace(s string) (ColorSpace, error) {
	switch s {
	case "Gray":
		return Gray, nil
	case "RGB":
		return RGB, nil
	case "CIELab":
		return CIELab, nil
	default:
		return 0, errf(ErrUnsupported, "unknown colorSpace %q", s)
	}
}

func (c ColorSpace) String() string {
	switch c {
	case RGB:
		return "RGB"
	case CIELab:
		return "CIELab"
	}
	return "Gray"
}

func (c ColorSpace) NominalChannels() int {
	if c == RGB || c == CIELab {
		return 3
	}
	return 1
}

// PixelStorage says how channels are interleaved in the stored block.
type PixelStorage int

const (
	// Planar stores whole channels contiguously. It is the default and the
	// layout of every pixel slice this package returns.
	Planar PixelStorage = iota
	// Normal stores samples pixel by pixel with channels consecutive.
	Normal
)

func parsePixelStorage(s string) (PixelStorage, error) {
	switch s {
	case "Planar":
		return Planar, nil
	case "Normal":
		return Normal, nil
	default:
		return 0, errf(ErrUnsupported, "unknown pixelStorage %q", s)
	}
}

func (p PixelStorage) String() string {
	if p == Normal {
		return "Normal"
	}
	return "Planar"
}

// FITSKeyword is one FITS header keyword attached to an image.
type FITSKeyword struct {
	// Name is the keyword, up to eight characters, not space padded.
	Name string
	// Value is the serialized value.
	Value string
	// Comment is the optional trailing comment.
	Comment string
}

// Image is one <Image> core element and its pixel data.
//
// In memory the samples are always planar: channel by channel, each channel
// row by row. A file stored in the Normal model is interleaved on read and
// re-interleaved on write, so the rest of the program never has to care.
//
// The exported metadata fields are written back when the image's pixels are
// dirty, so changing ID, ImageType, ColorSpace and so on takes effect.
// FITSKeywords, ICCProfile and Thumbnail are read-only for now except for the
// thumbnail's own pixels.
type Image struct {
	// ID is the id attribute, unique within the file.
	ID string
	// UUID is the image's optional universally unique identifier.
	UUID string
	// ImageType is a free-form classification such as "MasterLight".
	ImageType string
	// SampleFormat is the type of one sample.
	SampleFormat SampleFormat
	// ColorSpace is the colour model.
	ColorSpace ColorSpace
	// PixelStorage says how channels were interleaved in the file. Pixel
	// slices are always planar; this is preserved for writing.
	PixelStorage PixelStorage
	// Bounds is the declared representable range when HasBounds is set.
	Bounds [2]float64
	// HasBounds reports whether the file declared a bounds attribute.
	HasBounds bool
	// Offset is the zero point of the representable range, when present.
	Offset float64
	// HasOffset reports whether the file declared an offset attribute.
	HasOffset bool
	// Orientation is the mirroring attribute, e.g. "horizontal".
	Orientation string
	// FITSKeywords are the FITS header records attached to the image.
	FITSKeywords []FITSKeyword

	// Thumbnail is a preview associated with the image, if present.
	Thumbnail *Image
	// ICCProfile holds the decoded ICC profile bytes, if present.
	ICCProfile []byte

	dims        []int
	channels    int
	data        any
	dirty       bool
	headerDirty bool
	byteOrder   ByteOrder
	block       *dataBlock
	elem        *xmlNode
	src         []byte
	importErr   error
}

// Dims returns the image's dimensions, fastest-varying first, without the
// channel count.
func (im *Image) Dims() []int { return append([]int(nil), im.dims...) }

// Width is the length of the first dimension.
func (im *Image) Width() int {
	if len(im.dims) > 0 {
		return im.dims[0]
	}
	return 0
}

// Height is the length of the second dimension.
func (im *Image) Height() int {
	if len(im.dims) > 1 {
		return im.dims[1]
	}
	return 0
}

// Channels is the number of channels.
func (im *Image) Channels() int { return im.channels }

// Dirty reports whether the pixel data has been modified since the file was
// opened. Clean blocks are written back byte for byte.
func (im *Image) Dirty() bool { return im.dirty }

// SampleCount is the total number of samples in the image.
func (im *Image) SampleCount() int {
	n := im.channels
	for _, d := range im.dims {
		n *= d
	}
	return n
}

func (im *Image) planeSize() int {
	n := 1
	for _, d := range im.dims {
		n *= d
	}
	return n
}

func (im *Image) require2D() error {
	if len(im.dims) != 2 {
		return errf(ErrNot2D, "image has %d dimensions", len(im.dims))
	}
	if im.importErr != nil {
		return im.importErr
	}
	return nil
}

// Pixels returns the image's samples as a typed slice in planar order.
// The slice aliases the image's storage: changes are visible, but call
// SetPixels (or touch Dirty) so the file knows to re-encode the block.
func Pixels[T Sample](im *Image) ([]T, error) {
	if err := im.require2D(); err != nil {
		return nil, err
	}
	p, ok := im.data.([]T)
	if !ok {
		var zero T
		return nil, errf(ErrFormat, "image is %s, not %T", im.SampleFormat, zero)
	}
	return p, nil
}

// Plane returns one channel's samples in row-major order.
func Plane[T Sample](im *Image, c int) ([]T, error) { return planeOf[T](im, c) }

func planeOf[T Sample](im *Image, c int) ([]T, error) {
	p, err := Pixels[T](im)
	if err != nil {
		return nil, err
	}
	if c < 0 || c >= im.channels {
		return nil, errf(ErrBounds, "channel %d out of range [0,%d)", c, im.channels)
	}
	n := im.planeSize()
	return p[c*n : (c+1)*n], nil
}

// Plane returns one channel's samples in row-major order.
func (im *Image) Plane(c int) (any, error) {
	if err := im.require2D(); err != nil {
		return nil, err
	}
	if c < 0 || c >= im.channels {
		return nil, errf(ErrBounds, "channel %d out of range [0,%d)", c, im.channels)
	}
	switch d := im.data.(type) {
	case []uint8:
		return d[c*im.planeSize() : (c+1)*im.planeSize()], nil
	case []uint16:
		return d[c*im.planeSize() : (c+1)*im.planeSize()], nil
	case []uint32:
		return d[c*im.planeSize() : (c+1)*im.planeSize()], nil
	case []uint64:
		return d[c*im.planeSize() : (c+1)*im.planeSize()], nil
	case []float32:
		return d[c*im.planeSize() : (c+1)*im.planeSize()], nil
	case []float64:
		return d[c*im.planeSize() : (c+1)*im.planeSize()], nil
	case []complex64:
		return d[c*im.planeSize() : (c+1)*im.planeSize()], nil
	case []complex128:
		return d[c*im.planeSize() : (c+1)*im.planeSize()], nil
	}
	return nil, errf(ErrFormat, "image has no pixel data")
}

// SetPixels replaces the samples. The slice must match the image's geometry
// and sample format, and is used directly rather than copied.
func SetPixels[T Sample](im *Image, p []T) error {
	if len(im.dims) != 2 {
		return errf(ErrNot2D, "image has %d dimensions", len(im.dims))
	}
	if len(p) != im.SampleCount() {
		return errf(ErrFormat, "got %d samples, image has %d", len(p), im.SampleCount())
	}
	var zero T
	want := formatOf(zero)
	if want != im.SampleFormat {
		return errf(ErrFormat, "got %T samples, image is %s", zero, im.SampleFormat)
	}
	im.data = p
	im.dirty = true
	return nil
}

func formatOf[T Sample](zero T) SampleFormat {
	switch any(zero).(type) {
	case uint8:
		return UInt8
	case uint16:
		return UInt16
	case uint32:
		return UInt32
	case uint64:
		return UInt64
	case float32:
		return Float32
	case float64:
		return Float64
	case complex64:
		return Complex32
	case complex128:
		return Complex64
	}
	return -1
}

// formatOfData reports the sample format implied by an image's storage type.
func formatOfData(v any) SampleFormat {
	switch v.(type) {
	case []uint8:
		return UInt8
	case []uint16:
		return UInt16
	case []uint32:
		return UInt32
	case []uint64:
		return UInt64
	case []float32:
		return Float32
	case []float64:
		return Float64
	case []complex64:
		return Complex32
	case []complex128:
		return Complex64
	}
	return -1
}

func dataLen(v any) int {
	switch d := v.(type) {
	case []uint8:
		return len(d)
	case []uint16:
		return len(d)
	case []uint32:
		return len(d)
	case []uint64:
		return len(d)
	case []float32:
		return len(d)
	case []float64:
		return len(d)
	case []complex64:
		return len(d)
	case []complex128:
		return len(d)
	}
	return -1
}

// Sample is the set of Go types a pixel sample can have. Named types are not
// accepted: the pixel slices returned by Pixels are the image's own storage,
// so the element type must match the image's format exactly.
type Sample interface {
	uint8 | uint16 | uint32 | uint64 |
		float32 | float64 | complex64 | complex128
}

func (im *Image) index(x, y, c int) int {
	return (c*im.Height()+y)*im.Width() + x
}

func (im *Image) checkCoord(x, y, c int) {
	if x < 0 || x >= im.Width() || y < 0 || y >= im.Height() || c < 0 || c >= im.channels {
		panic(fmt.Sprintf("xisf: coordinate (%d,%d,%d) outside %dx%dx%d", x, y, c, im.Width(), im.Height(), im.channels))
	}
}

// At returns one real sample. It panics if the coordinate is outside the
// image or the image is complex. Use Pixels for typed, checked access.
func (im *Image) At(x, y, c int) float64 {
	if err := im.require2D(); err != nil {
		panic(err)
	}
	im.checkCoord(x, y, c)
	i := im.index(x, y, c)
	switch d := im.data.(type) {
	case []uint8:
		return float64(d[i])
	case []uint16:
		return float64(d[i])
	case []uint32:
		return float64(d[i])
	case []uint64:
		return float64(d[i])
	case []float32:
		return float64(d[i])
	case []float64:
		return d[i]
	default:
		panic("xisf: At is not defined for complex pixel data")
	}
}

// Set stores one real sample, converting and clamping it to the image's
// format. It panics on an invalid coordinate.
func (im *Image) Set(x, y, c int, v float64) {
	if err := im.require2D(); err != nil {
		panic(err)
	}
	im.checkCoord(x, y, c)
	i := im.index(x, y, c)
	switch d := im.data.(type) {
	case []uint8:
		d[i] = uint8(clampUint(v, math.MaxUint8))
	case []uint16:
		d[i] = uint16(clampUint(v, math.MaxUint16))
	case []uint32:
		d[i] = uint32(clampUint(v, math.MaxUint32))
	case []uint64:
		d[i] = clampUint(v, math.MaxUint64)
	case []float32:
		d[i] = float32(v)
	case []float64:
		d[i] = v
	default:
		panic("xisf: Set is not defined for complex pixel data")
	}
	im.dirty = true
}

func clampUint(v float64, max uint64) uint64 {
	if v <= 0 || math.IsNaN(v) {
		return 0
	}
	limit := float64(max)
	if v >= limit {
		return max
	}
	r := math.Round(v)
	if r >= limit {
		return max
	}
	return uint64(r)
}

// AtComplex returns one complex sample.
func (im *Image) AtComplex(x, y, c int) complex128 {
	if err := im.require2D(); err != nil {
		panic(err)
	}
	im.checkCoord(x, y, c)
	i := im.index(x, y, c)
	switch d := im.data.(type) {
	case []complex64:
		return complex128(d[i])
	case []complex128:
		return d[i]
	default:
		panic("xisf: AtComplex is only defined for complex pixel data")
	}
}

// SetComplex stores one complex sample.
func (im *Image) SetComplex(x, y, c int, v complex128) {
	if err := im.require2D(); err != nil {
		panic(err)
	}
	im.checkCoord(x, y, c)
	i := im.index(x, y, c)
	switch d := im.data.(type) {
	case []complex64:
		d[i] = complex64(v)
	case []complex128:
		d[i] = v
	default:
		panic("xisf: SetComplex is only defined for complex pixel data")
	}
	im.dirty = true
}

// Float64 returns a float64 copy of the samples. Complex images are refused.
func (im *Image) Float64() ([]float64, error) {
	if err := im.require2D(); err != nil {
		return nil, err
	}
	n := im.SampleCount()
	out := make([]float64, n)
	switch d := im.data.(type) {
	case []uint8:
		for i, v := range d {
			out[i] = float64(v)
		}
	case []uint16:
		for i, v := range d {
			out[i] = float64(v)
		}
	case []uint32:
		for i, v := range d {
			out[i] = float64(v)
		}
	case []uint64:
		for i, v := range d {
			out[i] = float64(v)
		}
	case []float32:
		for i, v := range d {
			out[i] = float64(v)
		}
	case []float64:
		copy(out, d)
	default:
		return nil, errf(ErrUnsupported, "%s is a complex format", im.SampleFormat)
	}
	return out, nil
}

// SetFloat64 replaces the samples with values converted to the image's
// format. Complex images are refused.
func (im *Image) SetFloat64(v []float64) error {
	if err := im.require2D(); err != nil {
		return err
	}
	if len(v) != im.SampleCount() {
		return errf(ErrFormat, "got %d samples, image has %d", len(v), im.SampleCount())
	}
	switch im.SampleFormat {
	case UInt8, UInt16, UInt32, UInt64:
		// Reuse the existing typed storage when possible.
		switch d := im.data.(type) {
		case []uint8:
			for i, x := range v {
				d[i] = uint8(clampUint(x, math.MaxUint8))
			}
		case []uint16:
			for i, x := range v {
				d[i] = uint16(clampUint(x, math.MaxUint16))
			}
		case []uint32:
			for i, x := range v {
				d[i] = uint32(clampUint(x, math.MaxUint32))
			}
		case []uint64:
			for i, x := range v {
				d[i] = clampUint(x, math.MaxUint64)
			}
		default:
			return errf(ErrFormat, "pixel storage does not match %s", im.SampleFormat)
		}
		im.dirty = true
		return nil
	case Float32:
		d := make([]float32, len(v))
		for i, x := range v {
			d[i] = float32(x)
		}
		im.data = d
		im.dirty = true
		return nil
	case Float64:
		im.data = append([]float64(nil), v...)
		im.dirty = true
		return nil
	}
	return errf(ErrUnsupported, "%s is a complex format", im.SampleFormat)
}

// RepresentableRange is the image's declared black and white points, or the
// default [0, 2^k-1] for integer images that declare none.
func (im *Image) RepresentableRange() (low, high float64, ok bool) {
	if im.HasBounds {
		return im.Bounds[0], im.Bounds[1], true
	}
	switch im.SampleFormat {
	case UInt8:
		return 0, 255, true
	case UInt16:
		return 0, 65535, true
	case UInt32:
		return 0, math.MaxUint32, true
	case UInt64:
		return 0, math.MaxUint64, true
	}
	return 0, 0, false
}

// --- pixel conversion ------------------------------------------------------

func (im *Image) decodePixels(raw []byte) error {
	if len(im.dims) != 2 {
		// Keep the image available and preserved, but do not pretend to
		// understand its sample layout.
		im.importErr = errf(ErrNot2D, "image has %d dimensions", len(im.dims))
		return nil
	}
	size := im.SampleFormat.Size()
	count := im.SampleCount()
	if size == 0 {
		return errf(ErrUnsupported, "unknown sample format")
	}
	if len(raw) != count*size {
		return errf(ErrCorrupt, "pixel block is %d bytes, %dx%d x %d channels of %s needs %d",
			len(raw), im.Width(), im.Height(), im.channels, im.SampleFormat, count*size)
	}
	if im.byteOrder == BigEndian && im.SampleFormat.UnitSize() > 1 {
		raw = append([]byte(nil), raw...)
		swapUnits(raw, im.SampleFormat.UnitSize())
	}
	var planar any
	switch im.SampleFormat {
	case UInt8:
		planar = decodeTyped[uint8](raw)
	case UInt16:
		planar = decodeTyped[uint16](raw)
	case UInt32:
		planar = decodeTyped[uint32](raw)
	case UInt64:
		planar = decodeTyped[uint64](raw)
	case Float32:
		planar = decodeTyped[float32](raw)
	case Float64:
		planar = decodeTyped[float64](raw)
	case Complex32:
		planar = decodeTyped[complex64](raw)
	case Complex64:
		planar = decodeTyped[complex128](raw)
	}
	if im.PixelStorage == Normal && im.channels > 1 {
		planar = deinterleave(planar, im.channels, im.planeSize())
	}
	im.data = planar
	return nil
}

func decodeTyped[T any](raw []byte) []T {
	n := len(raw) / int(unsafe.Sizeof(*new(T)))
	out := make([]T, n)
	if n == 0 {
		return out
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(&out[0])), n*int(unsafe.Sizeof(out[0])))
	copy(dst, raw)
	return out
}

func deinterleave(src any, channels, planeSize int) any {
	switch s := src.(type) {
	case []uint8:
		out := make([]uint8, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[c*planeSize+i] = s[i*channels+c]
			}
		}
		return out
	case []uint16:
		out := make([]uint16, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[c*planeSize+i] = s[i*channels+c]
			}
		}
		return out
	case []uint32:
		out := make([]uint32, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[c*planeSize+i] = s[i*channels+c]
			}
		}
		return out
	case []uint64:
		out := make([]uint64, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[c*planeSize+i] = s[i*channels+c]
			}
		}
		return out
	case []float32:
		out := make([]float32, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[c*planeSize+i] = s[i*channels+c]
			}
		}
		return out
	case []float64:
		out := make([]float64, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[c*planeSize+i] = s[i*channels+c]
			}
		}
		return out
	case []complex64:
		out := make([]complex64, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[c*planeSize+i] = s[i*channels+c]
			}
		}
		return out
	case []complex128:
		out := make([]complex128, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[c*planeSize+i] = s[i*channels+c]
			}
		}
		return out
	}
	return src
}

func interleave(src any, channels, planeSize int) any {
	switch s := src.(type) {
	case []uint8:
		out := make([]uint8, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[i*channels+c] = s[c*planeSize+i]
			}
		}
		return out
	case []uint16:
		out := make([]uint16, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[i*channels+c] = s[c*planeSize+i]
			}
		}
		return out
	case []uint32:
		out := make([]uint32, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[i*channels+c] = s[c*planeSize+i]
			}
		}
		return out
	case []uint64:
		out := make([]uint64, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[i*channels+c] = s[c*planeSize+i]
			}
		}
		return out
	case []float32:
		out := make([]float32, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[i*channels+c] = s[c*planeSize+i]
			}
		}
		return out
	case []float64:
		out := make([]float64, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[i*channels+c] = s[c*planeSize+i]
			}
		}
		return out
	case []complex64:
		out := make([]complex64, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[i*channels+c] = s[c*planeSize+i]
			}
		}
		return out
	case []complex128:
		out := make([]complex128, len(s))
		for c := 0; c < channels; c++ {
			for i := 0; i < planeSize; i++ {
				out[i*channels+c] = s[c*planeSize+i]
			}
		}
		return out
	}
	return src
}

// bytesNative serialises the samples in the image's original storage model
// and byte order.
func (im *Image) bytesNative() ([]byte, error) {
	if err := im.require2D(); err != nil {
		return nil, err
	}
	if got := formatOfData(im.data); got != im.SampleFormat {
		return nil, errf(ErrFormat, "pixel storage is %s but sampleFormat is %s", got, im.SampleFormat)
	}
	if got := dataLen(im.data); got != im.SampleCount() {
		return nil, errf(ErrFormat, "pixel storage holds %d samples, geometry needs %d", got, im.SampleCount())
	}
	src := im.data
	if im.PixelStorage == Normal && im.channels > 1 {
		src = interleave(src, im.channels, im.planeSize())
	}
	n := im.SampleCount() * im.SampleFormat.Size()
	view := viewBytes(src, n)
	out := append([]byte(nil), view...)
	if im.byteOrder == BigEndian && im.SampleFormat.UnitSize() > 1 {
		swapUnits(out, im.SampleFormat.UnitSize())
	}
	return out, nil
}

func viewBytes(src any, n int) []byte {
	switch s := src.(type) {
	case []uint8:
		return s
	case []uint16:
		if len(s) == 0 {
			return nil
		}
		return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), n)
	case []uint32:
		if len(s) == 0 {
			return nil
		}
		return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), n)
	case []uint64:
		if len(s) == 0 {
			return nil
		}
		return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), n)
	case []float32:
		if len(s) == 0 {
			return nil
		}
		return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), n)
	case []float64:
		if len(s) == 0 {
			return nil
		}
		return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), n)
	case []complex64:
		if len(s) == 0 {
			return nil
		}
		return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), n)
	case []complex128:
		if len(s) == 0 {
			return nil
		}
		return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), n)
	}
	return nil
}

func swapUnits(b []byte, unit int) {
	if unit < 2 {
		return
	}
	for i := 0; i+unit <= len(b); i += unit {
		for l, r := i, i+unit-1; l < r; l, r = l+1, r-1 {
			b[l], b[r] = b[r], b[l]
		}
	}
}

// parseGeometry parses d1:d2:...:channels.
func parseGeometry(s string) ([]int, int, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return nil, 0, errf(ErrHeader, "geometry %q needs at least one dimension and a channel count", s)
	}
	vals := make([]int, len(parts))
	for i, p := range parts {
		v, err := strconv.ParseUint(strings.TrimSpace(p), 10, 31)
		if err != nil || v == 0 {
			return nil, 0, errf(ErrHeader, "invalid geometry component %q", p)
		}
		vals[i] = int(v)
	}
	channels := vals[len(vals)-1]
	return vals[:len(vals)-1], channels, nil
}

func formatGeometry(dims []int, channels int) string {
	parts := make([]string, 0, len(dims)+1)
	for _, d := range dims {
		parts = append(parts, strconv.Itoa(d))
	}
	parts = append(parts, strconv.Itoa(channels))
	return strings.Join(parts, ":")
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}
