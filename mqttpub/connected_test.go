package mqttpub

import "testing"

// Connected is what a /healthz reports, so it follows the same signal as
// Publish: the connection actually being open.  paho's IsConnected() also
// says true while it auto-reconnects, when nothing can be sent.
func TestConnected_FollowsOpenConnection(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    *fakeClient
		want bool
	}{
		{"open", &fakeClient{connected: true}, true},
		{"closed", &fakeClient{}, false},
		{"reconnecting", &fakeClient{reconnecting: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &MQTTPublisher{client: tc.c}
			if got := p.Connected(); got != tc.want {
				t.Errorf("Connected() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Before Connect there is no connection.
func TestConnected_FalseBeforeConnect(t *testing.T) {
	p, err := NewMQTTPublisher(Options{Broker: "tcp://127.0.0.1:1", ClientID: "test"}, "t", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if p.Connected() {
		t.Error("Connected() = true before Connect")
	}
}
