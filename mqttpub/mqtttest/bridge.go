package mqtttest

import (
	"strings"
	"testing"
)

// Substitution is one change StartBridged makes to a mirrored config.
type Substitution struct{ From, To string }

// StartBridged runs conf, a mirror of a production local-broker and bridge
// config, as a local broker listening on addr (host:port), and returns it.
//
// The config runs as shipped except for subs, applied in order, each
// replacing the first occurrence of From.  They are what cannot be the same
// off the host: the listener moved to addr's port, password-protected
// listeners dropped, the bridge's address pointed at an uplink Broker (or a
// Proxy in front of one), a shorter restart_timeout.  Each From must be in
// conf or the test fails: a mirror that has drifted from its substitutions
// would otherwise run with the production values left in.
//
// persistence false is appended: a service's mirror relies on the
// distribution's main mosquitto.conf for persistence, which is not here,
// and the retained store must not outlive a test.
func StartBridged(t testing.TB, conf, addr string, subs ...Substitution) *Broker {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(conf, sub.From) {
			t.Fatalf("bridge config no longer contains %q; update the substitutions", sub.From)
		}
		conf = strings.Replace(conf, sub.From, sub.To, 1)
	}
	return StartBrokerConf(t, conf+"\npersistence false\n", addr)
}
