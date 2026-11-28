// Package washhouse is washhouse's MQTT wire contract (sweeney/washhouse's
// docs/WIRE.md, which is normative): the payload types, their encoders and
// strict decoders, and the topics. The goldens in testdata pin the bytes;
// washhouse checks every WIRE.md example against this package.
//
// washhouse publishes with it, and statehouse's adapter decodes with it.
//
// Key order is the struct field order. Times are Time (UTC, to the
// millisecond), numbers Num (at most 3 decimals); unknown is nil, which is
// null on the wire, never 0.
package washhouse

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// The payloads' schema names (WIRE.md, Conventions).
const (
	SchemaState  = "net.swee.washhouse.state.v1"
	SchemaTank   = "net.swee.washhouse.tank.v1"
	SchemaEvent  = "net.swee.washhouse.event.v1"
	SchemaStatus = "net.swee.washhouse.status.v1"
)

func join(base, rest string) string { return strings.TrimSuffix(base, "/") + "/" + rest }

// StateTopic is the system state's topic under base (e.g. water/hot).
func StateTopic(base string) string { return join(base, "state") }

// TankTopic is one cylinder's topic.
func TankTopic(base, deviceID string) string { return join(base, "tank/"+deviceID) }

// EventsTopic is the events topic.
func EventsTopic(base string) string { return join(base, "events") }

// StatusTopic is the daemon's status topic.
func StatusTopic(base string) string { return join(base, "status") }

// TimeLayout is every time on the wire: UTC, exactly three decimals.
const TimeLayout = "2006-01-02T15:04:05.000Z"

// Time is a time on the wire.
type Time struct{ time.Time }

// MarshalJSON writes t in UTC, to the millisecond (truncated).
func (t Time) MarshalJSON() ([]byte, error) {
	return []byte(`"` + t.UTC().Format(TimeLayout) + `"`), nil
}

// UnmarshalJSON accepts exactly TimeLayout.
func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	x, err := time.Parse(TimeLayout, s)
	if err != nil {
		return fmt.Errorf("time %q isn't %s", s, TimeLayout)
	}
	t.Time = x
	return nil
}

// Num is a number on the wire: rounded to 3 decimals, without trailing
// zeros. NaN and infinities are refused.
type Num float64

// MarshalJSON rounds x to 3 decimals; a rounded -0 is 0.
func (x Num) MarshalJSON() ([]byte, error) {
	f := float64(x)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, fmt.Errorf("washhouse: %v isn't a finite number", f)
	}
	r, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'f', 3, 64), 64)
	if r == 0 {
		r = 0 // not -0
	}
	return []byte(strconv.FormatFloat(r, 'f', -1, 64)), nil
}

// TankState is a tank's thermal state.
type TankState string

// The tank states.
const (
	StateHeating TankState = "heating"
	StateDrawing TankState = "drawing"
	StateCooling TankState = "cooling"
	StateUnknown TankState = "unknown"
)

// Source is what a heating tank's gain is put down to.
type Source string

// The heat sources.
const (
	SourceHW Source = "hw"
	SourceCH Source = "ch"
)

// Kind is a draw's kind.
type Kind string

// The draw kinds.
const (
	KindTap    Kind = "tap"
	KindShower Kind = "shower"
	KindBath   Kind = "bath"
)

// VolumeMetered is draw_ended's only volume_kind.
const VolumeMetered = "metered"

// Reason is why a draw's heat balance (and, for the masking ones, its
// shares) can't be trusted. A consumer must accept one it doesn't know.
type Reason string

// The reasons (WIRE.md, draw_balanced).
const (
	ReasonHWOn        Reason = "hw_on"
	ReasonGained      Reason = "tank_gained"
	ReasonHWUnknown   Reason = "hw_unknown"
	ReasonSmall       Reason = "small"
	ReasonThermocline Reason = "thermocline_gap"
	ReasonGapUnread   Reason = "gap_unread"
	ReasonInterrupted Reason = "interrupted"
	ReasonSharesEarly Reason = "shares_early"
	ReasonNoLoss      Reason = "no_loss"
	ReasonTankMissing Reason = "tank_missing"
)

// State is the system: {base}/state.
type State struct {
	Schema       string        `json:"schema"`
	Timestamp    Time          `json:"timestamp"`
	StoredKWh    *Num          `json:"stored_kwh"`
	UsableL40    *Num          `json:"usable_l40"`
	UsableL40Low *Num          `json:"usable_l40_low"`
	LowHotWater  *bool         `json:"low_hot_water"`
	Partial      *Partial      `json:"partial" wire:"added"`
	Boiler       Boiler        `json:"boiler"`
	AmbientC     *Num          `json:"ambient_c"`
	ActiveDraw   *ActiveDraw   `json:"active_draw"`
	Meter        Meter         `json:"meter"`
	Tanktemp     Tanktemp      `json:"tanktemp"`
	Tanks        []TankSummary `json:"tanks"`
}

// Partial is the known cylinders' totals while one or more are blind:
// lower bounds on the system's. Missing names the blind ones.
type Partial struct {
	Missing      []string `json:"missing"`
	StoredKWh    Num      `json:"stored_kwh"`
	UsableL40    Num      `json:"usable_l40"`
	UsableL40Low Num      `json:"usable_l40_low"`
}

// Boiler is the programmer: true on, false off, nil unknown.
type Boiler struct {
	HW *bool `json:"hw"`
	CH *bool `json:"ch"`
}

// ActiveDraw is a running draw.
type ActiveDraw struct {
	DrawID      string `json:"draw_id"`
	StartedAt   Time   `json:"started_at"`
	LastLitreAt Time   `json:"last_litre_at"`
	Litres      Num    `json:"litres"`
	FlowLPerMin *Num   `json:"flow_l_min"`
	Kind        Kind   `json:"kind"` // provisional
}

// Meter is the hot-water meter as washhouse last heard it.
type Meter struct {
	Timestamp        *Time           `json:"timestamp"`
	CountingSince    *Time           `json:"counting_since"`
	Count            *uint64         `json:"count"`
	LitresSinceStart *Num            `json:"litres_since_start"`
	ContinuousFlow   *ContinuousFlow `json:"continuous_flow"`
}

// Tanktemp is tanktemp's latest status.
type Tanktemp struct {
	Timestamp *Time   `json:"timestamp"`
	Online    *bool   `json:"online"`
	Version   *string `json:"version"`
}

// TankSummary is one cylinder in the system state.
type TankSummary struct {
	DeviceID   string    `json:"device_id"`
	State      TankState `json:"state"`
	HeatSource *Source   `json:"heat_source"`
	StoredKWh  *Num      `json:"stored_kwh"`
	UsableL40  *Num      `json:"usable_l40"`
	TopC       *Num      `json:"top_c"`
}

// Tank is one cylinder: {base}/tank/{device_id}.
type Tank struct {
	Schema       string    `json:"schema"`
	DeviceID     string    `json:"device_id"`
	Timestamp    Time      `json:"timestamp"`
	SweepAt      *Time     `json:"sweep_at"`
	State        TankState `json:"state"`
	HeatSource   *Source   `json:"heat_source"`
	StoredKWh    *Num      `json:"stored_kwh"`
	MeanC        *Num      `json:"mean_c"`
	TopC         *Num      `json:"top_c"`
	HotL         *Num      `json:"hot_l"`
	UsableL40    *Num      `json:"usable_l40"`
	UsableL40Low *Num      `json:"usable_l40_low"`
	PowerKW      *Num      `json:"power_kw"`
	UAWPerK      *Num      `json:"ua_w_per_k"`
	ProbesOK     *int      `json:"probes_ok"`
	ProbesTotal  *int      `json:"probes_total"`
	Fault        *Fault    `json:"fault" wire:"added"`
}

// FaultReason is why a cylinder is blind. Open: a consumer accepts one
// it doesn't know.
type FaultReason string

// The fault reasons (WIRE.md, Faults).
const (
	FaultProbes          FaultReason = "probes"
	FaultStale           FaultReason = "stale"
	FaultTanktempOffline FaultReason = "tanktemp_offline"
)

// Fault is why a cylinder is blind, since when, and tanktemp's error codes
// in its latest sweep, counted (empty, never nil, on the wire).
type Fault struct {
	Since  Time           `json:"since"`
	Reason FaultReason    `json:"reason"`
	Errors map[string]int `json:"errors"`
}

// EventType names an event.
type EventType string

// The events (WIRE.md, Events).
const (
	EventDrawStarted           EventType = "draw_started"
	EventDrawUpdated           EventType = "draw_updated"
	EventDrawEnded             EventType = "draw_ended"
	EventDrawBalanced          EventType = "draw_balanced"
	EventContinuousFlowStarted EventType = "continuous_flow_started"
	EventContinuousFlowEnded   EventType = "continuous_flow_ended"
	EventHeatingStarted        EventType = "heating_started"
	EventHeatingEnded          EventType = "heating_ended"
	EventTankStateChanged      EventType = "tank_state_changed"
	EventTankLost              EventType = "tank_lost"
	EventTankRestored          EventType = "tank_restored"
)

// Event is one {base}/events message. Exactly one body is set, the one
// its Event names (body).
type Event struct {
	Schema         string          `json:"schema"`
	Event          EventType       `json:"event"`
	Timestamp      Time            `json:"timestamp"`
	ActiveDraw     *ActiveDraw     `json:"active_draw,omitempty"`
	DrawEnded      *DrawEnded      `json:"draw_ended,omitempty"`
	DrawBalanced   *DrawBalanced   `json:"draw_balanced,omitempty"`
	ContinuousFlow *ContinuousFlow `json:"continuous_flow,omitempty"`
	Tank           *TankEvent      `json:"tank,omitempty"`
	TankLost       *TankLost       `json:"tank_lost,omitempty"`
	TankRestored   *TankRestored   `json:"tank_restored,omitempty"`
}

// TankLost is a cylinder blind for the configured time: its fault then.
type TankLost struct {
	DeviceID string         `json:"device_id"`
	Reason   FaultReason    `json:"reason"`
	Errors   map[string]int `json:"errors"`
	Since    Time           `json:"since"`
}

// TankRestored is a lost cylinder estimated again for the configured
// time: when it was lost, and how long it was blind.
type TankRestored struct {
	DeviceID  string `json:"device_id"`
	LostSince Time   `json:"lost_since"`
	DurationS Num    `json:"duration_s"`
}

// DrawEnded is the meter's account of an ended draw.
type DrawEnded struct {
	DrawID      string   `json:"draw_id"`
	StartedAt   Time     `json:"started_at"`
	EndedAt     Time     `json:"ended_at"`
	DurationS   Num      `json:"duration_s"`
	Litres      Num      `json:"litres"`
	VolumeKind  string   `json:"volume_kind"`
	FlowLPerMin *Num     `json:"flow_l_min"`
	Kind        Kind     `json:"kind"`
	MeterFrom   MeterRef `json:"meter_from"`
	MeterTo     MeterRef `json:"meter_to"`
}

// MeterRef is one tanktemp meter message.
type MeterRef struct {
	CountingSince Time   `json:"counting_since"`
	Seq           uint64 `json:"seq"`
}

// DrawBalanced is an ended draw's attribution.
type DrawBalanced struct {
	DrawID              string      `json:"draw_id"`
	EndedAt             Time        `json:"ended_at"`
	Tanks               []TankShare `json:"tanks"`
	OutletPeakC         *Num        `json:"outlet_peak_c"`
	InletDropC          *Num        `json:"inlet_drop_c"`
	EnergyKWh           *Num        `json:"energy_kwh"`
	HeatBalanceL        *Num        `json:"heat_balance_l"`
	HeatBalanceReliable bool        `json:"heat_balance_reliable"`
	Reasons             []Reason    `json:"reasons"`
	MaskedByReheat      bool        `json:"masked_by_reheat"`
}

// TankShare is one tank's part in a draw. Share and Litres are both set or
// both nil (absent on the wire: the split couldn't be measured).
type TankShare struct {
	DeviceID    string `json:"device_id"`
	Share       *Num   `json:"share,omitempty"`
	Litres      *Num   `json:"litres,omitempty"`
	OutletPeakC *Num   `json:"outlet_peak_c"`
	InletDropC  *Num   `json:"inlet_drop_c"`
}

// ContinuousFlow is creep.
type ContinuousFlow struct {
	Since       Time `json:"since"`
	Last        Time `json:"last"`
	Litres      Num  `json:"litres"`
	FlowLPerMin Num  `json:"flow_l_min"`
}

// TankEvent is a change in one tank.
type TankEvent struct {
	DeviceID   string    `json:"device_id"`
	State      TankState `json:"state"`
	Prev       TankState `json:"prev"`
	HeatSource *Source   `json:"heat_source"`
	PowerKW    *Num      `json:"power_kw"`
}

// StatusEvent is a status message's event.
type StatusEvent string

// The status events.
const (
	StatusStartup   StatusEvent = "STARTUP"
	StatusHeartbeat StatusEvent = "HEARTBEAT"
	StatusShutdown  StatusEvent = "SHUTDOWN"
	StatusLWT       StatusEvent = "LWT"
)

// Status is {base}/status. Reason is set on SHUTDOWN only.
type Status struct {
	Schema    string      `json:"schema"`
	Online    bool        `json:"online"`
	Event     StatusEvent `json:"event"`
	Timestamp Time        `json:"timestamp"`
	Version   string      `json:"version"`
	Ready     bool        `json:"ready"`
	UptimeS   int64       `json:"uptime_s"`
	Tanks     []string    `json:"tanks"`
	Reason    *string     `json:"reason,omitempty"`
}

// LWT is the last will, exactly.
func LWT() []byte { return []byte(`{"online":false,"event":"LWT"}`) }

// ErrUnknownEvent is an event this package doesn't know; a consumer
// ignores it (new events are additive).
var ErrUnknownEvent = errors.New("washhouse: unknown event")

// body names the body key each event carries.
var body = map[EventType]string{
	EventDrawStarted:           "active_draw",
	EventDrawUpdated:           "active_draw",
	EventDrawEnded:             "draw_ended",
	EventDrawBalanced:          "draw_balanced",
	EventContinuousFlowStarted: "continuous_flow",
	EventContinuousFlowEnded:   "continuous_flow",
	EventHeatingStarted:        "tank",
	EventHeatingEnded:          "tank",
	EventTankStateChanged:      "tank",
	EventTankLost:              "tank_lost",
	EventTankRestored:          "tank_restored",
}

// bodies lists the bodies e has set, by key.
func (e Event) bodies() []string {
	var out []string
	if e.ActiveDraw != nil {
		out = append(out, "active_draw")
	}
	if e.DrawEnded != nil {
		out = append(out, "draw_ended")
	}
	if e.DrawBalanced != nil {
		out = append(out, "draw_balanced")
	}
	if e.ContinuousFlow != nil {
		out = append(out, "continuous_flow")
	}
	if e.Tank != nil {
		out = append(out, "tank")
	}
	if e.TankLost != nil {
		out = append(out, "tank_lost")
	}
	if e.TankRestored != nil {
		out = append(out, "tank_restored")
	}
	return out
}

// check says whether e is well formed: a known event with exactly its
// body, and shares and litres set together.
func (e Event) check() error {
	want, ok := body[e.Event]
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknownEvent, e.Event)
	}
	if got := e.bodies(); len(got) != 1 || got[0] != want {
		return fmt.Errorf("washhouse: %s carries %v, want exactly %s", e.Event, got, want)
	}
	if d := e.DrawBalanced; d != nil {
		for _, t := range d.Tanks {
			if (t.Share == nil) != (t.Litres == nil) {
				return fmt.Errorf("washhouse: draw_balanced tank %s has share or litres without the other", t.DeviceID)
			}
		}
	}
	return nil
}
