package dashboardapi

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// sysInfo caches system-level metrics that need state between calls (CPU usage
// from /proc/stat delta) and provides total RAM. Linux-only; returns zeros on
// other platforms.
type sysInfo struct {
	mu sync.Mutex

	// /proc/stat fields from previous sample
	prevCPUTotal uint64
	prevCPUBusy  uint64
}

// totalMemBytes returns total system RAM. Reads /proc/meminfo on Linux,
// returns 0 on other platforms.
func totalMemBytes() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseUint(fields[1], 10, 64)
				if err == nil {
					return kb * 1024
				}
			}
			break
		}
	}
	return 0
}

// cpuUsagePct returns system-wide CPU usage percentage (0-100) computed from
// /proc/stat delta between calls. First call returns 0. Linux-only.
func (s *sysInfo) cpuUsagePct() float64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0
	}
	// First line: "cpu  user nice system idle iowait irq softirq steal ..."
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}
	var vals []uint64
	for _, v := range fields[1:] {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0
		}
		vals = append(vals, n)
	}
	// idle = idle(3) + iowait(4) if present
	idle := vals[3]
	if len(vals) > 4 {
		idle += vals[4]
	}
	total := uint64(0)
	for _, v := range vals {
		total += v
	}
	busy := total - idle

	s.mu.Lock()
	prevTotal := s.prevCPUTotal
	prevBusy := s.prevCPUBusy
	s.prevCPUTotal = total
	s.prevCPUBusy = busy
	s.mu.Unlock()

	if prevTotal == 0 {
		return 0
	}
	dt := total - prevTotal
	if dt == 0 {
		return 0
	}
	return float64(busy-prevBusy) / float64(dt) * 100
}
