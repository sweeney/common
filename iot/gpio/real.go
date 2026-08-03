//go:build linux

package gpio

import (
	"fmt"

	"github.com/sweeney/common/iot/profile"
	"github.com/warthog618/go-gpiocdev"
)

// RealChannelReader reads N GPIO lines from actual hardware using the Linux
// GPIO character device. Inversion is applied per channel via ActiveLow.
type RealChannelReader struct {
	chip     *gpiocdev.Chip
	lines    []*gpiocdev.Line
	channels []profile.Channel
}

// NewRealChannelReader requests one input line per channel (bias per the
// channel's Pull mode) and returns a reader aligned to the profile's channel
// order.
func NewRealChannelReader(channels []profile.Channel) (*RealChannelReader, error) {
	chip, err := gpiocdev.NewChip("gpiochip0")
	if err != nil {
		return nil, fmt.Errorf("open gpio chip: %w", err)
	}

	r := &RealChannelReader{chip: chip, channels: channels}
	for _, c := range channels {
		line, err := chip.RequestLine(c.Pin, gpiocdev.AsInput, pullOption(c.Pull))
		if err != nil {
			r.Close()
			return nil, fmt.Errorf("request %s pin %d: %w", c.Key, c.Pin, err)
		}
		r.lines = append(r.lines, line)
	}
	return r, nil
}

// pullOption maps a profile pull mode to the gpiocdev line option. PullDown is
// the default (matches Pi boot state); PullUp is for buttons wired to GND.
func pullOption(p profile.PullMode) gpiocdev.LineReqOption {
	if p == profile.PullUp {
		return gpiocdev.WithPullUp
	}
	return gpiocdev.WithPullDown
}

// ReadChannels returns one logical value per channel in profile order,
// applying each channel's ActiveLow inversion.
func (r *RealChannelReader) ReadChannels() ([]bool, error) {
	out := make([]bool, len(r.lines))
	for i, line := range r.lines {
		raw, err := line.Value()
		if err != nil {
			return nil, fmt.Errorf("read %s pin: %w", r.channels[i].Key, err)
		}
		// ActiveLow: raw active (1) = OFF, raw inactive (0) = ON.
		// Active-high: raw active (1) = ON.
		if r.channels[i].ActiveLow {
			out[i] = raw == 0
		} else {
			out[i] = raw == 1
		}
	}
	return out, nil
}

// Close reconfigures lines to input/pull-down (Pi boot defaults) then releases
// them and the chip, so external hardware does not hold pins at boot.
func (r *RealChannelReader) Close() error {
	var errs []error
	for i, line := range r.lines {
		if line == nil {
			continue
		}
		if err := line.Reconfigure(gpiocdev.AsInput, gpiocdev.WithPullDown); err != nil {
			errs = append(errs, fmt.Errorf("reconfigure %s pin: %w", r.channels[i].Key, err))
		}
		if err := line.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s pin: %w", r.channels[i].Key, err))
		}
	}
	if r.chip != nil {
		if err := r.chip.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close chip: %w", err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}
