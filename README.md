# xisf

[![Go Reference](https://pkg.go.dev/badge/github.com/hoxca/xisf.svg)](https://pkg.go.dev/github.com/hoxca/xisf)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A Go library for reading and writing monolithic [XISF](https://pixinsight.com/doc/docs/XISF-1.0-spec/XISF-1.0-spec.html)
(PixInsight Extensible Image Serialization Format) images.

The design goal is *editing without collateral damage*. A file is parsed
losslessly: the XML header is kept as a byte-span DOM, and only the attributes
and elements that actually change are re-rendered. Image blocks whose pixels
were not touched are copied back with their original bytes, and edited blocks
keep their original codec, shuffle item size, subblock layout, byte order and
checksum algorithm.

## Status

v0.0.1 — pre-1.0. The API may change between minor versions until v1.0.0.
Only monolithic XISF units are supported; see the limitations below.

## Install

```
go get github.com/hoxca/xisf
```

## Quick start

```go
f, err := xisf.Open("light.xisf")
if err != nil {
    log.Fatal(err)
}
im := f.Images()[0]

// Typed, zero-copy access. The slice aliases the image's storage.
px, err := xisf.Pixels[float32](im)
if err != nil {
    log.Fatal(err)
}
for i := range px {
    px[i] *= 2
}
im.MarkDirty() // Pixels returns the live slice; tell the file it changed

if err := f.WriteFile("light.xisf", nil); err != nil {
    log.Fatal(err)
}
```

`At`/`Set` are available for scalar access and `Float64`/`SetFloat64` for a
float64 copy. `SetPixels` installs a slice you own.

## Creating files

`NewImage` builds a new monolithic unit around an existing pixel slice. The
slice is used directly and must be in planar order: all samples of channel 0,
then channel 1, and so on.

```go
rgb := make([]float32, 3*w*h)
// fill rgb

f, err := xisf.NewImage(xisf.ImageSpec{
    Width:        w,
    Height:       h,
    Channels:     3,
    PixelStorage: xisf.Normal, // interleaved in the file
    ID:           "SHO",
    ImageType:    "MasterLight",
}, rgb)
if err != nil {
    log.Fatal(err)
}

im := f.Images()[0]
err = im.AddProperty(xisf.Property{
    ID:    "PCL:OverridableIdentifier",
    Type:  "Boolean",
    Value: "false",
})
if err != nil {
    log.Fatal(err)
}

err = f.WriteFile("out.xisf", &xisf.WriteOptions{
    Compression: &xisf.Compression{Codec: xisf.CodecZlib},
})
if err != nil {
    log.Fatal(err)
}
```

The sample format is inferred from the slice type. One channel implies
`Gray`; three channels imply `RGB`, or `CIELab` when `ColorSpace` is set.
Floating-point and complex images default to `bounds="0:1"`, as XISF requires.
Integer images omit bounds unless `HasBounds` is set. `NewImage` writes
`XISF:CreationTime`, `XISF:CreatorApplication` and `XISF:BlockAlignmentSize`
metadata; the creator application can be overridden through `WriteOptions`.
Blocks are aligned to 4096 bytes, which the declared property makes visible to
readers.

## What is preserved

* **Untouched files are byte-identical.** `Bytes(nil)` on a freshly opened
  file returns the original bytes when nothing has been modified.
* **The header is a DOM over the original bytes.** Unknown elements, attribute
  order, comments, namespaces and the exact escaping of large properties are
  kept verbatim. Modified attributes are re-escaped; everything else is copied.
* **Compression is preserved per block.** A dirty image block is re-encoded
  with the codec, `+sh` flag, item size, subblock table, byte order and
  checksum algorithm it had. Clean blocks are copied verbatim.
* **Layout is reproducible.** Data blocks are re-laid-out on the
  `XISF:BlockAlignmentSize` boundary (4096 in PixInsight files). The header
  length is solved to a fixed point because `attachment:position:size`
  literals change the header size.

## Overriding compression

`WriteOptions.Compression` and `.Checksum` apply to *dirty* image blocks. Use
them to convert a file to another codec:

```go
err := f.WriteFile("out.xisf", &xisf.WriteOptions{
    Compression: &xisf.Compression{
        Codec:      xisf.CodecZstd,
        Shuffled:   true,
        ItemSize:   4, // Float32
    },
    CompressionLevel: 75,
})
if err != nil {
    log.Fatal(err)
}
```

`Compression.UncompressedSize` is filled in from the image geometry. Codecs
are `zlib`, `lz4`, `lz4hc` and `zstd`, each optionally byte-shuffled.

### Writing uncompressed blocks

`WriteOptions.Uncompressed` stores image blocks raw. It also converts blocks
that were already compressed, even when no pixel was edited, so it works on a
file you have only just opened:

```go
err := f.WriteFile("out.xisf", &xisf.WriteOptions{Uncompressed: true})
```

This matters for PixInsight, which only performs incremental file reads on
uncompressed XISF: processes such as ImageIntegration reject compressed input
with *"Incremental file reads have been enabled, but they cannot be performed
on this file"*. The option takes precedence over `Compression`. A file that
declares no `XISF:BlockAlignmentSize` gets the 4096-byte alignment PixInsight
uses for its own uncompressed output. `XISF:OutputHints` is left alone, since
that property is advisory and a decoder must not rely on it.

## Metadata

`f.Metadata()` and `f.Properties()` expose `Property` values; untouched
properties keep their underlying XML on write. `f.Property("XISF:...")` looks
one up. `f.AddProperty` appends a unit property, and `f.SetProperty` updates an
existing one or appends it:

```go
err := f.SetProperty(xisf.Property{
    ID:    "PCL:OverridableIdentifier",
    Type:  "Boolean",
    Value: "false",
})
```

Scalar, complex and time point values are written as `value` attributes;
`String` values are written as element text. Properties stored in data blocks
are not supported. `WriteOptions.UpdateCreationTime` and `.CreatorApplication`
update those two metadata properties.

Image-level properties use the same API on `Image`:

```go
im := f.Images()[0]
err := im.AddProperty(xisf.Property{
    ID:    "PCL:OverridableIdentifier",
    Type:  "Boolean",
    Value: "false",
})
```

`im.Properties()` and `im.Property(id)` return only the properties directly
under that image.

## Supported

* Monolithic `.xisf` units: 8-byte signature, 32-bit header length, XML header,
  attached blocks.
* 2D images: `geometry="width:height:channels"`, `Gray`, `RGB` and `CIELab`.
* `UInt8`, `UInt16`, `UInt32`, `UInt64`, `Float32`, `Float64`, `Complex32`,
  `Complex64`, in `Planar` or `Normal` storage, little- or big-endian.
* `zlib`, `lz4`, `lz4hc`, `zstd`, with and without byte shuffling, subblocks,
  and `sha-1`/`sha-256`/`sha-512` checksums.
* `attachment`, `embedded` and `inline` data block locations. Attachment blocks
  are read without copying; pixels are always returned in planar order.

## Not supported (yet)

* Distributed units: `.xish` headers, `.xisb` block files, and `path(...)` /
  `url(...)` locations are rejected with `ErrUnsupported`.
* N-dimensional images: they are preserved but not editable.
* Editing ancillary blocks: thumbnails are editable through
  `Image.Thumbnail`; ICC profiles and FITS keywords are exposed read-only and
  preserved verbatim.
* Unknown codecs: a block using a codec or checksum this package does not
  implement is preserved byte for byte; only that image's pixels are
  unavailable (`Pixels` returns `ErrUnsupported`).

## Testing

Synthetic fixtures cover every sample format and codec. Tests against real
PixInsight files skip cleanly when the samples are absent. Put them in `img/`
(ignored by git) or point `TestLargeRGBFile` at a file with `XISF_LARGE_FILE`.

Codec compatibility is checked against compressed samples written by
PixInsight itself: `TestCompressedImagesMatchReference` decodes every
compressed file in `img/` and compares its pixels bit for bit with the
uncompressed master it was derived from, which validates `zlib`, `lz4`,
`lz4hc` and `zstd` byte shuffling and pixel ordering against the reference
encoder rather than against this package's own output.

```
make test-verbose
```

The `img/` directory is intentionally not committed: those are the author's
raw subframes. `img/test_files.txt` is the xisf files list needed for testing.

## License

MIT. See [LICENSE](LICENSE).
