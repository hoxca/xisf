package xisf

import (
	"encoding/hex"
	"strconv"
	"strings"
)

// LocKind is the kind of an XISF data block location.
type LocKind int

const (
	// LocInline is data in the character contents of the element.
	LocInline LocKind = iota
	// LocEmbedded is data in the character contents of a child Data element.
	LocEmbedded
	// LocAttachment is data after the header in a monolithic file.
	LocAttachment
	// LocPath is data in an external local file (distributed units only).
	LocPath
	// LocURL is data at an external URL (distributed units only).
	LocURL
)

// TextEncoding is the encoding of an inline or embedded data block.
type TextEncoding int

const (
	// EncBase64 is Base64 text.
	EncBase64 TextEncoding = iota
	// EncHex is lowercase Base16 text.
	EncHex
)

func (e TextEncoding) String() string {
	if e == EncHex {
		return "hex"
	}
	return "base64"
}

// Location describes where a data block's bytes are.
type Location struct {
	// Kind is the location model.
	Kind LocKind
	// Encoding applies to inline and embedded blocks.
	Encoding TextEncoding
	// Position and Size are the byte offset and length for attachment blocks.
	Position int64
	Size     int64
	// Path is the target for LocPath and LocURL.
	Path string
	// Index is the block index for a block stored in a .xisb file.
	Index *int64
}

// parseLocation parses a location attribute. The specification defines two
// grammars: colon-separated fields for inline and attachment, and
// parenthesised targets for path and url.
func parseLocation(s string) (Location, error) {
	s = strings.TrimSpace(s)
	if loc, ok, err := parseParenthesised(s); ok {
		return loc, err
	}
	scheme, rest, hasRest := strings.Cut(s, ":")
	switch scheme {
	case "embedded":
		if hasRest {
			return Location{}, errf(ErrHeader, "embedded location takes no parameters, got %q", rest)
		}
		return Location{Kind: LocEmbedded, Encoding: EncBase64}, nil
	case "inline":
		switch rest {
		case "base64":
			return Location{Kind: LocInline, Encoding: EncBase64}, nil
		case "hex":
			return Location{Kind: LocInline, Encoding: EncHex}, nil
		default:
			return Location{}, errf(ErrHeader, "inline encoding must be base64 or hex, got %q", rest)
		}
	case "attachment":
		pos, size, ok := strings.Cut(rest, ":")
		if !ok {
			return Location{}, errf(ErrHeader, "attachment needs position:size, got %q", rest)
		}
		p, err := parseUint(pos, "attachment position")
		if err != nil {
			return Location{}, err
		}
		n, err := parseUint(size, "attachment size")
		if err != nil {
			return Location{}, err
		}
		return Location{Kind: LocAttachment, Position: p, Size: n}, nil
	default:
		return Location{}, errf(ErrHeader, "unknown location scheme %q", scheme)
	}
}

func parseParenthesised(s string) (Location, bool, error) {
	var kind LocKind
	var body string
	switch {
	case strings.HasPrefix(s, "path("):
		kind, body = LocPath, strings.TrimPrefix(s, "path(")
	case strings.HasPrefix(s, "url("):
		kind, body = LocURL, strings.TrimPrefix(s, "url(")
	default:
		return Location{}, false, nil
	}
	// The locator extends to the last closing parenthesis, so a URL or path
	// may contain parentheses of its own.
	close := strings.LastIndexByte(body, ')')
	if close < 0 {
		return Location{}, true, errf(ErrHeader, "missing closing parenthesis in %q", s)
	}
	target := strings.TrimSpace(body[:close])
	if target == "" {
		return Location{}, true, errf(ErrHeader, "empty locator in %q", s)
	}
	loc := Location{Kind: kind, Path: target}
	if tail := strings.TrimSpace(body[close+1:]); tail != "" {
		if !strings.HasPrefix(tail, ":") {
			return Location{}, true, errf(ErrHeader, "unexpected %q after locator", tail)
		}
		index, err := parseBlockIndex(strings.TrimSpace(tail[1:]))
		if err != nil {
			return Location{}, true, err
		}
		loc.Index = &index
	}
	return loc, true, nil
}

func parseBlockIndex(s string) (int64, error) {
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		base = 16
		s = s[2:]
	}
	v, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		return 0, errf(ErrHeader, "invalid block index %q", s)
	}
	return int64(v), nil
}

func (l Location) String() string {
	switch l.Kind {
	case LocInline:
		return "inline:" + l.Encoding.String()
	case LocEmbedded:
		return "embedded"
	case LocAttachment:
		return "attachment:" + strconv.FormatInt(l.Position, 10) + ":" + strconv.FormatInt(l.Size, 10)
	case LocPath:
		s := "path(" + l.Path + ")"
		if l.Index != nil {
			s += ":" + formatIndex(*l.Index)
		}
		return s
	case LocURL:
		s := "url(" + l.Path + ")"
		if l.Index != nil {
			s += ":" + formatIndex(*l.Index)
		}
		return s
	}
	return ""
}

func formatIndex(v int64) string { return "0x" + strconv.FormatUint(uint64(v), 16) }

// ByteOrder is the byte order of a data block's multi-byte values.
type ByteOrder int

const (
	// LittleEndian is the byte order of ordinary XISF data blocks.
	LittleEndian ByteOrder = iota
	// BigEndian is selected by the byteOrder="big" attribute.
	BigEndian
)

func parseByteOrder(s string) (ByteOrder, error) {
	switch s {
	case "little":
		return LittleEndian, nil
	case "big":
		return BigEndian, nil
	default:
		return LittleEndian, errf(ErrHeader, "unknown byteOrder %q", s)
	}
}

func (o ByteOrder) String() string {
	if o == BigEndian {
		return "big"
	}
	return "little"
}

// Codec names a compression algorithm.
type Codec string

const (
	// CodecZlib is DEFLATE in a zlib wrapper.
	CodecZlib Codec = "zlib"
	// CodecLZ4 is the LZ4 block format.
	CodecLZ4 Codec = "lz4"
	// CodecLZ4HC is the LZ4 block format from the high-compression encoder.
	CodecLZ4HC Codec = "lz4hc"
	// CodecZstd is a Zstandard frame.
	CodecZstd Codec = "zstd"
)

// Subblock is one compression subblock: compressed and uncompressed lengths.
type Subblock struct {
	// Compressed is the length in bytes of the stored subblock.
	Compressed int64
	// Uncompressed is the length the subblock expands to. Equal values mean
	// the subblock is stored raw.
	Uncompressed int64
}

// Compression describes how a data block was compressed.
type Compression struct {
	// Codec is the algorithm.
	Codec Codec
	// Shuffled reports whether the byte-shuffling transform was applied.
	Shuffled bool
	// UncompressedSize is the total length after decompression. Writers may
	// leave it zero; it is filled in from the block being written.
	UncompressedSize int64
	// ItemSize is the shuffle item size in bytes, when Shuffled is set.
	ItemSize int
	// Subblocks is the subblock table, when the block was split.
	Subblocks []Subblock
}

func parseCompression(s string) (*Compression, error) {
	fields := strings.Split(s, ":")
	method := fields[0]
	shuffled := false
	if strings.HasSuffix(method, "+sh") {
		shuffled = true
		method = strings.TrimSuffix(method, "+sh")
	}
	var codec Codec
	switch method {
	case "zlib":
		codec = CodecZlib
	case "lz4":
		codec = CodecLZ4
	case "lz4hc":
		codec = CodecLZ4HC
	case "zstd":
		codec = CodecZstd
	default:
		return nil, errf(ErrUnsupported, "unknown compression codec %q", method)
	}
	if len(fields) < 2 {
		return nil, errf(ErrHeader, "compression %q has no uncompressed size", s)
	}
	size, err := parseUint(fields[1], "uncompressed size")
	if err != nil {
		return nil, err
	}
	c := &Compression{Codec: codec, Shuffled: shuffled, UncompressedSize: size}
	rest := fields[2:]
	if shuffled {
		if len(rest) == 0 {
			return nil, errf(ErrHeader, "shuffled compression %q has no item size", s)
		}
		item, err := parseUint(rest[0], "shuffle item size")
		if err != nil {
			return nil, err
		}
		c.ItemSize = int(item)
		rest = rest[1:]
	}
	if len(rest) != 0 {
		return nil, errf(ErrHeader, "unexpected trailing field in compression %q", s)
	}
	if c.ItemSize < 0 {
		return nil, errf(ErrHeader, "shuffle item size is negative")
	}
	return c, nil
}

func (c *Compression) String() string {
	if c == nil {
		return ""
	}
	s := string(c.Codec)
	if c.Shuffled {
		s += "+sh"
	}
	s += ":" + strconv.FormatInt(c.UncompressedSize, 10)
	if c.Shuffled {
		s += ":" + strconv.Itoa(c.ItemSize)
	}
	return s
}

func parseSubblocks(s string) ([]Subblock, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []Subblock
	for _, part := range strings.Split(s, ":") {
		c, u, ok := strings.Cut(part, ",")
		if !ok {
			return nil, errf(ErrHeader, "subblock %q must be compressed,uncompressed", part)
		}
		ci, err := parseUint(strings.TrimSpace(c), "subblock compressed length")
		if err != nil {
			return nil, err
		}
		ui, err := parseUint(strings.TrimSpace(u), "subblock uncompressed length")
		if err != nil {
			return nil, err
		}
		out = append(out, Subblock{Compressed: ci, Uncompressed: ui})
	}
	return out, nil
}

func formatSubblocks(subs []Subblock) string {
	parts := make([]string, len(subs))
	for i, s := range subs {
		parts[i] = strconv.FormatInt(s.Compressed, 10) + "," + strconv.FormatInt(s.Uncompressed, 10)
	}
	return strings.Join(parts, ":")
}

// ChecksumAlgorithm is a hash algorithm permitted for a data block checksum.
type ChecksumAlgorithm string

const (
	// SHA1 is the SHA-1 digest, spelled "sha-1" in the header.
	SHA1 ChecksumAlgorithm = "sha-1"
	// SHA256 is SHA-2 with a 256-bit digest.
	SHA256 ChecksumAlgorithm = "sha-256"
	// SHA512 is SHA-2 with a 512-bit digest.
	SHA512 ChecksumAlgorithm = "sha-512"
)

func parseChecksumAlgorithm(s string) (ChecksumAlgorithm, error) {
	switch s {
	case "sha-1", "sha1":
		return SHA1, nil
	case "sha-256", "sha256":
		return SHA256, nil
	case "sha-512", "sha512":
		return SHA512, nil
	default:
		return "", errf(ErrUnsupported, "unknown checksum algorithm %q", s)
	}
}

// Checksum is a parsed checksum attribute.
type Checksum struct {
	// Algorithm is the hash function.
	Algorithm ChecksumAlgorithm
	// Digest is the raw hash.
	Digest []byte
}

func parseChecksum(s string) (*Checksum, error) {
	alg, digest, ok := strings.Cut(s, ":")
	if !ok {
		return nil, errf(ErrHeader, "checksum %q has no digest", s)
	}
	a, err := parseChecksumAlgorithm(alg)
	if err != nil {
		return nil, err
	}
	d, err := hex.DecodeString(strings.TrimSpace(digest))
	if err != nil {
		return nil, errf(ErrHeader, "checksum digest is not hexadecimal")
	}
	return &Checksum{Algorithm: a, Digest: d}, nil
}

func (c *Checksum) String() string {
	if c == nil {
		return ""
	}
	return string(c.Algorithm) + ":" + hex.EncodeToString(c.Digest)
}

func parseUint(s, what string) (int64, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, errf(ErrHeader, "%s is not an unsigned integer: %q", what, s)
	}
	if v > uint64(^uint64(0)>>1) {
		return 0, errf(ErrHeader, "%s is too large", what)
	}
	return int64(v), nil
}
