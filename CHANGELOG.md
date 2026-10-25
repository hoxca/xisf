# Changelog

All notable changes to this project will be documented here.
The format is based on Keep a Changelog, and this project adheres
to Semantic Versioning.

## [Unreleased]

### Added

- `File.AddProperty` and `File.SetProperty` for adding or updating unit
  metadata properties before writing.
- `Image.Properties`, `Image.Property`, `Image.AddProperty`, and
  `Image.SetProperty` for image-level metadata.

## [0.0.1] - 2026-10-22

First public release.

### Added

- Read, edit, and write monolithic XISF 1.0 units.
- Lossless round-trips: untouched files are byte-identical; unknown XML
  elements, attribute order, comments, and namespaces are preserved.
- Typed, zero-copy pixel access via `Pixels[T]`, `Plane[T]`, `SetPixels`,
  plus `At`/`Set` and `Float64`/`SetFloat64` scalar helpers.
- All 2D sample formats (`UInt8` through `UInt64`, `Float32`/`Float64`,
  `Complex32`/`Complex64`), `Gray`, `RGB`, and `CIELab`, planar and normal
  storage, little- and big-endian.
- `zlib`, `lz4`, `lz4hc`, `zstd`, byte shuffling, subblocks, and
  SHA-1/SHA-256/SHA-512 checksums.
- Attachment, embedded, and inline blocks; per-block compression overrides
  through `WriteOptions`.
- Metadata and property access with atomic `WriteFile`.

### Known limitations

- Distributed units (`.xish`/`.xisb`, `path(...)`, `url(...)`) are rejected
  with `ErrUnsupported`.
- No API for creating a `File` from scratch.
- N-dimensional images are preserved but not editable.
- ICC profiles, FITS keywords, and ancillary blocks are read-only.

[Unreleased]: https://github.com/hoxca/xisf/commits/main
[0.0.1]: https://github.com/hoxca/xisf/releases/tag/v0.0.1
