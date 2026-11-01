package mqtttest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// publish sends one QoS 1 message to brokerURL and waits for its ack.
func publish(t *testing.T, brokerURL, topic, payload string, retained bool) {
	t.Helper()
	c := mqtt.NewClient(mqtt.NewClientOptions().AddBroker(brokerURL).
		SetClientID(fmt.Sprintf("pub-%d", clientSeq.Add(1))))
	if tok := c.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatalf("connecting to %s: %v", brokerURL, tok.Error())
	}
	defer c.Disconnect(100)
	if tok := c.Publish(topic, 1, retained, payload); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatalf("publishing to %s: %v", topic, tok.Error())
	}
}

// waitFor polls until cond holds, or fails after within.
func waitFor(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBroker_RecordAndRetained(t *testing.T) {
	b := StartBroker(t, "")
	if !strings.HasPrefix(b.URL(), "tcp://127.0.0.1:") {
		t.Errorf("URL() = %q", b.URL())
	}
	rec := Record(t, b.URL(), "test/#")

	publish(t, b.URL(), "test/a", "1", false)
	publish(t, b.URL(), "test/b", "x", true)
	publish(t, b.URL(), "test/a", "2", false)
	publish(t, b.URL(), "other", "ignored", false)
	waitFor(t, 5*time.Second, "three messages", func() bool { return len(rec.Messages()) == 3 })
	rec.WaitQuiet(50*time.Millisecond, time.Second)

	on := rec.On("test/a")
	if len(on) != 2 || on[0].Payload != "1" || on[1].Payload != "2" {
		t.Errorf("On(test/a) = %+v, want 1 then 2", on)
	}
	if got := rec.Messages(); len(got) != 3 {
		t.Errorf("recorded %d messages, want 3 (other is outside the filter)", len(got))
	}

	if p, ok := Retained(t, b.URL(), "test/b"); !ok || p != "x" {
		t.Errorf("Retained(test/b) = %q, %v; want x", p, ok)
	}
	if _, ok := Retained(t, b.URL(), "test/a"); ok {
		t.Error("Retained(test/a) found a message that was never retained")
	}
}

// Restart keeps the config; with persistence off the retained store is
// emptied, which is what a test restarting the broker relies on.
func TestBroker_Restart(t *testing.T) {
	b := StartBroker(t, "")
	publish(t, b.URL(), "test/r", "v", true)
	b.Restart()
	if _, ok := Retained(t, b.URL(), "test/r"); ok {
		t.Error("retained message survived a restart without persistence")
	}
	publish(t, b.URL(), "test/r", "w", true)
	if p, ok := Retained(t, b.URL(), "test/r"); !ok || p != "w" {
		t.Errorf("after restart: Retained = %q, %v; want w", p, ok)
	}
	if b.Logs() == "" {
		t.Error("no broker logs captured")
	}
	b.Stop()
	b.Stop() // safe twice
}

func TestStartBridged_ForwardsToUplink(t *testing.T) {
	uplink := StartBroker(t, "")
	conf, err := os.ReadFile(filepath.Join("testdata", "bridge.conf"))
	if err != nil {
		t.Fatal(err)
	}
	port := FreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	local := StartBridged(t, string(conf), addr,
		Substitution{"listener 1883 127.0.0.1", fmt.Sprintf("listener %d 127.0.0.1", port)},
		Substitution{"address uplink.example:1883", "address " + uplink.Addr},
		Substitution{"restart_timeout 5 30", "restart_timeout 1 2"},
	)
	if local.Addr != addr {
		t.Errorf("Addr = %q, want %q", local.Addr, addr)
	}
	if !strings.Contains(confText(t, local), "persistence false") {
		t.Error("bridged broker runs with persistence")
	}

	// The bridge announces itself on the uplink once connected.
	waitFor(t, 10*time.Second, "the bridge notification", func() bool {
		p, ok := Retained(t, uplink.URL(), "sites/test/bridge")
		return ok && p == "1"
	})
	rec := Record(t, uplink.URL(), "test/#")
	publish(t, local.URL(), "test/x", "over the bridge", false)
	waitFor(t, 5*time.Second, "the message on the uplink", func() bool { return len(rec.On("test/x")) == 1 })
}

func confText(t *testing.T, b *Broker) string {
	t.Helper()
	c, err := os.ReadFile(b.Conf)
	if err != nil {
		t.Fatal(err)
	}
	return string(c)
}

// fatalTB records a Fatal and stops the goroutine, as testing.T does.
type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper() {}
func (f *fatalTB) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// A mirror that has drifted from the substitutions must fail, not run
// with the production values left in.
func TestStartBridged_FailsOnStaleSubstitution(t *testing.T) {
	tb := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		StartBridged(tb, "listener 1883 127.0.0.1\n", "127.0.0.1:1",
			Substitution{"address uplink.example:1883", "address 127.0.0.1:2"})
	}()
	<-done
	if !strings.Contains(tb.msg, `no longer contains "address uplink.example:1883"`) {
		t.Errorf("Fatal = %q, want one naming the missing text", tb.msg)
	}
}

func TestReadLines(t *testing.T) {
	got, err := ReadLines(filepath.Join("testdata", "lines.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "first|second|third" {
		t.Errorf("ReadLines = %q, want first, second, third", got)
	}
	if _, err := ReadLines(filepath.Join("testdata", "missing.txt")); err == nil {
		t.Error("ReadLines of a missing file: no error")
	}
}
