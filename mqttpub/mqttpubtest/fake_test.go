package mqttpubtest_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/sweeney/common/mqttpub"
	"github.com/sweeney/common/mqttpub/mqttpubtest"
)

var _ mqttpub.Publisher = (*mqttpubtest.Fake)(nil)

func TestFakePublisher_Find(t *testing.T) {
	fp := &mqttpubtest.Fake{}
	fp.Publish(mqttpub.Message{Topic: "a/b", Payload: "v1"}) //nolint:errcheck
	fp.Publish(mqttpub.Message{Topic: "c/d", Payload: "v2"}) //nolint:errcheck

	msg, ok := fp.Find("c/d")
	if !ok {
		t.Fatal("Find should return true for existing topic")
	}
	if msg.Payload != "v2" {
		t.Errorf("Find payload = %q, want %q", msg.Payload, "v2")
	}

	_, ok = fp.Find("missing")
	if ok {
		t.Error("Find should return false for missing topic")
	}
}

func TestFakePublisher_Retained(t *testing.T) {
	fp := &mqttpubtest.Fake{}
	if _, ok := fp.Retained("t"); ok {
		t.Fatal("nothing published: Retained should report false")
	}

	fp.Publish(mqttpub.Message{Topic: "t", Payload: "v1", Retained: true})    //nolint:errcheck
	fp.Publish(mqttpub.Message{Topic: "t", Payload: "v2", Retained: true})    //nolint:errcheck
	fp.Publish(mqttpub.Message{Topic: "t", Payload: "live"})                  //nolint:errcheck
	fp.Publish(mqttpub.Message{Topic: "u", Payload: "other", Retained: true}) //nolint:errcheck
	msg, ok := fp.Retained("t")
	if !ok || msg.Payload != "v2" {
		t.Errorf("Retained = %q, %v; want the last retained payload %q", msg.Payload, ok, "v2")
	}

	// An empty retained payload deletes the retained message.
	fp.Publish(mqttpub.Message{Topic: "t", Payload: "", Retained: true}) //nolint:errcheck
	if msg, ok := fp.Retained("t"); ok {
		t.Errorf("empty retained payload should delete the retained message, got %q", msg.Payload)
	}
	if _, ok := fp.Retained("u"); !ok {
		t.Error("deleting one topic must not affect another")
	}
}

// ups-mqtt's version of this test drove its PublishAll; here the fake is
// called directly.
func TestFakePublisher_PublishError(t *testing.T) {
	want := errors.New("broker down")
	fp := &mqttpubtest.Fake{PublishError: want}
	if err := fp.Publish(mqttpub.Message{Topic: "a"}); !errors.Is(err, want) {
		t.Fatalf("Publish() = %v, want %v", err, want)
	}
	if len(fp.Messages) != 0 {
		t.Errorf("a failed publish was recorded: %+v", fp.Messages)
	}
	if strings.Join(fp.Failed, ",") != "a" {
		t.Errorf("Failed = %v, want [a]", fp.Failed)
	}
}

// FailTopic fails only the topics it selects: a partially degraded link.
func TestFakePublisher_FailTopic(t *testing.T) {
	fp := &mqttpubtest.Fake{FailTopic: func(tp string) bool { return tp == "bad" }}
	if err := fp.Publish(mqttpub.Message{Topic: "good"}); err != nil {
		t.Errorf("unselected topic failed: %v", err)
	}
	err := fp.Publish(mqttpub.Message{Topic: "bad"})
	if err == nil || !strings.Contains(err.Error(), `"bad"`) {
		t.Errorf("Publish(bad) = %v, want an error naming the topic", err)
	}
	if len(fp.Messages) != 1 || fp.Messages[0].Topic != "good" {
		t.Errorf("Messages = %+v, want only good", fp.Messages)
	}
	if strings.Join(fp.Failed, ",") != "bad" {
		t.Errorf("Failed = %v, want [bad]", fp.Failed)
	}

	// With PublishError set too, a selected topic fails with it.
	want := errors.New("broker down")
	fp.PublishError = want
	if err := fp.Publish(mqttpub.Message{Topic: "bad"}); !errors.Is(err, want) {
		t.Errorf("Publish(bad) = %v, want %v", err, want)
	}
	if err := fp.Publish(mqttpub.Message{Topic: "good"}); err != nil {
		t.Errorf("unselected topic failed with PublishError set: %v", err)
	}
}

// IndexOf is how publish ordering is asserted.
func TestFakePublisher_IndexOf(t *testing.T) {
	fp := &mqttpubtest.Fake{}
	fp.Publish(mqttpub.Message{Topic: "a"}) //nolint:errcheck
	fp.Publish(mqttpub.Message{Topic: "b"}) //nolint:errcheck
	fp.Publish(mqttpub.Message{Topic: "a"}) //nolint:errcheck
	if got := fp.IndexOf("b"); got != 1 {
		t.Errorf("IndexOf(b) = %d, want 1", got)
	}
	if got := fp.IndexOf("a"); got != 0 {
		t.Errorf("IndexOf(a) = %d, want the first, 0", got)
	}
	if got := fp.IndexOf("missing"); got != -1 {
		t.Errorf("IndexOf(missing) = %d, want -1", got)
	}
}

func TestFakePublisher_All(t *testing.T) {
	fp := &mqttpubtest.Fake{}
	fp.Publish(mqttpub.Message{Topic: "a", Payload: "1"}) //nolint:errcheck
	fp.Publish(mqttpub.Message{Topic: "b", Payload: "x"}) //nolint:errcheck
	fp.Publish(mqttpub.Message{Topic: "a", Payload: "2"}) //nolint:errcheck
	got := fp.All("a")
	if len(got) != 2 || got[0].Payload != "1" || got[1].Payload != "2" {
		t.Errorf("All(a) = %+v, want payloads 1 then 2", got)
	}
	if got := fp.All("missing"); len(got) != 0 {
		t.Errorf("All(missing) = %+v, want none", got)
	}
}

// Connected stands in for MQTTPublisher.Connected in a health check.  A
// fresh fake is connected.
func TestFakePublisher_Connected(t *testing.T) {
	fp := &mqttpubtest.Fake{}
	if !fp.Connected() {
		t.Error("a fresh fake should be connected")
	}
	fp.Disconnected = true
	if fp.Connected() {
		t.Error("Connected() = true with Disconnected set")
	}
}

func TestFakePublisher_Close(t *testing.T) {
	fp := &mqttpubtest.Fake{}
	if fp.Closed {
		t.Fatal("should not be closed initially")
	}
	fp.Close() //nolint:errcheck
	if !fp.Closed {
		t.Error("should be closed after Close()")
	}
}

func TestFakePublisher_Reset(t *testing.T) {
	fp := &mqttpubtest.Fake{}
	fp.Publish(mqttpub.Message{Topic: "x", Payload: "y"}) //nolint:errcheck
	fp.Closed = true
	fp.Reset()

	if len(fp.Messages) != 0 {
		t.Error("Reset should clear Messages")
	}
	if fp.Closed {
		t.Error("Reset should set Closed=false")
	}
}

// Reset clears the error injection and the connection toggle too.
func TestFakePublisher_ResetClearsInjection(t *testing.T) {
	fp := &mqttpubtest.Fake{
		PublishError: errors.New("down"),
		FailTopic:    func(string) bool { return true },
		Disconnected: true,
	}
	fp.Publish(mqttpub.Message{Topic: "x"}) //nolint:errcheck
	fp.Reset()
	if fp.PublishError != nil || fp.FailTopic != nil || fp.Failed != nil || fp.Disconnected {
		t.Errorf("Reset left injection in place: %+v", fp)
	}
	if err := fp.Publish(mqttpub.Message{Topic: "x"}); err != nil {
		t.Errorf("Publish after Reset = %v", err)
	}
}
