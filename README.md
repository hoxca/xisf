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

## Metadata

`f.Metadata()` and `f.Properties()` expose `Property` values read-only;
the underlying XML is preserved on write. `f.Property("XISF:...")` looks one
up. `WriteOptions.UpdateCreationTime` and `.CreatorApplication` update those
two metadata properties.

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
* Creating files from scratch: there is no constructor for a new `File`.
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
