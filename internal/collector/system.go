package collector

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

type SystemInfo struct {
	Hostname       string          `json:"hostname"`
	OS             string          `json:"os"`
	Kernel         string          `json:"kernel"`
	UptimeSeconds  int64           `json:"uptime_seconds"`
	Load           [3]float64      `json:"load"`
	CPUCount       int             `json:"cpu_count"`
	CPUPercent     float64         `json:"cpu_percent"`
	Memory         Memory          `json:"memory"`
	Disks          []Disk          `json:"disks"`
	ListeningPorts []ListeningPort `json:"listening_ports"`
}

type Memory struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Available   uint64  `json:"available"`
	UsedPercent float64 `json:"used_percent"`
	SwapTotal   uint64  `json:"swap_total"`
	SwapUsed    uint64  `json:"swap_used"`
}

type Disk struct {
	Mount       string  `json:"mount"`
	Device      string  `json:"device"`
	FSType      string  `json:"fstype"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

type ListeningPort struct {
	Proto   string `json:"proto"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

type System struct {
	mounts []string
	// previous /proc/stat sample, to compute CPU usage between two collections
	prevIdle, prevTotal uint64
}

func NewSystem(mounts []string) *System { return &System{mounts: mounts} }

func (s *System) Name() string { return "system" }

func (s *System) Collect(ctx context.Context) (any, error) {
	info := SystemInfo{CPUCount: runtime.NumCPU()}
	info.Hostname, _ = os.Hostname()
	info.OS = osPrettyName()
	info.Kernel = readTrim("/proc/sys/kernel/osrelease")

	if f := strings.Fields(readTrim("/proc/uptime")); len(f) > 0 {
		up, _ := strconv.ParseFloat(f[0], 64)
		info.UptimeSeconds = int64(up)
	}
	if f := strings.Fields(readTrim("/proc/loadavg")); len(f) >= 3 {
		for i := range 3 {
			info.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	info.CPUPercent = s.cpuPercent()

	mem, err := readMemory()
	if err != nil {
		return nil, err
	}
	info.Memory = mem
	info.Disks = readDisks(s.mounts)
	info.ListeningPorts = readListeningPorts()
	return info, nil
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func osPrettyName() string {
	for _, line := range strings.Split(readTrim("/etc/os-release"), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return runtime.GOOS
}

func (s *System) cpuPercent() float64 {
	line, _, _ := strings.Cut(readTrim("/proc/stat"), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0
	}
	var total, idle uint64
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle + iowait
			idle += n
		}
	}
	dTotal, dIdle := total-s.prevTotal, idle-s.prevIdle
	s.prevTotal, s.prevIdle = total, idle
	if dTotal == 0 {
		return 0
	}
	return round1(100 * float64(dTotal-dIdle) / float64(dTotal))
}

func readMemory() (Memory, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return Memory{}, err
	}
	defer f.Close()
	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(v)
		if len(fields) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(fields[0], 10, 64)
		vals[k] = n * 1024 // values are in kB
	}
	m := Memory{
		Total:     vals["MemTotal"],
		Available: vals["MemAvailable"],
		SwapTotal: vals["SwapTotal"],
		SwapUsed:  vals["SwapTotal"] - vals["SwapFree"],
	}
	if m.Total == 0 {
		return m, fmt.Errorf("could not read memory from /proc/meminfo")
	}
	m.Used = m.Total - m.Available
	m.UsedPercent = round1(100 * float64(m.Used) / float64(m.Total))
	return m, nil
}

var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "tmpfs": true, "devtmpfs": true, "devpts": true,
	"cgroup": true, "cgroup2": true, "overlay": true, "squashfs": true, "mqueue": true,
	"debugfs": true, "tracefs": true, "securityfs": true, "pstore": true, "bpf": true,
	"autofs": true, "hugetlbfs": true, "configfs": true, "fusectl": true, "binfmt_misc": true,
	"nsfs": true, "ramfs": true, "rootfs": true, "rpc_pipefs": true, "efivarfs": true, "fuse.lxcfs": true,
	"fuse.gvfsd-fuse": true, "fuse.portal": true, "nfsd": true,
}

func readDisks(only []string) []Disk {
	wanted := map[string]bool{}
	for _, m := range only {
		wanted[m] = true
	}
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()

	var disks []Disk
	seenDev := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		dev, mount, fstype := fields[0], unescapeMount(fields[1]), fields[2]
		if len(wanted) > 0 {
			if !wanted[mount] {
				continue
			}
		} else if pseudoFS[fstype] || seenDev[dev] || strings.HasPrefix(mount, "/snap/") {
			continue
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(mount, &st); err != nil || st.Blocks == 0 {
			continue
		}
		seenDev[dev] = true
		bs := uint64(st.Bsize)
		d := Disk{
			Mount: mount, Device: dev, FSType: fstype,
			Total: st.Blocks * bs,
			Free:  st.Bavail * bs,
		}
		d.Used = d.Total - st.Bfree*bs
		// Same formula as df: used / (used + available to non-root)
		if denom := d.Used + d.Free; denom > 0 {
			d.UsedPercent = round1(100 * float64(d.Used) / float64(denom))
		}
		disks = append(disks, d)
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Mount < disks[j].Mount })
	return disks
}

// unescapeMount decodes octal escapes used in /proc/mounts (e.g. \040 for space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// readListeningPorts parses /proc/net/{tcp,tcp6,udp,udp6}.
func readListeningPorts() []ListeningPort {
	sources := []struct {
		file, proto, state string
		v6                 bool
	}{
		{"/proc/net/tcp", "tcp", "0A", false},
		{"/proc/net/tcp6", "tcp", "0A", true},
		{"/proc/net/udp", "udp", "07", false},
		{"/proc/net/udp6", "udp", "07", true},
	}
	seen := map[string]bool{}
	var ports []ListeningPort
	for _, src := range sources {
		data, err := os.ReadFile(src.file)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines[min(1, len(lines)):] {
			f := strings.Fields(line)
			if len(f) < 4 || f[3] != src.state {
				continue
			}
			addrHex, portHex, ok := strings.Cut(f[1], ":")
			if !ok {
				continue
			}
			port, err := strconv.ParseUint(portHex, 16, 16)
			if err != nil || port == 0 {
				continue
			}
			p := ListeningPort{Proto: src.proto, Address: decodeAddr(addrHex, src.v6), Port: int(port)}
			key := fmt.Sprintf("%s/%s/%d", p.Proto, p.Address, p.Port)
			if !seen[key] {
				seen[key] = true
				ports = append(ports, p)
			}
		}
	}
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].Port != ports[j].Port {
			return ports[i].Port < ports[j].Port
		}
		if ports[i].Proto != ports[j].Proto {
			return ports[i].Proto < ports[j].Proto
		}
		return ports[i].Address < ports[j].Address
	})
	return ports
}

// decodeAddr converts the little-endian hex addresses of /proc/net/* to text.
func decodeAddr(h string, v6 bool) string {
	if !v6 {
		if len(h) != 8 {
			return h
		}
		var b [4]byte
		for i := range 4 {
			n, _ := strconv.ParseUint(h[i*2:i*2+2], 16, 8)
			b[3-i] = byte(n)
		}
		return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
	}
	if len(h) != 32 {
		return h
	}
	// IPv6: four 32-bit little-endian words
	var b [16]byte
	for w := range 4 {
		for i := range 4 {
			n, _ := strconv.ParseUint(h[w*8+i*2:w*8+i*2+2], 16, 8)
			b[w*4+3-i] = byte(n)
		}
	}
	return formatIPv6(b)
}

func formatIPv6(b [16]byte) string {
	allZero := true
	for _, v := range b {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return "::"
	}
	// IPv4-mapped (::ffff:a.b.c.d)
	if string(b[:10]) == string(make([]byte, 10)) && b[10] == 0xff && b[11] == 0xff {
		return fmt.Sprintf("::ffff:%d.%d.%d.%d", b[12], b[13], b[14], b[15])
	}
	if string(b[:15]) == string(make([]byte, 15)) && b[15] == 1 {
		return "::1"
	}
	parts := make([]string, 8)
	for i := range 8 {
		parts[i] = strconv.FormatUint(uint64(b[i*2])<<8|uint64(b[i*2+1]), 16)
	}
	return strings.Join(parts, ":")
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}
