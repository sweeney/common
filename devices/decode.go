package devices

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Problem is something wrong with one device in a document. Decode reports
// values it had to drop; Check reports the rest. Neither refuses a document.
type Problem struct {
	ID      string // device id
	Field   string // JSON key, or empty for the whole device
	Code    string // one of the Code constants
	Message string
}

func (p Problem) String() string {
	if p.Field == "" {
		return fmt.Sprintf("%s: %s: %s", p.ID, p.Code, p.Message)
	}
	return fmt.Sprintf("%s.%s: %s: %s", p.ID, p.Field, p.Code, p.Message)
}

// The codes a Problem carries. They reuse the floorplan validator's names
// where the two check the same thing.
const (
	CodeBadValue          = "device.bad_value"          // Decode: a value did not fit its field and was dropped
	CodeFloorMismatch     = "device.floor_mismatch"     // room is not on the declared floor
	CodeCoversUnresolved  = "device.covers_unresolved"  // covers is neither house nor a room id
	CodeIdentityMissing   = "device.identity_missing"   // nothing a service can match the device by
	CodeDuplicateIdentity = "device.duplicate_identity" // two devices share a scheme and primary or display
	CodeLegacyLocation    = "device.legacy_location"    // still carries the deprecated location
)

// ErrNotObject is returned by Decode for a document that is not a JSON object.
var ErrNotObject = errors.New("devices: document is not a JSON object")

// fields maps each JSON key to its Device field index, from the struct tags.
var fields = func() map[string]int {
	m := map[string]int{}
	t := reflect.TypeOf(Device{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		m[name] = i
	}
	return m
}()

// fieldFor finds the field for a key as encoding/json would: an exact match
// first, then case-insensitively.
func fieldFor(key string) (int, bool) {
	if i, ok := fields[key]; ok {
		return i, true
	}
	for name, i := range fields {
		if strings.EqualFold(name, key) {
			return i, true
		}
	}
	return 0, false
}

// Decode reads a devices_<site> document. It fails only when data is not a
// JSON object. A value that does not fit its field is dropped and reported as
// CodeBadValue, and the rest of that device is kept; a device that is not an
// object is left out and reported. Unknown keys are ignored, so a namespace
// can gain a key before this package knows it. Every device comes back
// normalised. Problems are ordered by device id, then key.
func Decode(data []byte) (map[string]Device, []Problem, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrNotObject, err)
	}
	if raw == nil {
		return nil, nil, ErrNotObject
	}

	ids := make([]string, 0, len(raw))
	for id := range raw {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make(map[string]Device, len(raw))
	var problems []Problem
	for _, id := range ids {
		d, ps, ok := decodeDevice(id, raw[id])
		problems = append(problems, ps...)
		if ok {
			d.Normalise()
			out[id] = d
		}
	}
	return out, problems, nil
}

func decodeDevice(id string, data json.RawMessage) (Device, []Problem, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return Device{}, []Problem{{
			ID: id, Code: CodeBadValue,
			Message: fmt.Sprintf("device is %s, not an object; left out", kind(data)),
		}}, false
	}

	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var d Device
	var problems []Problem
	dv := reflect.ValueOf(&d).Elem()
	for _, k := range keys {
		i, ok := fieldFor(k)
		if !ok {
			continue
		}
		// Decode into a fresh value and set it only on success, so a bad
		// value leaves no partial result behind.
		p := reflect.New(dv.Field(i).Type())
		if err := json.Unmarshal(raw[k], p.Interface()); err != nil {
			problems = append(problems, Problem{
				ID: id, Field: k, Code: CodeBadValue,
				Message: fmt.Sprintf("dropped: %v", err),
			})
			continue
		}
		dv.Field(i).Set(p.Elem())
	}
	return d, problems, true
}

func kind(data json.RawMessage) string {
	switch b := bytes.TrimSpace(data); {
	case len(b) == 0:
		return "empty"
	case b[0] == '[':
		return "an array"
	case b[0] == '"':
		return "a string"
	case bytes.Equal(b, []byte("null")):
		return "null"
	case bytes.Equal(b, []byte("true")), bytes.Equal(b, []byte("false")):
		return "a boolean"
	default:
		return "a number"
	}
}
