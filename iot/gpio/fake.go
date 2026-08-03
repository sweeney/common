package gpio

import "errors"

// FakeChannelReader is a generic N-channel test double. Each Sample is a slice
// of logical (post-inversion) values aligned to the profile's channel order.
type FakeChannelReader struct {
	// Samples contains scripted per-channel values to return.
	Samples [][]bool

	index int

	// Closed tracks if Close was called.
	Closed bool

	// ReadError, if set, will be returned by ReadChannels().
	ReadError error
}

// NewFakeChannelReader creates a FakeChannelReader with the given samples.
func NewFakeChannelReader(samples [][]bool) *FakeChannelReader {
	return &FakeChannelReader{Samples: samples}
}

// ReadChannels returns the next scripted sample, repeating the last when
// samples are exhausted.
func (f *FakeChannelReader) ReadChannels() ([]bool, error) {
	if f.ReadError != nil {
		return nil, f.ReadError
	}
	if len(f.Samples) == 0 {
		return nil, errors.New("no samples configured")
	}
	sample := f.Samples[f.index]
	if f.index < len(f.Samples)-1 {
		f.index++
	}
	return sample, nil
}

// Close marks the reader as closed.
func (f *FakeChannelReader) Close() error {
	f.Closed = true
	return nil
}

// Reset resets the reader to the beginning of samples.
func (f *FakeChannelReader) Reset() {
	f.index = 0
	f.Closed = false
}
