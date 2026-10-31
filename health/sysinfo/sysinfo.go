// Package sysinfo reads a Raspberry Pi's vital signs for /healthz: CPU
// temperature, load, memory, uptime, root disk, and the network state that
// pi-helper writes to /run/pi-helper.env. Every file is read through an
// fs.FS rooted at "/", so tests use fstest.MapFS. A file that is missing or
// unparseable gives null, never 0.
//
// Only files seen on the Pis in use are read (checked on a Pi Zero 2 W
// running Debian 13).
package sysinfo

import (
	"bufio"
	"bytes"
	"io/fs"
	"math"
	"strconv"
	"strings"
	"syscall"
)

// Paths, relative to the filesystem root.
const (
	PathCPUTemp  = "sys/class/thermal/thermal_zone0/temp"
	PathLoadAvg  = "proc/loadavg"
	PathUptime   = "proc/uptime"
	PathMemInfo  = "proc/meminfo"
	PathPiHelper = "run/pi-helper.env"
)

// Info is the snapshot. Field order is the JSON order.
type Info struct {
	CPUTempC       *float64 `json:"cpu_temp_c"`
	Load1          *float64 `json:"load_1"`
	Load5          *float64 `json:"load_5"`
	Load15         *float64 `json:"load_15"`
	MemTotalMB     *float64 `json:"mem_total_mb"`
	MemAvailableMB *float64 `json:"mem_available_mb"`
	UptimeS        *int64   `json:"system_uptime_s"`
	DiskTotalMB    *float64 `json:"disk_total_mb"`
	DiskFreeMB     *float64 `json:"disk_free_mb"`
	Network        *Network `json:"network"`
}

// Network is pi-helper's NETWORK_* values.
type Network struct {
	Status     string `json:"status,omitempty"`
	Type       string `json:"type,omitempty"`
	IP         string `json:"ip,omitempty"`
	Gateway    string `json:"gateway,omitempty"`
	Host       string `json:"host,omitempty"`
	WifiStatus string `json:"wifi_status,omitempty"`
	WifiSSID   string `json:"wifi_ssid,omitempty"`
}

// Disk is a filesystem's size.
type Disk struct {
	TotalBytes uint64
	FreeBytes  uint64 // available to unprivileged users
}

// Read takes the snapshot. disk may be nil (no disk figures).
func Read(fsys fs.FS, disk func() (Disk, error)) Info {
	var in Info
	if v, ok := readFloat(fsys, PathCPUTemp, 0); ok {
		in.CPUTempC = ptr(round1(v / 1000))
	}
	if f := fields(fsys, PathLoadAvg); len(f) >= 3 {
		in.Load1, in.Load5, in.Load15 = parse(f[0]), parse(f[1]), parse(f[2])
		if in.Load1 == nil || in.Load5 == nil || in.Load15 == nil {
			in.Load1, in.Load5, in.Load15 = nil, nil, nil
		}
	}
	if v, ok := readFloat(fsys, PathUptime, 0); ok {
		s := int64(v)
		in.UptimeS = &s
	}
	if data, err := fs.ReadFile(fsys, PathMemInfo); err == nil {
		mem := meminfo(data)
		if v, ok := mem["MemTotal"]; ok {
			in.MemTotalMB = ptr(round1(v / 1024))
		}
		if v, ok := mem["MemAvailable"]; ok {
			in.MemAvailableMB = ptr(round1(v / 1024))
		}
	}
	if disk != nil {
		if d, err := disk(); err == nil && d.TotalBytes > 0 {
			in.DiskTotalMB = ptr(round1(float64(d.TotalBytes) / (1 << 20)))
			in.DiskFreeMB = ptr(round1(float64(d.FreeBytes) / (1 << 20)))
		}
	}
	if data, err := fs.ReadFile(fsys, PathPiHelper); err == nil {
		in.Network = PiHelperEnv(data)
	}
	return in
}

// PiHelperEnv parses pi-helper's KEY=VALUE env file. It returns nil if the
// file has no NETWORK_* keys.
func PiHelperEnv(data []byte) *Network {
	var n Network
	found := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		v = strings.Trim(v, `"'`)
		dst := map[string]*string{
			"NETWORK_STATUS": &n.Status, "NETWORK_TYPE": &n.Type, "NETWORK_IP": &n.IP,
			"NETWORK_GATEWAY": &n.Gateway, "NETWORK_HOST": &n.Host,
			"NETWORK_WIFI_STATUS": &n.WifiStatus, "NETWORK_WIFI_SSID": &n.WifiSSID,
		}[k]
		if dst != nil {
			*dst = v
			found = true
		}
	}
	if !found {
		return nil
	}
	return &n
}

// StatfsDisk returns a disk reader for the filesystem holding path.
func StatfsDisk(path string) func() (Disk, error) {
	return func() (Disk, error) {
		var st syscall.Statfs_t
		if err := syscall.Statfs(path, &st); err != nil {
			return Disk{}, err
		}
		bs := uint64(st.Bsize)
		return Disk{TotalBytes: uint64(st.Blocks) * bs, FreeBytes: uint64(st.Bavail) * bs}, nil
	}
}

func fields(fsys fs.FS, name string) []string {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil
	}
	return strings.Fields(string(data))
}

func readFloat(fsys fs.FS, name string, field int) (float64, bool) {
	f := fields(fsys, name)
	if len(f) <= field {
		return 0, false
	}
	p := parse(f[field])
	if p == nil {
		return 0, false
	}
	return *p, true
}

func parse(s string) *float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// meminfo parses "Key:   123 kB" lines into kB values.
func meminfo(data []byte) map[string]float64 {
	out := map[string]float64{}
	for _, line := range strings.Split(string(data), "\n") {
		k, rest, ok := strings.Cut(line, ":")
		f := strings.Fields(rest)
		if !ok || len(f) == 0 {
			continue
		}
		if v := parse(f[0]); v != nil {
			out[k] = *v
		}
	}
	return out
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func ptr[T any](v T) *T { return &v }
