package xisf

import (
	"fmt"
	"strconv"
	"time"
)

// defaultAlignment is the data block alignment used for newly created files.
const defaultAlignment = 4096

// ImageSpec describes a new two-dimensional XISF image.
//
// Width and Height must be positive. Channels must be 1 or 3. For one channel
// the colour space is Gray; for three channels it defaults to RGB, and may be
// set explicitly to CIELab.
type ImageSpec struct {
	// Width and Height are the image dimensions in pixels.
	Width, Height int
	// Channels is the number of samples per pixel: 1 for Gray, 3 for RGB or
	// CIELab.
	Channels int
	// ColorSpace is the colour model. The zero value is inferred from Channels.
	ColorSpace ColorSpace
	// PixelStorage is how channels are stored in the file. The zero value is
	// Planar.
	PixelStorage PixelStorage
	// ID is the image's optional id attribute.
	ID string
	// ImageType is the image's optional free-form classification.
	ImageType string
	// Bounds is the declared representable sample range. It is used when
	// HasBounds is set. Floating-point and complex images default to 0:1.
	Bounds [2]float64
	// HasBounds reports whether Bounds should be written for an integer image.
	HasBounds bool
}

// NewImage creates a monolithic XISF unit containing one image. The pixels are
// in planar order: the first Width*Height samples are the first channel, then
// the second, and so on. The slice is used directly rather than copied.
//
// The returned File is fully writable. Add image properties through the
// image's AddProperty or SetProperty methods, and compression or checksums
// through WriteOptions when calling Bytes or WriteFile. The image block is
// aligned to XISF:BlockAlignmentSize, which NewImage declares as 4096.
func NewImage[T Sample](spec ImageSpec, pixels []T) (*File, error) {
	if spec.Width <= 0 || spec.Height <= 0 {
		return nil, errf(ErrGeometry, "image is %d×%d, want positive dimensions", spec.Width, spec.Height)
	}
	if spec.Channels != 1 && spec.Channels != 3 {
		return nil, errf(ErrGeometry, "channels = %d, want 1 or 3", spec.Channels)
	}
	if spec.PixelStorage != Planar && spec.PixelStorage != Normal {
		return nil, errf(ErrGeometry, "unknown pixel storage %d", spec.PixelStorage)
	}

	channels := spec.Channels
	colorSpace := spec.ColorSpace
	switch channels {
	case 1:
		switch colorSpace {
		case Gray:
		case RGB, CIELab:
			return nil, errf(ErrGeometry, "%s requires three channels", colorSpace)
		default:
			return nil, errf(ErrGeometry, "unknown colour space %d", colorSpace)
		}
	case 3:
		switch colorSpace {
		case Gray:
			colorSpace = RGB
		case RGB, CIELab:
		default:
			return nil, errf(ErrGeometry, "unknown colour space %d", colorSpace)
		}
	}

	maxInt := int(^uint(0) >> 1)
	if spec.Width > maxInt/spec.Height {
		return nil, errf(ErrGeometry, "%d×%d image is too large", spec.Width, spec.Height)
	}
	planeSize := spec.Width * spec.Height
	if planeSize > maxInt/channels {
		return nil, errf(ErrGeometry, "%d×%d×%d image is too large", spec.Width, spec.Height, channels)
	}
	count := planeSize * channels
	if len(pixels) != count {
		return nil, errf(ErrGeometry, "got %d samples, want %d for %d×%d×%d",
			len(pixels), count, spec.Width, spec.Height, channels)
	}

	var zero T
	format := formatOf(zero)
	if format < 0 {
		return nil, errf(ErrUnsupported, "unsupported sample type %T", zero)
	}
	if format.Size() > 0 && count > maxInt/format.Size() {
		return nil, errf(ErrGeometry, "%d samples of %s are too large", count, format)
	}

	hasBounds := spec.HasBounds
	bounds := spec.Bounds
	if !hasBounds && format.IsFloat() {
		hasBounds = true
		bounds = [2]float64{0, 1}
	}

	header := []byte(fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>` +
			`<xisf version="1.0" xmlns="http://www.pixinsight.com/xisf">` +
			`<Image location="attachment:0:0"/>` +
			`<Metadata></Metadata>` +
			`</xisf>`))
	doc, err := parseXML(header)
	if err != nil {
		return nil, err
	}
	var imageNode *xmlNode
	for _, n := range doc.root.children() {
		if n.localName() == "Image" {
			imageNode = n
			break
		}
	}
	if imageNode == nil {
		return nil, errf(ErrHeader, "new image header has no <Image> element")
	}

	f := &File{
		doc:       doc,
		src:       header,
		version:   "1.0",
		alignment: defaultAlignment,
	}
	im := &Image{
		ID:           spec.ID,
		ImageType:    spec.ImageType,
		SampleFormat: format,
		ColorSpace:   colorSpace,
		PixelStorage: spec.PixelStorage,
		Bounds:       bounds,
		HasBounds:    hasBounds,
		dims:         []int{spec.Width, spec.Height},
		channels:     channels,
		data:         pixels,
		dirty:        true,
		byteOrder:    LittleEndian,
		elem:         imageNode,
		src:          header,
	}
	block := &dataBlock{
		elem:      imageNode,
		loc:       Location{Kind: LocAttachment},
		encoding:  EncBase64,
		byteOrder: LittleEndian,
	}
	im.block = block
	f.images = append(f.images, im)
	f.allImages = append(f.allImages, im)
	f.blocks = append(f.blocks, block)

	im.writeAttributes()

	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if err := f.SetProperty(Property{
		ID:    "XISF:CreationTime",
		Type:  "String",
		Value: now,
	}); err != nil {
		return nil, err
	}
	if err := f.SetProperty(Property{
		ID:    "XISF:CreatorApplication",
		Type:  "String",
		Value: "xisf",
	}); err != nil {
		return nil, err
	}
	// Declare the alignment that Bytes applies to attached blocks, so readers
	// can memory-map or seek into them (PixInsight's incremental reads).
	if err := f.SetProperty(Property{
		ID:    "XISF:BlockAlignmentSize",
		Type:  "UInt16",
		Value: strconv.Itoa(defaultAlignment),
	}); err != nil {
		return nil, err
	}

	return f, nil
}
