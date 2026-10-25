package xisf

import (
	"errors"
	"fmt"
)

// Sentinel errors. Every error returned by this package wraps one of these,
// so callers can classify failures with errors.Is without matching strings.
var (
	ErrNotXISF     = errors.New("not an XISF file")
	ErrTruncated   = errors.New("truncated XISF file")
	ErrHeader      = errors.New("invalid XISF header")
	ErrCorrupt     = errors.New("corrupt data block")
	ErrChecksum    = errors.New("data block checksum mismatch")
	ErrUnsupported = errors.New("unsupported XISF feature")
	ErrNot2D       = errors.New("image is not two-dimensional")
	ErrFormat      = errors.New("sample format mismatch")
	ErrBounds      = errors.New("pixel coordinate out of range")
	ErrProperty    = errors.New("invalid property")
)

func errf(kind error, format string, args ...any) error {
	return fmt.Errorf("xisf: %w: %s", kind, fmt.Sprintf(format, args...))
}
