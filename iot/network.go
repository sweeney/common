package iot

import (
	"bufio"
	"bytes"
	"os"
	"strings"

	"github.com/sweeney/common/iot/status"
)

// pi-helper's variable names, written to /run/pi-helper.env on the user's Pis.
const (
	envNetworkType       = "NETWORK_TYPE"
	envNetworkIP         = "NETWORK_IP"
	envNetworkStatus     = "NETWORK_STATUS"
	envNetworkGateway    = "NETWORK_GATEWAY"
	envNetworkWifiStatus = "NETWORK_WIFI_STATUS"
	envNetworkWifiSSID   = "NETWORK_WIFI_SSID"
)

// piHelperEnvPath is the file ReadNetworkInfo reads. pi-helper rewrites it
// whenever the network state changes. Tests point it elsewhere.
var piHelperEnvPath = "/run/pi-helper.env"

// ReadNetworkInfo is the default RunConfig.NetworkInfo. It reads pi-helper's
// env file afresh on every call, so the status reflects the network as it is
// now. systemd's EnvironmentFile= is read only once, at start, and at boot that
// is often before the WiFi is up. Status pages call it per request too, since
// the tracker's copy only changes at heartbeats. Each call is one small read
// of a tmpfs file.
//
// If the file cannot be read, it falls back to the process environment
// (ReadNetworkInfoFromEnv). A readable file is authoritative: if it has no
// NETWORK_STATUS the result is nil, whatever the environment says.
//
// It relies on pi-helper replacing the file atomically (temp file + rename),
// which it does, so a read never sees a half-written file.
func ReadNetworkInfo() *status.NetworkInfo {
	data, err := os.ReadFile(piHelperEnvPath)
	if err != nil {
		return ReadNetworkInfoFromEnv()
	}
	return parsePiHelperEnv(data)
}

// parsePiHelperEnv parses pi-helper's env file, returning nil when it has no
// NETWORK_STATUS. pi-helper writes one unquoted KEY=value per line, so each
// value is everything after the first "=", taken verbatim: an SSID may contain
// spaces, quotes or "=". Lines without "=" and unknown keys are ignored.
func parsePiHelperEnv(data []byte) *status.NetworkInfo {
	var n status.NetworkInfo
	dst := map[string]*string{
		envNetworkType:       &n.Type,
		envNetworkIP:         &n.IP,
		envNetworkStatus:     &n.Status,
		envNetworkGateway:    &n.Gateway,
		envNetworkWifiStatus: &n.WifiStatus,
		envNetworkWifiSSID:   &n.SSID,
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if p := dst[k]; ok && p != nil {
			*p = v
		}
	}
	if n.Status == "" {
		return nil
	}
	return &n
}

// ReadNetworkInfoFromEnv reads pi-helper's NETWORK_* environment variables into
// a status.NetworkInfo, returning nil when NETWORK_STATUS is unset. These are
// fixed for the life of the process, so the default source only falls back to
// them when pi-helper's env file cannot be read.
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

// netStatus and netWifiStatus name a heartbeat's network state for the
// "changed since last heartbeat" log lines; no info at all is "unavailable".
func netStatus(n *status.NetworkInfo) string {
	if n == nil {
		return "unavailable"
	}
	return n.Status
}

func netWifiStatus(n *status.NetworkInfo) string {
	if n == nil {
		return "unavailable"
	}
	return n.WifiStatus
}
