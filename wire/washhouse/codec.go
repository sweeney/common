package washhouse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// EncodeState is s's payload, with its schema set.
func EncodeState(s State) ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	s.Schema = SchemaState
	if s.Tanks == nil {
		s.Tanks = []TankSummary{}
	}
	return json.Marshal(s)
}

// EncodeTank is t's payload, with its schema set.
func EncodeTank(t Tank) ([]byte, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	t.Schema = SchemaTank
	return json.Marshal(t)
}

// EncodeEvent is e's payload, with its schema set. It refuses an event
// that isn't well formed: an unknown event, a body that isn't exactly the
// one its event names, or a tank share without its litres.
func EncodeEvent(e Event) ([]byte, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}
	e.Schema = SchemaEvent
	if d := e.DrawBalanced; d != nil {
		c := *d
		if c.Tanks == nil {
			c.Tanks = []TankShare{}
		}
		if c.Reasons == nil {
			c.Reasons = []Reason{}
		}
		e.DrawBalanced = &c
	}
	return json.Marshal(e)
}

// EncodeStatus is s's payload, with its schema set. Use LWT for the last
// will.
func EncodeStatus(s Status) ([]byte, error) {
	if s.Event == StatusLWT {
		return nil, fmt.Errorf("washhouse: the last will is LWT(), not a status")
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	s.Schema = SchemaStatus
	if s.Tanks == nil {
		s.Tanks = []string{}
	}
	return json.Marshal(s)
}

// DecodeState decodes {base}/state.
func DecodeState(b []byte) (State, error) {
	var s State
	if err := decode(b, SchemaState, &s); err != nil {
		return State{}, err
	}
	return s, s.validate()
}

// DecodeTank decodes {base}/tank/{device_id}.
func DecodeTank(b []byte) (Tank, error) {
	var t Tank
	if err := decode(b, SchemaTank, &t); err != nil {
		return Tank{}, err
	}
	return t, t.validate()
}

// DecodeEvent decodes {base}/events. An event it doesn't know is
// ErrUnknownEvent, for the consumer to ignore.
func DecodeEvent(b []byte) (Event, error) {
	var head struct {
		Schema *string    `json:"schema"`
		Event  *EventType `json:"event"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return Event{}, fmt.Errorf("washhouse: event: %w", err)
	}
	if head.Schema == nil || *head.Schema != SchemaEvent {
		return Event{}, fmt.Errorf("washhouse: event: schema isn't %s", SchemaEvent)
	}
	if head.Event == nil {
		return Event{}, fmt.Errorf("washhouse: event: no event")
	}
	if _, ok := body[*head.Event]; !ok {
		return Event{}, fmt.Errorf("%w %q", ErrUnknownEvent, *head.Event)
	}
	var e Event
	if err := decode(b, SchemaEvent, &e); err != nil {
		return Event{}, err
	}
	if err := e.validate(); err != nil {
		return Event{}, err
	}
	return e, nil
}

// DecodeStatus decodes {base}/status, the last will included (Event
// StatusLWT, Online false, nothing else set).
func DecodeStatus(b []byte) (Status, error) {
	var head struct {
		Event *StatusEvent `json:"event"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return Status{}, fmt.Errorf("washhouse: status: %w", err)
	}
	if head.Event != nil && *head.Event == StatusLWT {
		var c bytes.Buffer
		if err := json.Compact(&c, b); err != nil || !bytes.Equal(c.Bytes(), LWT()) {
			return Status{}, fmt.Errorf("washhouse: status: a last will must be exactly %s", LWT())
		}
		return Status{Event: StatusLWT}, nil
	}
	var s Status
	if err := decode(b, SchemaStatus, &s); err != nil {
		return Status{}, err
	}
	return s, s.validate()
}

// decode checks that b carries every key v's type requires, then decodes
// it into v and checks its schema. Unknown keys are accepted: new fields
// are additive.
func decode(b []byte, schema string, v any) error {
	t := reflect.TypeOf(v).Elem()
	name := strings.TrimPrefix(strings.TrimSuffix(schema, ".v1"), "net.swee.washhouse.")
	if err := present(b, t, name); err != nil {
		return fmt.Errorf("washhouse: %w", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("washhouse: %s: %w", name, err)
	}
	if got := reflect.ValueOf(v).Elem().FieldByName("Schema").String(); got != schema {
		return fmt.Errorf("washhouse: %s: schema %q, want %q", name, got, schema)
	}
	return nil
}

var (
	timeType = reflect.TypeOf(Time{})
	numType  = reflect.TypeOf(Num(0))
)

func isNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// present checks raw against t, recursively: every key of a struct is
// there unless its field is omitempty, and only a pointer may be null
// (an omitempty one is absent, never null). The values themselves are
// json.Unmarshal's to check.
func present(raw []byte, t reflect.Type, path string) error {
	if t.Kind() == reflect.Pointer {
		if isNull(raw) {
			return nil
		}
		t = t.Elem()
	} else if isNull(raw) {
		return fmt.Errorf("%s is null", path)
	}
	if t == timeType || t == numType {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		// json.Unmarshal matches keys ignoring case, so a key that
		// differs from a known one only in case would override it.
		for k := range m {
			for i := range t.NumField() {
				key, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
				if k != key && strings.EqualFold(k, key) {
					return fmt.Errorf("%s.%s differs from %s only in case", path, k, key)
				}
			}
		}
		for i := range t.NumField() {
			f := t.Field(i)
			key, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			omit := strings.Contains(opts, "omitempty")
			v, ok := m[key]
			switch {
			case !ok && omit:
				continue
			case !ok:
				return fmt.Errorf("%s.%s is missing", path, key)
			case omit && isNull(v):
				return fmt.Errorf("%s.%s is null: it is absent when there is none", path, key)
			}
			if err := present(v, f.Type, path+"."+key); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var a []json.RawMessage
		if err := json.Unmarshal(raw, &a); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for i, v := range a {
			if err := present(v, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}
