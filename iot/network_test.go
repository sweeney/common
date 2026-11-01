package iot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sweeney/common/iot/status"
)

// usePiHelperFile points ReadNetworkInfo at path for the rest of the test.
func usePiHelperFile(t *testing.T, path string) {
	t.Helper()
	old := piHelperEnvPath
	piHelperEnvPath = path
	t.Cleanup(func() { piHelperEnvPath = old })
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// clearNetworkEnv blanks every NETWORK_* variable for the rest of the test.
func clearNetworkEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{envNetworkType, envNetworkIP, envNetworkStatus, envNetworkGateway, envNetworkWifiStatus, envNetworkWifiSSID} {
		t.Setenv(k, "")
	}
}

const connectedEnvFile = `NETWORK_GATEWAY=192.168.1.1
NETWORK_HOST=pi
NETWORK_IP=192.168.1.50
NETWORK_STATUS=connected
NETWORK_TYPE=wifi
NETWORK_WIFI_SSID=HomeNet
NETWORK_WIFI_STATUS=connected
`

var connectedInfo = status.NetworkInfo{
	Type: "wifi", IP: "192.168.1.50", Status: "connected",
	Gateway: "192.168.1.1", WifiStatus: "connected", SSID: "HomeNet",
}

func TestParsePiHelperEnv(t *testing.T) {
	got := ParsePiHelperEnv([]byte(connectedEnvFile))
	if got == nil || *got != connectedInfo {
		t.Errorf("got %+v, want %+v", got, connectedInfo)
	}
}

func TestParsePiHelperEnvSkipsCommentsBlanksAndQuotes(t *testing.T) {
	data := "# written by pi-helper\n\n  NETWORK_STATUS=\"connected\"  \nNETWORK_IP='10.0.0.2'\nnot a pair\nOTHER=x\n"
	got := ParsePiHelperEnv([]byte(data))
	want := status.NetworkInfo{Status: "connected", IP: "10.0.0.2"}
	if got == nil || *got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParsePiHelperEnvNoStatusIsNil(t *testing.T) {
	for _, data := range []string{"", "NETWORK_IP=10.0.0.2\n", "NETWORK_STATUS=\n"} {
		if got := ParsePiHelperEnv([]byte(data)); got != nil {
			t.Errorf("ParsePiHelperEnv(%q) = %+v, want nil", data, got)
		}
	}
}

// The regression: the network was read once, at process start, from an
// environment systemd had copied from the file before the WiFi was up, and
// stayed "disconnected" for the life of the process. ReadNetworkInfo must see
// each rewrite of the file, and prefer the file to a stale environment.
func TestReadNetworkInfoRereadsFileOnEveryCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pi-helper.env")
	usePiHelperFile(t, path)
	t.Setenv(envNetworkStatus, "disconnected") // the stale copy systemd made at start

	writeFile(t, path, "NETWORK_STATUS=disconnected\nNETWORK_WIFI_STATUS=disconnected\n")
	if got := ReadNetworkInfo(); got == nil || got.Status != "disconnected" {
		t.Fatalf("before WiFi is up: got %+v", got)
	}

	writeFile(t, path, connectedEnvFile)
	if got := ReadNetworkInfo(); got == nil || *got != connectedInfo {
		t.Fatalf("after pi-helper rewrote the file: got %+v, want %+v", got, connectedInfo)
	}
}

func TestReadNetworkInfoFileWithoutStatusIsNil(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pi-helper.env")
	usePiHelperFile(t, path)
	t.Setenv(envNetworkStatus, "connected")

	writeFile(t, path, "NETWORK_HOST=pi\n")
	if got := ReadNetworkInfo(); got != nil {
		t.Errorf("readable file without NETWORK_STATUS: got %+v, want nil", got)
	}
}

func TestReadNetworkInfoFallsBackToEnvWhenFileMissing(t *testing.T) {
	usePiHelperFile(t, filepath.Join(t.TempDir(), "absent.env"))
	clearNetworkEnv(t)
	t.Setenv(envNetworkStatus, "connected")
	t.Setenv(envNetworkIP, "10.0.0.9")

	got := ReadNetworkInfo()
	want := status.NetworkInfo{Status: "connected", IP: "10.0.0.9"}
	if got == nil || *got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	t.Setenv(envNetworkStatus, "")
	if got := ReadNetworkInfo(); got != nil {
		t.Errorf("no file and no env: got %+v, want nil", got)
	}
}

func TestPiHelperEnvPathDefault(t *testing.T) {
	if PiHelperEnvPath != "/run/pi-helper.env" || piHelperEnvPath != PiHelperEnvPath {
		t.Errorf("PiHelperEnvPath = %q, piHelperEnvPath = %q", PiHelperEnvPath, piHelperEnvPath)
	}
}
