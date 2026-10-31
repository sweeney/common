// Package mqttpub publishes MQTT messages for the fleet's daemons: a
// Publisher interface, MQTTPublisher (paho, with its own capped-backoff
// reconnect instead of paho's auto-reconnect) and the Message they send.
//
// Topics and payload shapes belong to each service.  The one exception is
// OfflinePayload, the {"online":false} last will that several services share.
//
// Extracted from ups-mqtt's internal/publisher; netmon carried a copy.
package mqttpub

// Message is a single MQTT publish request.
type Message struct {
	Topic    string
	Payload  string
	Retained bool
	// QOS is chosen per message, not per client: telemetry and events are
	// published at different levels.  Behind a local broker bridged to a
	// remote one, the bridge queues QoS 1 messages while its link is down,
	// up to a cap, and drops the newest once the cap is reached.  Telemetry
	// at QoS 1 would fill that queue with readings the next poll supersedes,
	// and the event it exists to report would be the message dropped.  So
	// telemetry goes at QoS 0, events (an outage, its clear, the offline
	// announcement) at QoS 1.
	QOS byte
}

// Publisher is the minimal interface a service uses to send MQTT messages.
// MQTTPublisher and mqttpubtest.Fake both implement it.
type Publisher interface {
	Publish(msg Message) error
	Close() error
}

// Options are the connection settings.  Timings are in Tuning.
type Options struct {
	Broker   string // e.g. tcp://broker.example:1883 or ssl://broker.example:8883
	ClientID string
	// Username and Password are sent only when Username is set.
	Username string
	Password string
	// TLSCACert, when set, is a PEM file trusted as an additional CA.
	TLSCACert string
	// WillQOS is the QoS of the last will.  The will is the broker
	// announcing this client dead, an event, so it is normally the event
	// QoS (1).
	WillQOS byte
}
