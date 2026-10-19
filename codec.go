package xisf

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"io"
	"math"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

// --- byte shuffling --------------------------------------------------------

// shuffle rearranges the bytes of fixed-size items so that all the first
// bytes come first, then all the second bytes, and so on. Trailing bytes that
// do not form a whole item are stored unaltered at the end, as the
// specification's reference implementation does.
func shuffle(src []byte, item int) []byte {
	if item < 2 || len(src) < item {
		return append([]byte(nil), src...)
	}
	count := len(src) / item
	out := make([]byte, len(src))
	for b := 0; b < item; b++ {
		for i := 0; i < count; i++ {
			out[b*count+i] = src[i*item+b]
		}
	}
	copy(out[count*item:], src[count*item:])
	return out
}

func unshuffle(src []byte, item int) []byte {
	if item < 2 || len(src) < item {
		return append([]byte(nil), src...)
	}
	count := len(src) / item
	out := make([]byte, len(src))
	for b := 0; b < item; b++ {
		plane := src[b*count : (b+1)*count]
		for i, v := range plane {
			out[i*item+b] = v
		}
	}
	copy(out[count*item:], src[count*item:])
	return out
}

// --- decompression ---------------------------------------------------------

func maxExpansionRatio(c Codec) int64 {
	switch c {
	case CodecZlib:
		return 2048
	case CodecLZ4, CodecLZ4HC:
		return 512
	case CodecZstd:
		return 65536
	}
	return 65536
}

// decodeCompression reverses compression, subblocks and byte shuffling.
func decodeCompression(stored []byte, c *Compression) ([]byte, error) {
	if c == nil {
		return stored, nil
	}
	size := c.UncompressedSize
	if size < 0 || uint64(size) > uint64(^uint(0)>>1) {
		return nil, errf(ErrCorrupt, "negative or oversized uncompressed size")
	}
	// The size comes from the header, not from the data. Bound it before
	// allocating so a tiny hostile block cannot ask for terabytes.
	ceiling := int64(len(stored))*maxExpansionRatio(c.Codec) + 1024
	if size > ceiling {
		return nil, errf(ErrCorrupt, "a %d-byte block claims %d uncompressed bytes, beyond any ratio %s can achieve",
			len(stored), size, c.Codec)
	}

	var plain []byte
	if len(c.Subblocks) == 0 {
		// A block without a subblocks attribute is one subblock, and the
		// uncompressed-subblock rule applies to it too: equal lengths may
		// mean stored raw, or data compressed by an earlier revision.
		out, err := decodeSubblock(stored, c.Codec, int(size))
		if err != nil {
			return nil, errf(ErrCorrupt, "%v", err)
		}
		plain = out
	} else {
		var compressedTotal, uncompressedTotal int64
		for _, s := range c.Subblocks {
			compressedTotal += s.Compressed
			uncompressedTotal += s.Uncompressed
		}
		if compressedTotal != int64(len(stored)) {
			return nil, errf(ErrCorrupt, "subblocks account for %d bytes, block holds %d", compressedTotal, len(stored))
		}
		if uncompressedTotal != size {
			return nil, errf(ErrCorrupt, "subblocks decompress to %d bytes, block declares %d", uncompressedTotal, size)
		}
		if size > 0 {
			plain = make([]byte, 0, int(size))
		}
		rest := stored
		for i, s := range c.Subblocks {
			ci, ui := int(s.Compressed), int(s.Uncompressed)
			if ci < 0 || ci > len(rest) || ui < 0 {
				return nil, errf(ErrCorrupt, "subblock %d has invalid lengths", i)
			}
			chunk := rest[:ci]
			rest = rest[ci:]
			out, err := decodeSubblock(chunk, c.Codec, ui)
			if err != nil {
				return nil, errf(ErrCorrupt, "subblock %d: %v", i, err)
			}
			if len(out) != ui {
				return nil, errf(ErrCorrupt, "subblock %d declares %d bytes but produced %d", i, ui, len(out))
			}
			plain = append(plain, out...)
		}
	}
	if int64(len(plain)) != size {
		return nil, errf(ErrCorrupt, "block declares %d uncompressed bytes but produced %d", size, len(plain))
	}
	if c.Shuffled && c.ItemSize > 0 {
		plain = unshuffle(plain, c.ItemSize)
	}
	return plain, nil
}

// decodeSubblock decodes one compression subblock. A subblock whose stored
// length equals its uncompressed length is normally stored raw; an encoder
// implementing an earlier revision may also have compressed it, so the
// decoder tries the codec first and falls back to the stored bytes.
func decodeSubblock(chunk []byte, codec Codec, ui int) ([]byte, error) {
	if len(chunk) == ui {
		if dec, err := decodeChunk(chunk, codec, ui); err == nil && len(dec) == ui {
			return dec, nil
		}
		return append([]byte(nil), chunk...), nil
	}
	dec, err := decodeChunk(chunk, codec, ui)
	if err != nil {
		return nil, err
	}
	return dec, nil
}

func decodeChunk(chunk []byte, codec Codec, size int) ([]byte, error) {
	if size == 0 {
		return nil, nil
	}
	switch codec {
	case CodecZlib:
		r, err := zlib.NewReader(bytes.NewReader(chunk))
		if err != nil {
			return nil, err
		}
		defer func() { _ = r.Close() }()
		out, err := io.ReadAll(io.LimitReader(r, int64(size)+1))
		if err != nil {
			return nil, err
		}
		return out, nil
	case CodecLZ4, CodecLZ4HC:
		// LZ4HC is an encoder setting; the stream format is LZ4 block format.
		out := make([]byte, size)
		n, err := lz4.UncompressBlock(chunk, out)
		if err != nil {
			return nil, err
		}
		return out[:n], nil
	case CodecZstd:
		maxMem := uint64(size) * 4
		if maxMem < 1<<20 {
			maxMem = 1 << 20
		}
		dec, err := zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(maxMem),
		)
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		out, err := dec.DecodeAll(chunk, nil)
		if err != nil {
			return nil, err
		}
		return out, nil
	default:
		return nil, errf(ErrUnsupported, "cannot decode %q blocks", codec)
	}
}

// --- compression -----------------------------------------------------------

type codecEncoder struct {
	codec Codec
	level int
	zstd  *zstd.Encoder
}

func newCodecEncoder(codec Codec, level int) (*codecEncoder, error) {
	e := &codecEncoder{codec: codec, level: level}
	if codec == CodecZstd {
		zlevel := 3
		if level > 0 {
			zlevel = 1 + (level*21)/100
			if zlevel > 22 {
				zlevel = 22
			}
		}
		enc, err := zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(zlevel)),
			zstd.WithEncoderConcurrency(1),
		)
		if err != nil {
			return nil, err
		}
		e.zstd = enc
	}
	return e, nil
}

func (e *codecEncoder) close() {
	if e.zstd != nil {
		_ = e.zstd.Close()
	}
}

// compress returns the compressed bytes, or nil when the codec cannot make
// the chunk smaller.
func (e *codecEncoder) compress(chunk []byte) ([]byte, error) {
	switch e.codec {
	case CodecZlib:
		lvl := zlib.DefaultCompression
		if e.level > 0 {
			lvl = 1 + (e.level*9)/100
			if lvl > 9 {
				lvl = 9
			}
		}
		var buf bytes.Buffer
		w, err := zlib.NewWriterLevel(&buf, lvl)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(chunk); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		if buf.Len() >= len(chunk) {
			return nil, nil
		}
		return buf.Bytes(), nil
	case CodecLZ4:
		dst := make([]byte, lz4.CompressBlockBound(len(chunk)))
		n, err := lz4.CompressBlock(chunk, dst, nil)
		if err != nil {
			return nil, err
		}
		if n == 0 || n >= len(chunk) {
			return nil, nil
		}
		return dst[:n], nil
	case CodecLZ4HC:
		n := 9
		if e.level > 0 {
			n = 1 + (e.level*9)/100
			if n > 9 {
				n = 9
			}
		}
		levels := [...]lz4.CompressionLevel{
			lz4.Level1, lz4.Level2, lz4.Level3, lz4.Level4, lz4.Level5,
			lz4.Level6, lz4.Level7, lz4.Level8, lz4.Level9,
		}
		hc := lz4.CompressorHC{Level: levels[n-1]}
		dst := make([]byte, lz4.CompressBlockBound(len(chunk)))
		compressed, err := hc.CompressBlock(chunk, dst)
		if err != nil {
			return nil, err
		}
		if compressed == 0 || compressed >= len(chunk) {
			return nil, nil
		}
		return dst[:compressed], nil
	case CodecZstd:
		out := e.zstd.EncodeAll(chunk, nil)
		if len(out) >= len(chunk) {
			return nil, nil
		}
		return out, nil
	default:
		return nil, errf(ErrUnsupported, "cannot encode %q blocks", e.codec)
	}
}

// maxCodecChunk is the most a codec takes in one call. Larger blocks are
// split into subblocks, which the format already requires for that reason.
func maxCodecChunk(codec Codec) int {
	switch codec {
	case CodecZlib:
		return math.MaxUint32
	case CodecLZ4, CodecLZ4HC:
		return 0x7E00_0000
	default:
		return math.MaxInt32
	}
}

// encodeCompression applies shuffling, splits the block, compresses each
// subblock, and stores incompressible chunks raw. It returns the stored bytes
// and the subblock table to record. A nil or empty subblock table means the
// block was encoded as a single stream without a subblocks attribute.
func encodeCompression(plain []byte, c *Compression, level int) (stored []byte, subs []Subblock, err error) {
	if c == nil {
		return append([]byte(nil), plain...), nil, nil
	}
	data := plain
	if c.Shuffled {
		if c.ItemSize <= 0 {
			return nil, nil, errf(ErrUnsupported, "shuffled compression needs a positive item size")
		}
		data = shuffle(data, c.ItemSize)
	}

	var sizes []int
	if len(c.Subblocks) > 0 {
		total := 0
		for _, s := range c.Subblocks {
			sizes = append(sizes, int(s.Uncompressed))
			total += int(s.Uncompressed)
		}
		if total != len(data) {
			return nil, nil, errf(ErrCorrupt, "subblocks cover %d bytes but the image has %d", total, len(data))
		}
	} else {
		limit := maxCodecChunk(c.Codec)
		if len(data) == 0 {
			sizes = []int{0}
		} else {
			for off := 0; off < len(data); {
				n := limit
				if n > len(data)-off {
					n = len(data) - off
				}
				sizes = append(sizes, n)
				off += n
			}
		}
	}

	enc, err := newCodecEncoder(c.Codec, level)
	if err != nil {
		return nil, nil, err
	}
	defer enc.close()

	var out []byte
	offset := 0
	for _, n := range sizes {
		if offset+n > len(data) {
			return nil, nil, errf(ErrCorrupt, "subblock sizes exceed the data")
		}
		chunk := data[offset : offset+n]
		offset += n
		comp, err := enc.compress(chunk)
		if err != nil {
			return nil, nil, err
		}
		if comp == nil {
			out = append(out, chunk...)
			subs = append(subs, Subblock{Compressed: int64(len(chunk)), Uncompressed: int64(len(chunk))})
		} else {
			out = append(out, comp...)
			subs = append(subs, Subblock{Compressed: int64(len(comp)), Uncompressed: int64(len(chunk))})
		}
	}
	return out, subs, nil
}

// --- checksums -------------------------------------------------------------

func hashDigest(alg ChecksumAlgorithm, data []byte) ([]byte, error) {
	switch alg {
	case SHA1:
		sum := sha1.Sum(data)
		return sum[:], nil
	case SHA256:
		sum := sha256.Sum256(data)
		return sum[:], nil
	case SHA512:
		sum := sha512.Sum512(data)
		return sum[:], nil
	default:
		return nil, errf(ErrUnsupported, "unknown checksum algorithm %q", alg)
	}
}

func verifyChecksum(stored []byte, c *Checksum) error {
	if c == nil {
		return nil
	}
	got, err := hashDigest(c.Algorithm, stored)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, c.Digest) {
		return errf(ErrChecksum, "%s digest does not match the stored block", c.Algorithm)
	}
	return nil
}
