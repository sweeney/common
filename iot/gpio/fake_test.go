package gpio

import (
	"errors"
	"testing"
)

func TestFakeChannelReaderRead(t *testing.T) {
	samples := [][]bool{
		{true, false},
		{false, true},
		{true, true},
	}

	f := NewFakeChannelReader(samples)

	for i, want := range samples {
		got, err := f.ReadChannels()
		if err != nil {
			t.Fatalf("sample %d: unexpected error: %v", i, err)
		}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("sample %d: got %v, want %v", i, got, want)
		}
	}

	// Fourth read should repeat the last sample.
	got, err := f.ReadChannels()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0] != true || got[1] != true {
		t.Errorf("repeat: got %v, want [true true]", got)
	}
}

func TestFakeChannelReaderNoSamples(t *testing.T) {
	f := NewFakeChannelReader(nil)
	if _, err := f.ReadChannels(); err == nil {
		t.Error("expected error with no samples")
	}
}

func TestFakeChannelReaderError(t *testing.T) {
	f := NewFakeChannelReader([][]bool{{true, true}})
	f.ReadError = errors.New("simulated error")

	_, err := f.ReadChannels()
	if err == nil {
		t.Error("expected error to be returned")
	}
	if err.Error() != "simulated error" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFakeChannelReaderClose(t *testing.T) {
	f := NewFakeChannelReader([][]bool{{true, true}})

	if f.Closed {
		t.Error("should not be closed initially")
	}
	if err := f.Close(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !f.Closed {
		t.Error("should be closed after Close()")
	}
}

func TestFakeChannelReaderReset(t *testing.T) {
	samples := [][]bool{
		{true, false},
		{false, true},
	}

	f := NewFakeChannelReader(samples)
	f.ReadChannels() // consume first
	f.Reset()

	got, _ := f.ReadChannels()
	if got[0] != true || got[1] != false {
		t.Errorf("after reset: got %v, want [true false]", got)
	}
}
