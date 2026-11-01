package iot

import (
	"bufio"
	"bytes"
	"os"
	"strings"

	"github.com/sweeney/common/iot/status"
)

// PiHelperEnvPath is where pi-helper writes the Pi's current network state,
// rewriting it whenever that state changes.
const PiHelperEnvPath = "/run/pi-helper.env"

// piHelperEnvPath is the file ReadNetworkInfo reads; tests point it elsewhere.
var piHelperEnvPath = PiHelperEnvPath

// ReadNetworkInfo is the default RunConfig.NetworkInfo source. It reads
// pi-helper's env file afresh on every call, so the status reflects the
// network as it is now, not as it was when the process started: systemd's
// EnvironmentFile= is read once at start, and at boot that is often before
// the WiFi is up. If the file cannot be read it falls back to the process
// environment (ReadNetworkInfoFromEnv). It returns nil when neither source
// has NETWORK_STATUS.
func ReadNetworkInfo() *status.NetworkInfo {
	data, err := os.ReadFile(piHelperEnvPath)
	if err != nil {
		return ReadNetworkInfoFromEnv()
	}
	return ParsePiHelperEnv(data)
}

// ParsePiHelperEnv parses pi-helper's KEY=VALUE env file into a
// status.NetworkInfo, returning nil when it has no NETWORK_STATUS. Blank lines
// and # comments are skipped and surrounding quotes are trimmed from values,
// as in health/sysinfo.PiHelperEnv.
func ParsePiHelperEnv(data []byte) *status.NetworkInfo {
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
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		if p := dst[k]; p != nil {
			*p = strings.Trim(v, `"'`)
		}
	}
	if n.Status == "" {
		return nil
	}
	return &n
}
