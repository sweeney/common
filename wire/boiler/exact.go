package boiler

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// unmarshalExact is json.Unmarshal into v, refusing first a key that
// differs from one of v's keys only in case, at any depth: encoding/json
// matches keys ignoring case, so such a key would override the known one.
// Unknown keys are still accepted.
func unmarshalExact(raw []byte, v any) error {
	if err := caseVariants(raw, reflect.TypeOf(v), ""); err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func caseVariants(raw []byte, t reflect.Type, path string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil // not an object (or null): json.Unmarshal reports it
	}
	for i := range t.NumField() {
		f := t.Field(i)
		key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		for k, v := range m {
			if k != key && strings.EqualFold(k, key) {
				return fmt.Errorf("key %s%s differs from %s only in case", path, k, key)
			}
			if k == key {
				if err := caseVariants(v, f.Type, path+key+"."); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
