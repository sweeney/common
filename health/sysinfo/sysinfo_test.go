package sysinfo

import (
	"encoding/json"
	"errors"
	"testing"
	"testing/fstest"
)

// Made-up values in the shape a Raspberry Pi running Debian produces.
func piFS() fstest.MapFS {
	return fstest.MapFS{
		"sys/class/thermal/thermal_zone0/temp": {Data: []byte("41234\n")},
		"proc/loadavg":                         {Data: []byte("0.09 0.06 0.01 6/164 12345\n")},
		"proc/uptime":                          {Data: []byte("1138051.16 4505740.04\n")},
		"proc/meminfo": {Data: []byte("MemTotal:         425112 kB\nMemFree:           12345 kB\n" +
			"MemAvailable:     240520 kB\nBuffers:            1000 kB\n")},
		"run/pi-helper.env": {Data: []byte("NETWORK_GATEWAY=192.0.2.1\nNETWORK_HOST=example-pi\n" +
			"NETWORK_IP=192.0.2.10\nNETWORK_STATUS=connected\nNETWORK_TYPE=wifi\n" +
			"NETWORK_WIFI_SSID=example\nNETWORK_WIFI_STATUS=connected\n")},
	}
}

func disk() (Disk, error) { return Disk{TotalBytes: 30 << 30, FreeBytes: 25 << 30}, nil }

func TestRead(t *testing.T) {
	got, err := json.Marshal(Read(piFS(), disk))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"cpu_temp_c":41.2,"load_1":0.09,"load_5":0.06,"load_15":0.01,"mem_total_mb":415.1,"mem_available_mb":234.9,"system_uptime_s":1138051,"disk_total_mb":30720,"disk_free_mb":25600,"network":{"status":"connected","type":"wifi","ip":"192.0.2.10","gateway":"192.0.2.1","host":"example-pi","wifi_status":"connected","wifi_ssid":"example"}}`
	if string(got) != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestEverythingMissingIsNull(t *testing.T) {
	got, _ := json.Marshal(Read(fstest.MapFS{}, func() (Disk, error) { return Disk{}, errors.New("no statfs") }))
	want := `{"cpu_temp_c":null,"load_1":null,"load_5":null,"load_15":null,"mem_total_mb":null,"mem_available_mb":null,"system_uptime_s":null,"disk_total_mb":null,"disk_free_mb":null,"network":null}`
	if string(got) != want {
		t.Errorf("unknown must be null, never 0:\n got %s\nwant %s", got, want)
	}
}

func TestGarbageIsNull(t *testing.T) {
	fs := fstest.MapFS{
		"sys/class/thermal/thermal_zone0/temp": {Data: []byte("hot\n")},
		"proc/loadavg":                         {Data: []byte("0.1\n")},
		"proc/uptime":                          {Data: []byte("\n")},
		"proc/meminfo":                         {Data: []byte("MemTotal: lots kB\n")},
	}
	info := Read(fs, nil)
	if info.CPUTempC != nil || info.Load1 != nil || info.UptimeS != nil || info.MemTotalMB != nil || info.DiskTotalMB != nil {
		t.Errorf("info = %+v", info)
	}
}

func TestPiHelperEnv(t *testing.T) {
	n := PiHelperEnv([]byte("# comment\nNETWORK_STATUS=connected\nNETWORK_IP='192.0.2.10'\nOTHER=x\n\nbroken line\n"))
	if n == nil || n.Status != "connected" || n.IP != "192.0.2.10" || n.Type != "" {
		t.Errorf("network = %+v", n)
	}
	if PiHelperEnv([]byte("OTHER=1\n")) != nil {
		t.Error("no NETWORK_ keys: nil")
	}
}

func TestStatfsDisk(t *testing.T) {
	d, err := StatfsDisk("/")()
	if err != nil || d.TotalBytes == 0 || d.FreeBytes > d.TotalBytes {
		t.Errorf("disk = %+v, %v", d, err)
	}
	if _, err := StatfsDisk("/does/not/exist")(); err == nil {
		t.Error("want an error")
	}
}
