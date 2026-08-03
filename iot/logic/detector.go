package logic

import (
	"time"

	"github.com/sweeney/common/iot/profile"
)

// Detector tracks state and detects debounced transitions for N channels.
//
// Internally it holds an ordered slice of channel states (the profile's channel
// order) plus a key index. It is fully sensor-agnostic: channels are keyed by
// Channel.Key and emit the Channel.OnEvent / Channel.OffEvent names.
type Detector struct {
	// debounceDuration is the profile-wide default debounce, used for channels
	// that do not set a per-channel Debounce.
	debounceDuration time.Duration
	// channels are the per-channel debounce states in profile order.
	channels []*channelState
	// index maps channel key -> position in channels.
	index map[string]int
	// counts counts emitted events keyed by event name.
	counts map[string]int

	baselined     bool
	startTime     time.Time
	lastHeartbeat time.Time
}

// NewProfileDetector creates a generic N-channel detector from a profile.
// debounceDuration is the default applied to channels without their own.
func NewProfileDetector(p profile.SensorProfile, debounceDuration time.Duration, startTime time.Time) *Detector {
	d := &Detector{
		debounceDuration: debounceDuration,
		index:            make(map[string]int, len(p.Channels)),
		counts:           make(map[string]int),
		startTime:        startTime,
		lastHeartbeat:    startTime,
	}
	for i, c := range p.Channels {
		deb := c.Debounce
		if deb <= 0 {
			deb = debounceDuration
		}
		d.channels = append(d.channels, &channelState{
			key:      c.Key,
			debounce: deb,
			onEvent:  c.OnEvent,
			offEvent: c.OffEvent,
		})
		d.index[c.Key] = i
	}
	return d
}

// ProcessValues takes the logical (post-inversion) value of every channel keyed
// by channel key plus the current time, advances debounce state, and returns
// any transitions as GenericEvents in profile order. No events are emitted
// until baseline is established.
func (d *Detector) ProcessValues(values map[string]bool, now time.Time) []GenericEvent {
	type pending struct {
		cs   *channelState
		name string
	}
	var transitions []pending

	for _, cs := range d.channels {
		newState := boolToState(values[cs.key])
		if name := d.processChannel(cs, newState, now); name != "" {
			transitions = append(transitions, pending{cs: cs, name: name})
		}
	}

	if !d.baselined {
		allBaselined := true
		for _, cs := range d.channels {
			if !cs.baselined {
				allBaselined = false
				break
			}
		}
		if allBaselined {
			d.baselined = true
		}
		return nil // no events until baseline established
	}

	if len(transitions) == 0 {
		return nil
	}

	states := d.statesSnapshot()
	events := make([]GenericEvent, 0, len(transitions))
	for _, p := range transitions {
		events = append(events, GenericEvent{
			Timestamp: now,
			Key:       p.cs.key,
			Name:      p.name,
			States:    states,
		})
		d.counts[p.name]++
	}
	return events
}

// processChannel handles debounce logic for a single channel.
// Returns the event name if a transition occurred, "" otherwise.
func (d *Detector) processChannel(ch *channelState, newState State, now time.Time) string {
	debounce := ch.debounce

	// First time seeing this channel — establish baseline.
	if !ch.baselined {
		if ch.pending == "" {
			ch.pending = newState
			ch.pendingSince = now
			return ""
		}
		if ch.pending != newState {
			// State changed during baseline, restart.
			ch.pending = newState
			ch.pendingSince = now
			return ""
		}
		if now.Sub(ch.pendingSince) >= debounce {
			ch.stable = newState
			ch.baselined = true
			ch.pending = ""
		}
		return ""
	}

	// Already baselined — detect transitions.
	if newState == ch.stable {
		ch.pending = ""
		return ""
	}

	if ch.pending != newState {
		ch.pending = newState
		ch.pendingSince = now
		return ""
	}

	if now.Sub(ch.pendingSince) >= debounce {
		oldState := ch.stable
		ch.stable = newState
		ch.pending = ""
		return eventNameForTransition(ch, oldState, newState)
	}

	return ""
}

func (d *Detector) statesSnapshot() map[string]State {
	states := make(map[string]State, len(d.channels))
	for _, cs := range d.channels {
		states[cs.key] = cs.stable
	}
	return states
}

func boolToState(b bool) State {
	if b {
		return StateOn
	}
	return StateOff
}

// eventNameForTransition returns the channel's configured On/Off event name.
func eventNameForTransition(ch *channelState, from, to State) string {
	if to == StateOn {
		return ch.onEvent
	}
	return ch.offEvent
}

// StartTime returns the time the detector was created.
func (d *Detector) StartTime() time.Time {
	return d.startTime
}

// GenericCountsSnapshot returns a copy of event counts keyed by event name.
func (d *Detector) GenericCountsSnapshot() map[string]int {
	out := make(map[string]int, len(d.counts))
	for k, v := range d.counts {
		out[k] = v
	}
	return out
}

// IsBaselined returns whether all channels have established a baseline.
func (d *Detector) IsBaselined() bool {
	return d.baselined
}

// CurrentStates returns a copy of the current stable state of every channel
// keyed by channel key.
func (d *Detector) CurrentStates() map[string]State {
	return d.statesSnapshot()
}

// CheckHeartbeat returns heartbeat data if the interval has elapsed since the
// last heartbeat (or startup). Returns nil if not yet baselined, if the
// interval has not elapsed, or if interval is <= 0 (disabled).
func (d *Detector) CheckHeartbeat(now time.Time, interval time.Duration) *HeartbeatData {
	if interval <= 0 {
		return nil
	}
	if !d.baselined {
		return nil
	}
	if now.Sub(d.lastHeartbeat) < interval {
		return nil
	}

	d.lastHeartbeat = now
	return &HeartbeatData{
		Timestamp: now,
		Uptime:    now.Sub(d.startTime),
		Counts:    d.GenericCountsSnapshot(),
	}
}
