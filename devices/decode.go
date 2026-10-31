package devices

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
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
	CodeDuplicateKey      = "device.duplicate_key"      // Decode: a field given more than once; the last wins
	CodeDuplicateID       = "device.duplicate_id"       // Decode: a device id given more than once; the last wins
	CodeRoomMalformed     = "device.room_malformed"     // room is not a "<floor>.<room>" id
	CodeFloorMismatch     = "device.floor_mismatch"     // room is not on the declared floor
	CodeCoversUnresolved  = "device.covers_unresolved"  // covers is neither house nor a room id
	CodeIdentityMissing   = "device.identity_missing"   // nothing a service can match the device by
	CodeDuplicateIdentity = "device.duplicate_identity" // two devices share a scheme and primary or display
	CodeLegacyLocation    = "device.legacy_location"    // still carries the deprecated location
)

// ErrNotObject is returned by Decode for a document that is not a JSON object.
var ErrNotObject = errors.New("devices: document is not a JSON object")

var (
	deviceFields    = jsonFields(reflect.TypeOf(Device{}))
	thresholdFields = jsonFields(reflect.TypeOf(Thresholds{}))
	durationPtr     = reflect.TypeOf((*time.Duration)(nil))
)

// jsonFields maps each JSON key of a struct type to its field index.
func jsonFields(t reflect.Type) map[string]int {
	m := map[string]int{}
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		m[name] = i
	}
	return m
}

// fieldFor finds the field for a key as encoding/json would: an exact match
// first, then case-insensitively.
func fieldFor(fields map[string]int, key string) (int, bool) {
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

// entry is one key of a JSON object and its value.
type entry struct {
	key string
	raw json.RawMessage
}

// entries reads a JSON object's keys in document order, keeping a key given
// twice, which unmarshalling into a map would merge without a word. ok is
// false if data is not exactly one JSON object.
func entries(data []byte) (es []entry, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, isKey := t.(string)
		var raw json.RawMessage
		if !isKey || dec.Decode(&raw) != nil {
			return nil, false
		}
		es = append(es, entry{key, raw})
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return es, true
}

// lastPerField picks, for each field, the last key in the document that names
// it, as encoding/json would, and reports the others without decoding them.
func lastPerField(id, prefix string, es []entry, fields map[string]int) (map[int]entry, []Problem) {
	chosen := map[int]entry{}
	var problems []Problem
	for i := len(es) - 1; i >= 0; i-- {
		f, ok := fieldFor(fields, es[i].key)
		if !ok {
			continue
		}
		if last, ok := chosen[f]; ok {
			problems = append(problems, Problem{
				ID: id, Field: prefix + es[i].key, Code: CodeDuplicateKey,
				Message: fmt.Sprintf("ignored: given again later as %q, which is used", last.key),
			})
			continue
		}
		chosen[f] = es[i]
	}
	return chosen, problems
}

// Decode reads a devices_<site> document. It fails only when data is not a
// JSON object. A value that does not fit its field is dropped and reported as
// CodeBadValue, and the rest of that device is kept; within thresholds, only
// the bad threshold is dropped. A device that is not an object is left out
// and reported. A device id or a field given twice keeps the last, as
// encoding/json does, and is reported. Unknown keys are ignored, so a
// namespace can gain a key before this package knows it. Every device comes
// back normalised. Problems are ordered by device id, then field.
func Decode(data []byte) (map[string]Device, []Problem, error) {
	es, ok := entries(data)
	if !ok {
		return nil, nil, ErrNotObject
	}

	out := make(map[string]Device, len(es))
	var problems []Problem
	// Backwards, so that the copy kept is the last in the document.
	for i := len(es) - 1; i >= 0; i-- {
		id := es[i].key
		if _, seen := out[id]; seen || containsID(problems, id) {
			problems = append(problems, Problem{
				ID: id, Code: CodeDuplicateID,
				Message: "ignored: the same device id is given again later, and that copy is used",
			})
			continue
		}
		d, ps, ok := decodeDevice(id, es[i].raw)
		problems = append(problems, ps...)
		if ok {
			d.Normalise()
			out[id] = d
		}
	}
	sort.SliceStable(problems, func(i, j int) bool {
		a, b := problems[i], problems[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Field < b.Field
	})
	return out, problems, nil
}

// containsID reports whether a device left out of the document (not an
// object) was already met under this id.
func containsID(problems []Problem, id string) bool {
	for _, p := range problems {
		if p.ID == id && p.Field == "" && p.Code == CodeBadValue {
			return true
		}
	}
	return false
}

func decodeDevice(id string, data json.RawMessage) (Device, []Problem, bool) {
	es, ok := entries(data)
	if !ok {
		return Device{}, []Problem{{
			ID: id, Code: CodeBadValue,
			Message: fmt.Sprintf("device is %s, not an object; left out", kind(data)),
		}}, false
	}

	var d Device
	dv := reflect.ValueOf(&d).Elem()
	chosen, problems := lastPerField(id, "", es, deviceFields)
	for i, e := range chosen {
		if i == deviceFields["thresholds"] {
			t, ps := decodeThresholds(id, e)
			problems = append(problems, ps...)
			d.Thresholds = t
			continue
		}
		// Decode into a fresh value and set it only on success, so a bad
		// value leaves no partial result behind.
		p := reflect.New(dv.Field(i).Type())
		if err := json.Unmarshal(e.raw, p.Interface()); err != nil {
			problems = append(problems, Problem{
				ID: id, Field: e.key, Code: CodeBadValue,
				Message: fmt.Sprintf("dropped: %v", err),
			})
			continue
		}
		dv.Field(i).Set(p.Elem())
	}
	return d, problems, true
}

// decodeThresholds decodes thresholds one value at a time, so that a bad
// threshold costs only itself and the device keeps the rest.
func decodeThresholds(id string, e entry) (*Thresholds, []Problem) {
	if bytes.Equal(bytes.TrimSpace(e.raw), []byte("null")) {
		return nil, nil
	}
	es, ok := entries(e.raw)
	if !ok {
		return nil, []Problem{{
			ID: id, Field: e.key, Code: CodeBadValue,
			Message: fmt.Sprintf("dropped: thresholds is %s, not an object", kind(e.raw)),
		}}
	}

	var t Thresholds
	tv := reflect.ValueOf(&t).Elem()
	chosen, problems := lastPerField(id, e.key+".", es, thresholdFields)
	for i, te := range chosen {
		var err error
		if tv.Field(i).Type() == durationPtr {
			var s *string
			if err = json.Unmarshal(te.raw, &s); err == nil && s != nil {
				var d time.Duration
				if d, err = parseDuration(*s); err == nil {
					tv.Field(i).Set(reflect.ValueOf(&d))
				}
			}
		} else {
			p := reflect.New(tv.Field(i).Type())
			if err = json.Unmarshal(te.raw, p.Interface()); err == nil {
				tv.Field(i).Set(p.Elem())
			}
		}
		if err != nil {
			problems = append(problems, Problem{
				ID: id, Field: e.key + "." + te.key, Code: CodeBadValue,
				Message: fmt.Sprintf("dropped: %v", err),
			})
		}
	}
	return &t, problems
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
