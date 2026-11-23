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
	for _, t := range s.Tanks {
		if err := firstErr(deviceID(t.DeviceID), t.State.valid(), validSource(t.HeatSource)); err != nil {
			return err
		}
	}
	return nil
}

func (t Tank) validate() error {
	return firstErr(deviceID(t.DeviceID), t.State.valid(), validSource(t.HeatSource))
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
