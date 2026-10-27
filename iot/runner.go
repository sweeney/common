// Package iot provides the daemon runtime that drives a sensor profile.
//
// A sensor binary supplies a profile.SensorProfile plus a GPIO reader, an MQTT
// publisher and a status tracker, then calls Run. Run polls the reader,
// debounces via the logic detector, publishes transition events and lifecycle
// messages (STARTUP / ready+scheduled HEARTBEAT / SHUTDOWN) to MQTT, and keeps
// the tracker updated for any HTTP/LED consumers. This is the control flow that
// every sensor daemon shares; the per-binary domain config lives in the profile.
package iot

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sweeney/common/iot/gpio"
	"github.com/sweeney/common/iot/logic"
	"github.com/sweeney/common/iot/mqtt"
	"github.com/sweeney/common/iot/profile"
	"github.com/sweeney/common/iot/status"
)

// Publisher is the MQTT capability the runtime needs: event + lifecycle
// publishing and connection status. Both *mqtt.RealPublisher and
// *mqtt.FakePublisher satisfy it.
type Publisher interface {
	mqtt.EventPublisher
	mqtt.ConnectionStatus
}

// RunConfig wires the runtime. Profile, Reader, Publisher and Tracker are
// required. The injection points default to production behaviour (a real
// ticker from Poll, OS SIGINT/SIGTERM, env-based network info, the std logger),
// so daemons set only Poll; tests override Tick/Signals/Now for determinism.
type RunConfig struct {
	Profile   profile.SensorProfile
	Reader    gpio.ChannelReader
	Publisher Publisher
	Tracker   *status.Tracker
	Debounce  time.Duration
	Heartbeat time.Duration
	Poll      time.Duration

	Now         func() time.Time           // default time.Now
	Tick        <-chan time.Time           // default time.NewTicker(Poll).C
	Signals     <-chan os.Signal           // default SIGINT/SIGTERM
	NetworkInfo func() *status.NetworkInfo // default ReadNetworkInfoFromEnv
	Logger      *log.Logger                // default log.Default()
}

// Run publishes the startup event, then drives the poll/debounce/publish loop
// until a shutdown signal arrives (at which point it publishes a SHUTDOWN event
// and returns nil).
func Run(cfg RunConfig) error {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	netInfo := cfg.NetworkInfo
	if netInfo == nil {
		netInfo = ReadNetworkInfoFromEnv
	}

	sig := cfg.Signals
	if sig == nil {
		sc := make(chan os.Signal, 1)
		signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM)
		sig = sc
	}

	tick := cfg.Tick
	if tick == nil {
		ticker := time.NewTicker(cfg.Poll)
		defer ticker.Stop()
		tick = ticker.C
	}

	// Publish the startup event with a full status snapshot, before the loop.
	if cfg.Tracker != nil {
		if n := netInfo(); n != nil {
			cfg.Tracker.SetNetwork(n)
		}
		snap := cfg.Tracker.Snapshot()
		startup := mqtt.SystemEvent{
			Timestamp:  snap.Now,
			Event:      "STARTUP",
			RawPayload: status.FormatStatusEvent(snap, "STARTUP", ""),
		}
		if err := cfg.Publisher.PublishSystem(startup); err != nil {
			logger.Printf("failed to publish startup event: %v", err)
		} else {
			logger.Printf("published startup event")
		}
	}

	logger.Printf("started: poll=%v debounce=%v heartbeat=%v", cfg.Poll, cfg.Debounce, cfg.Heartbeat)

	return loop(cfg, now, netInfo, logger, tick, sig)
}

func loop(cfg RunConfig, now func() time.Time, netInfo func() *status.NetworkInfo, logger *log.Logger, tick <-chan time.Time, sig <-chan os.Signal) error {
	prof := cfg.Profile
	tracker := cfg.Tracker
	publisher := cfg.Publisher

	startTime := now()
	detector := logic.NewProfileDetector(prof, cfg.Debounce, startTime)
	keys := prof.Keys()
	var prevNet *status.NetworkInfo
	var readyHeartbeatSent bool

	// syncTracker pushes the detector's current state into the tracker for the
	// HTTP/LED consumers.
	syncTracker := func() {
		if tracker == nil {
			return
		}
		tracker.UpdateChannels(detector.CurrentStates(), detector.IsBaselined(), detector.GenericCountsSnapshot())
		tracker.SetMQTTConnected(publisher.IsConnected())
	}

	for {
		select {
		case s := <-sig:
			logger.Printf("received %v, shutting down", s)
			reason := signalName(s)
			event := mqtt.SystemEvent{
				Timestamp: now(),
				Event:     "SHUTDOWN",
				Reason:    reason,
				Retained:  true,
			}
			if tracker != nil {
				tracker.SetMQTTConnected(publisher.IsConnected())
				snap := tracker.Snapshot()
				event.RawPayload = status.FormatStatusEvent(snap, "SHUTDOWN", reason)
			}
			if err := publisher.PublishSystem(event); err != nil {
				logger.Printf("failed to publish shutdown event: %v", err)
			} else {
				logger.Printf("published shutdown event")
			}
			return nil

		case <-tick:
			t := now()
			vals, err := cfg.Reader.ReadChannels()
			if err != nil {
				logger.Printf("gpio read error: %v", err)
				continue
			}

			values := make(map[string]bool, len(keys))
			for i, k := range keys {
				values[k] = vals[i]
			}

			for _, event := range detector.ProcessValues(values, t) {
				logger.Printf("event: %s (%s=%s)", event.Name, event.Key, event.States[event.Key])
				if err := publisher.PublishEvent(event); err != nil {
					logger.Printf("publish error: %v", err)
					// Don't crash on publish failure.
				}
			}

			if !detector.IsBaselined() {
				// Still waiting for baseline.
				continue
			}

			// Fire a heartbeat immediately the first time we're baselined so new
			// MQTT subscribers get real state without waiting for the next
			// scheduled interval.
			if !readyHeartbeatSent {
				readyHeartbeatSent = true
				logger.Printf("baseline established, publishing ready heartbeat")
				hbEvent := mqtt.SystemEvent{Timestamp: t, Event: "HEARTBEAT", Retained: true}
				if tracker != nil {
					if n := netInfo(); n != nil {
						tracker.SetNetwork(n)
					}
					syncTracker()
					snap := tracker.Snapshot()
					snap.Now = t
					hbEvent.RawPayload = status.FormatStatusEvent(snap, "HEARTBEAT", "")
				}
				if err := publisher.PublishSystem(hbEvent); err != nil {
					logger.Printf("ready heartbeat publish error: %v", err)
				}
			}

			// Scheduled heartbeat.
			if hbData := detector.CheckHeartbeat(t, cfg.Heartbeat); hbData != nil {
				mqttUp := "disconnected"
				if publisher.IsConnected() {
					mqttUp = "connected"
				}
				n := netInfo()
				netDesc := "unavailable"
				if n != nil {
					if n.SSID != "" {
						netDesc = fmt.Sprintf("%s/%s (wifi=%s ssid=%s)", n.Status, n.IP, n.WifiStatus, n.SSID)
					} else {
						netDesc = fmt.Sprintf("%s/%s", n.Status, n.IP)
					}
				}
				logger.Printf("heartbeat: uptime=%v mqtt=%s net=%s%s", hbData.Uptime, mqttUp, netDesc, countsSuffix(prof, hbData.Counts))

				// Detect network state changes since the last heartbeat.
				if prevNet != nil && n != nil && prevNet.Status != n.Status {
					logger.Printf("network: status changed since last heartbeat: %s → %s", prevNet.Status, n.Status)
				}
				if prevNet != nil && n != nil && prevNet.WifiStatus != n.WifiStatus {
					logger.Printf("network: wifi status changed since last heartbeat: %s → %s", prevNet.WifiStatus, n.WifiStatus)
				}
				prevNet = n

				hbEvent := mqtt.SystemEvent{Timestamp: hbData.Timestamp, Event: "HEARTBEAT", Retained: true}
				if tracker != nil {
					if nn := netInfo(); nn != nil {
						tracker.SetNetwork(nn)
					}
					syncTracker()
					snap := tracker.Snapshot()
					snap.Now = hbData.Timestamp
					hbEvent.RawPayload = status.FormatStatusEvent(snap, "HEARTBEAT", "")
				}
				if err := publisher.PublishSystem(hbEvent); err != nil {
					logger.Printf("heartbeat publish error: %v", err)
				}
			}

			// Update status tracker for HTTP/LED consumers.
			syncTracker()
		}
	}
}

// countsSuffix renders the per-channel event counts for the heartbeat log line,
// in profile order: " ch_on=1 ch_off=1 hw_on=1 hw_off=0".
func countsSuffix(prof profile.SensorProfile, counts map[string]int) string {
	var sb strings.Builder
	for _, c := range prof.Channels {
		fmt.Fprintf(&sb, " %s_on=%d %s_off=%d", c.Key, counts[c.OnEvent], c.Key, counts[c.OffEvent])
	}
	return sb.String()
}

func signalName(s os.Signal) string {
	switch s {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	default:
		return "UNKNOWN"
	}
}

// pi-helper env var names (written to /run/pi-helper.env on the user's Pis).
const (
	envNetworkType       = "NETWORK_TYPE"
	envNetworkIP         = "NETWORK_IP"
	envNetworkStatus     = "NETWORK_STATUS"
	envNetworkGateway    = "NETWORK_GATEWAY"
	envNetworkWifiStatus = "NETWORK_WIFI_STATUS"
	envNetworkWifiSSID   = "NETWORK_WIFI_SSID"
)

// ReadNetworkInfoFromEnv reads pi-helper's NETWORK_* environment variables into
// a status.NetworkInfo, returning nil when NETWORK_STATUS is unset. It is the
// default RunConfig.NetworkInfo source.
func ReadNetworkInfoFromEnv() *status.NetworkInfo {
	s := os.Getenv(envNetworkStatus)
	if s == "" {
		return nil
	}
	return &status.NetworkInfo{
		Type:       os.Getenv(envNetworkType),
		IP:         os.Getenv(envNetworkIP),
		Status:     s,
		Gateway:    os.Getenv(envNetworkGateway),
		WifiStatus: os.Getenv(envNetworkWifiStatus),
		SSID:       os.Getenv(envNetworkWifiSSID),
	}
}
