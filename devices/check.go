package devices

import (
	"fmt"
	"sort"
	"strings"
)

// Check reports problems with a document. It is advisory: it never refuses a
// document and never changes the map. It folds legacy identity fields itself,
// so a map that has not been through Normalise reads the same. A missing room
// or floor is not a problem: it means unknown. Problems are ordered by device
// id, then code.
func Check(m map[string]Device) []Problem {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var problems []Problem
	add := func(id, code, format string, args ...any) {
		problems = append(problems, Problem{ID: id, Code: code, Message: fmt.Sprintf(format, args...)})
	}
	// claimed maps scheme:lower(primary) and scheme:lower(display) to the
	// first id that used it. statehouse matches devices by both, so a second
	// claimant makes its choice arbitrary.
	claimed := map[string]string{}
	for _, id := range ids {
		d := m[id]
		d.Normalise()

		if d.Room != "" && !roomShaped(d.Room) {
			add(id, CodeRoomMalformed, "room %q is not a \"<floor>.<room>\" id", d.Room)
		} else if d.Room != "" && d.Floor != "" {
			if floor, _, _ := strings.Cut(d.Room, "."); floor != d.Floor {
				add(id, CodeFloorMismatch, "room %q is not on floor %q", d.Room, d.Floor)
			}
		}
		if d.Covers != "" && d.Covers != CoverageHouse && !roomShaped(d.Covers) {
			add(id, CodeCoversUnresolved, "covers %q is neither %q nor a room id", d.Covers, CoverageHouse)
		}
		if d.Scheme == "" || (d.Primary == "" && d.Display == "") {
			add(id, CodeIdentityMissing, "no scheme with a primary or display, so no service can match it")
		}
		for _, name := range []struct{ kind, value string }{{"primary", d.Primary}, {"display", d.Display}} {
			if d.Scheme == "" || name.value == "" {
				continue
			}
			key := d.Scheme + ":" + strings.ToLower(name.value)
			if other, ok := claimed[key]; ok && other != id {
				add(id, CodeDuplicateIdentity, "%s %s %q is also %q's", d.Scheme, name.kind, name.value, other)
				continue
			}
			claimed[key] = id
		}
		if d.Location != "" {
			add(id, CodeLegacyLocation, "still carries location %q; use room and covers", d.Location)
		}
	}
	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].ID != problems[j].ID {
			return problems[i].ID < problems[j].ID
		}
		return problems[i].Code < problems[j].Code
	})
	return problems
}

// roomShaped reports whether s has the shape of a floorplan room id,
// "<floor>.<room>".
func roomShaped(s string) bool {
	floor, room, ok := strings.Cut(s, ".")
	return ok && floor != "" && room != "" && !strings.ContainsAny(s, " \t")
}
