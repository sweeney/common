package washhouse

import (
	"fmt"
	"regexp"
)

// deviceIDPattern is a device id: a key in devices_<site>, and a topic
// segment.
var deviceIDPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

func deviceID(id string) error {
	if !deviceIDPattern.MatchString(id) {
		return fmt.Errorf("washhouse: device id %q must match [a-z0-9_-]+", id)
	}
	return nil
}

// The closed vocabularies (WIRE.md, Conventions). kind and reasons are
// open, and the event type is open to a consumer (ErrUnknownEvent).
func (s TankState) valid() error {
	switch s {
	case StateHeating, StateDrawing, StateCooling, StateUnknown:
		return nil
	}
	return fmt.Errorf("washhouse: tank state %q isn't heating, drawing, cooling or unknown", s)
}

func validSource(s *Source) error {
	if s == nil || *s == SourceHW || *s == SourceCH {
		return nil
	}
	return fmt.Errorf("washhouse: heat source %q isn't hw or ch", *s)
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s State) validate() error {
	if p := s.Partial; p != nil {
		if len(p.Missing) == 0 {
			return fmt.Errorf("washhouse: a partial names the cylinders missing from it")
		}
		if s.StoredKWh != nil || s.UsableL40 != nil || s.UsableL40Low != nil {
			return fmt.Errorf("washhouse: a partial comes with the totals null")
		}
		blind := map[string]bool{}
		known := 0
		for _, t := range s.Tanks {
			blind[t.DeviceID] = t.StoredKWh == nil
			if t.StoredKWh != nil {
				known++
			}
		}
		if s.LowHotWater != nil && *s.LowHotWater {
			return fmt.Errorf("washhouse: low_hot_water is never true from a partial")
		}
		seen := map[string]bool{}
		for _, id := range p.Missing {
			if err := deviceID(id); err != nil {
				return err
			}
			if seen[id] {
				return fmt.Errorf("washhouse: a partial's missing lists %q twice", id)
			}
			seen[id] = true
			if b, ok := blind[id]; !ok || !b {
				return fmt.Errorf("washhouse: a partial's missing %q must be a tank in tanks whose estimate is null", id)
			}
		}
		if known == 0 {
			return fmt.Errorf("washhouse: a partial needs a known tank: with none, it is null")
		}
	}
	for _, t := range s.Tanks {
		if err := firstErr(deviceID(t.DeviceID), t.State.valid(), validSource(t.HeatSource)); err != nil {
			return err
		}
	}
	return nil
}

func (t Tank) validate() error {
	if err := firstErr(deviceID(t.DeviceID), t.State.valid(), validSource(t.HeatSource)); err != nil {
		return err
	}
	f := t.Fault
	if f == nil {
		return nil
	}
	switch {
	case t.StoredKWh == nil && t.State != StateUnknown:
		return fmt.Errorf("washhouse: a blind tank's state is unknown, not %s", t.State)
	case f.LostAt == nil && t.StoredKWh != nil:
		return fmt.Errorf("washhouse: a fault not yet lost has no estimate")
	case f.Since.After(t.Timestamp.Time):
		return fmt.Errorf("washhouse: a fault since %v is after its message's %v", f.Since.Time, t.Timestamp.Time)
	case f.LostAt != nil && (f.LostAt.Before(f.Since.Time) || f.LostAt.After(t.Timestamp.Time)):
		return fmt.Errorf("washhouse: lost_at %v must be from since %v to the message's %v", f.LostAt.Time, f.Since.Time, t.Timestamp.Time)
	}
	return faultDetail(f.Reason, f.Errors)
}

// faultDetail checks a fault's reason (open, but not empty) and its error
// counts (each at least 1).
func faultDetail(r FaultReason, errs map[string]int) error {
	if r == "" {
		return fmt.Errorf("washhouse: a fault's reason is empty")
	}
	for code, n := range errs {
		if code == "" || n < 1 {
			return fmt.Errorf("washhouse: a fault's error %q counted %d", code, n)
		}
	}
	return nil
}

// validate checks e's body's values; check (washhouse.go) its shape.
func (e Event) validate() error {
	if err := e.check(); err != nil {
		return err
	}
	switch {
	case e.DrawEnded != nil:
		if e.DrawEnded.VolumeKind != VolumeMetered {
			return fmt.Errorf("washhouse: volume_kind %q isn't %s", e.DrawEnded.VolumeKind, VolumeMetered)
		}
	case e.DrawBalanced != nil:
		for _, t := range e.DrawBalanced.Tanks {
			if err := deviceID(t.DeviceID); err != nil {
				return err
			}
		}
	case e.Tank != nil:
		t := e.Tank
		return firstErr(deviceID(t.DeviceID), t.State.valid(), t.Prev.valid(), validSource(t.HeatSource))
	case e.TankLost != nil:
		x := e.TankLost
		if x.Since.After(e.Timestamp.Time) {
			return fmt.Errorf("washhouse: tank_lost since %v is after its timestamp %v", x.Since.Time, e.Timestamp.Time)
		}
		return firstErr(deviceID(x.DeviceID), faultDetail(x.Reason, x.Errors))
	case e.TankRestored != nil:
		x := e.TankRestored
		if x.DurationS < 0 || x.LostSince.After(e.Timestamp.Time) {
			return fmt.Errorf("washhouse: tank_restored lost since %v for %v s, at %v", x.LostSince.Time, x.DurationS, e.Timestamp.Time)
		}
		return deviceID(x.DeviceID)
	}
	return nil
}

// validate checks a status that isn't the last will: online matches the
// event, and a reason comes only with SHUTDOWN.
func (s Status) validate() error {
	switch s.Event {
	case StatusStartup, StatusHeartbeat:
		if !s.Online {
			return fmt.Errorf("washhouse: a %s is online", s.Event)
		}
	case StatusShutdown:
		if s.Online {
			return fmt.Errorf("washhouse: a SHUTDOWN is offline")
		}
	default:
		return fmt.Errorf("washhouse: status event %q isn't STARTUP, HEARTBEAT or SHUTDOWN", s.Event)
	}
	if s.Reason != nil && s.Event != StatusShutdown {
		return fmt.Errorf("washhouse: a %s has no reason", s.Event)
	}
	for _, id := range s.Tanks {
		if err := deviceID(id); err != nil {
			return err
		}
	}
	return nil
}
