package mqtt

import (
	"fmt"
	"log"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/profile"
)

// RealPublisher publishes to an actual MQTT broker.
type RealPublisher struct {
	client         paho.Client
	prof           profile.SensorProfile // drives event serialization + topics
	topic          string                // event topic (== prof.EventTopic)
	systemTopic    string                // system topic (== prof.SystemTopic)
	clientID       string
	mu             sync.Mutex
	connected      bool
	everConnected  bool
	buf            *ringBuffer
	connectedSince time.Time // when the current connection was established
	disconnectedAt time.Time // when the connection was last lost
	reconnectCount int       // consecutive reconnect attempts since last connection
}

// buildClientOptions constructs the Paho ClientOptions for the given broker.
// Extracted so tests can inspect the configuration without connecting.
func buildClientOptions(broker string, p *RealPublisher, willPayload []byte) *paho.ClientOptions {
	clientID := p.clientID
	if clientID == "" {
		clientID = "iot-sensor"
	}
	opts := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID(clientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetMaxReconnectInterval(30 * time.Second).
		SetConnectionLostHandler(p.onConnectionLost).
		SetReconnectingHandler(p.onReconnecting).
		SetOnConnectHandler(p.onConnect)

	if willPayload != nil {
		opts.SetWill(p.systemTopic, string(willPayload), 1, true)
	}

	return opts
}

// NewRealPublisherForProfile creates a publisher that connects to the given
// broker asynchronously, using the profile's event/system topics. It returns
// immediately — Paho retries in the background.
func NewRealPublisherForProfile(broker string, prof profile.SensorProfile, clientID string) *RealPublisher {
	willPayload, err := FormatSystemPayload(SystemEvent{
		Timestamp: time.Now(),
		Event:     "SHUTDOWN",
		Reason:    "MQTT_DISCONNECT",
		Source:    "last_will",
	})
	if err != nil {
		// FormatSystemPayload only fails on json.Marshal of a known struct —
		// unreachable in practice, but log and continue without a will.
		log.Printf("mqtt: failed to format will payload: %v", err)
	}

	p := &RealPublisher{
		prof:        prof,
		topic:       prof.EventTopic,
		systemTopic: prof.SystemTopic,
		clientID:    clientID,
		buf:         newRingBuffer(100),
	}

	p.client = paho.NewClient(buildClientOptions(broker, p, willPayload))

	// Non-blocking connect — Paho retries via ConnectRetry.
	p.client.Connect()

	return p
}

func (p *RealPublisher) onConnectionLost(_ paho.Client, err error) {
	p.mu.Lock()
	uptime := time.Since(p.connectedSince)
	p.connected = false
	p.disconnectedAt = time.Now()
	p.reconnectCount = 0
	p.mu.Unlock()
	log.Printf("mqtt: connection lost after %v: %v", uptime.Truncate(time.Second), err)
}

func (p *RealPublisher) onReconnecting(_ paho.Client, _ *paho.ClientOptions) {
	p.mu.Lock()
	p.reconnectCount++
	n := p.reconnectCount
	disconnectedFor := time.Since(p.disconnectedAt)
	p.mu.Unlock()
	log.Printf("mqtt: reconnecting to broker (attempt %d, disconnected %v)", n, disconnectedFor.Truncate(time.Second))
}

func (p *RealPublisher) onConnect(_ paho.Client) {
	p.mu.Lock()
	wasEverConnected := p.everConnected
	disconnectedFor := time.Duration(0)
	if wasEverConnected && !p.disconnectedAt.IsZero() {
		disconnectedFor = time.Since(p.disconnectedAt)
	}
	p.connected = true
	p.everConnected = true
	p.connectedSince = time.Now()
	p.reconnectCount = 0
	msgs := p.buf.drainAll()
	p.mu.Unlock()

	n := 0
	for _, msg := range msgs {
		token := p.client.Publish(msg.topic, msg.qos, msg.retained, msg.payload)
		if !token.WaitTimeout(5 * time.Second) {
			log.Printf("mqtt: replay timeout for buffered message")
			continue
		}
		if err := token.Error(); err != nil {
			log.Printf("mqtt: replay error: %v", err)
			continue
		}
		n++
	}

	if len(msgs) > 0 {
		log.Printf("mqtt: connected to broker, replayed %d/%d buffered events", n, len(msgs))
	} else if wasEverConnected {
		log.Printf("mqtt: reconnected to broker (was disconnected %v)", disconnectedFor.Truncate(time.Second))
	} else {
		log.Printf("mqtt: connected to broker")
	}

	// On reconnect (not first connect), publish a RECONNECTED event
	// to replace the stale LWT on the broker.
	if wasEverConnected {
		reconnectPayload, err := FormatSystemPayload(SystemEvent{
			Timestamp: time.Now(),
			Event:     "RECONNECTED",
		})
		if err != nil {
			log.Printf("mqtt: failed to format RECONNECTED payload: %v", err)
			return
		}
		token := p.client.Publish(p.systemTopic, 1, true, reconnectPayload)
		if !token.WaitTimeout(5 * time.Second) {
			log.Printf("mqtt: RECONNECTED publish timeout")
		} else if err := token.Error(); err != nil {
			log.Printf("mqtt: RECONNECTED publish error: %v", err)
		}
	}
}

// IsConnected reports whether the MQTT client is currently connected.
func (p *RealPublisher) IsConnected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected
}

// PublishEvent sends a generic channel-keyed event to the MQTT broker, using
// the publisher's profile for serialization (root key, channel order).
func (p *RealPublisher) PublishEvent(event logic.GenericEvent) error {
	payload := FormatEventPayload(p.prof, event.Timestamp, event.Name, event.States)
	return p.publishEventPayload(payload)
}

// publishEventPayload buffers (if disconnected) or publishes a pre-formatted
// event payload at QoS 0, not retained.
func (p *RealPublisher) publishEventPayload(payload []byte) error {
	topic := p.topic
	p.mu.Lock()
	if !p.connected {
		p.buf.push(bufferedMsg{topic: topic, payload: payload, qos: 0, retained: false})
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// QoS 0 (at-most-once), not retained
	token := p.client.Publish(topic, 0, false, payload)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("publish timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	return nil
}

// PublishSystem sends a system lifecycle event to the MQTT broker.
func (p *RealPublisher) PublishSystem(event SystemEvent) error {
	payload, err := FormatSystemPayload(event)
	if err != nil {
		return fmt.Errorf("format system payload: %w", err)
	}

	topic := p.systemTopic
	p.mu.Lock()
	if !p.connected {
		p.buf.push(bufferedMsg{topic: topic, payload: payload, qos: 1, retained: event.Retained})
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// QoS 1 (at-least-once) for system events - we want to ensure delivery
	token := p.client.Publish(topic, 1, event.Retained, payload)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("publish system timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("publish system: %w", err)
	}

	return nil
}

// Close disconnects from the broker.
func (p *RealPublisher) Close() error {
	p.mu.Lock()
	p.connected = false
	p.mu.Unlock()
	p.client.Disconnect(1000) // 1 second timeout
	return nil
}
