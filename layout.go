package xisf

import (
	"encoding/binary"
)

// Signature is the eight bytes every monolithic XISF file begins with.
const Signature = "XISF0100"

// PreambleLen is the size of the signature, header length and reserved field.
const PreambleLen = 16

// scan locates the XML header in a monolithic XISF file.
func scan(data []byte) (header []byte, headerStart, headerEnd int, err error) {
	// A distributed header file is XML and nothing else. This package handles
	// monolithic units only, but detecting the other form lets the error say
	// so instead of "bad signature".
	if looksLikeXML(data) && !startsWith(data, Signature) {
		return nil, 0, 0, errf(ErrUnsupported, "distributed XISF units (.xish) are not supported")
	}
	if len(data) < PreambleLen {
		return nil, 0, 0, errf(ErrTruncated, "need at least %d bytes for the preamble, have %d", PreambleLen, len(data))
	}
	if !startsWith(data, Signature) {
		return nil, 0, 0, errf(ErrNotXISF, "expected signature %q", Signature)
	}
	if data[12] != 0 || data[13] != 0 || data[14] != 0 || data[15] != 0 {
		return nil, 0, 0, errf(ErrHeader, "reserved preamble field is not zero")
	}

	n := binary.LittleEndian.Uint32(data[8:12])
	end := PreambleLen + int(n)
	if end < PreambleLen {
		return nil, 0, 0, errf(ErrHeader, "header length overflows")
	}
	if end > len(data) {
		return nil, 0, 0, errf(ErrTruncated, "header claims %d bytes, %d remain", n, len(data)-PreambleLen)
	}
	return data[PreambleLen:end], PreambleLen, end, nil
}

func startsWith(b []byte, s string) bool {
	if len(b) < len(s) {
		return false
	}
	return string(b[:len(s)]) == s
}

func looksLikeXML(b []byte) bool {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case '<':
			return true
		default:
			return false
		}
	}
	return false
}
