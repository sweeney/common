//go:build !linux

package gpio

import (
	"errors"

	"github.com/sweeney/common/iot/profile"
)

var errUnsupported = errors.New("gpio: not supported on this platform (requires Linux)")

// RealChannelReader is not available on non-Linux platforms.
type RealChannelReader struct{}

// NewRealChannelReader returns an error on non-Linux platforms.
func NewRealChannelReader(channels []profile.Channel) (*RealChannelReader, error) {
	return nil, errUnsupported
}

// ReadChannels is not implemented on non-Linux platforms.
func (r *RealChannelReader) ReadChannels() ([]bool, error) {
	return nil, errors.New("gpio: not supported")
}

// Close is not implemented on non-Linux platforms.
func (r *RealChannelReader) Close() error { return nil }
