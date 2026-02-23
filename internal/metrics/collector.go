package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kon-rad/openclaw-trace/internal/ingest"
)

type Enqueuer interface {
	Enqueue(event ingest.Event) bool
}

type Collector struct {
	interval time.Duration
	enqueuer Enqueuer
	dbPath   string

	lastCPUSample *cpuSample
	lastIO        *ioSample
}

type cpuSample struct {
	usageUsec int64
	at        time.Time
}

type ioSample struct {
	readBytes  int64
	writeBytes int64
	at         time.Time
}

func NewCollector(interval time.Duration, enqueuer Enqueuer, dbPath string) *Collector {
	return &Collector{
		interval: interval,
		enqueuer: enqueuer,
		dbPath:   dbPath,
	}
}

func (c *Collector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			event, emit, err := c.collect()
			if err != nil || !emit {
				continue
			}
			c.enqueuer.Enqueue(event)
		}
	}
}

func (c *Collector) collect() (ingest.Event, bool, error) {
	now := time.Now()
	usageUsec, err := readCPUUsageUsec()
	if err != nil {
		return ingest.Event{}, false, err
	}
	cores := readCPUCgroupCores()

	cur := &cpuSample{usageUsec: usageUsec, at: now}
	if c.lastCPUSample == nil {
		c.lastCPUSample = cur
		// SYSM-06: discard first sample.
		return ingest.Event{}, false, nil
	}
	deltaUsage := float64(cur.usageUsec-c.lastCPUSample.usageUsec) / 1_000_000.0
	deltaTime := cur.at.Sub(c.lastCPUSample.at).Seconds()
	c.lastCPUSample = cur
	if deltaTime <= 0 {
		return ingest.Event{}, false, nil
	}
	cpuPct := (deltaUsage / deltaTime) * 100.0 / cores
	if cpuPct < 0 {
		cpuPct = 0
	}

	memCurrent, memTotal := readMemoryCgroup()
	memAvail := int64(0)
	if memTotal > 0 && memTotal >= memCurrent {
		memAvail = memTotal - memCurrent
	}

	diskUsed, diskTotal, diskFree := readDiskStats(filepath.Dir(c.dbPath))
	ioReadRate, ioWriteRate := c.readIORates(now)
	usagePct := 0.0
	if diskTotal > 0 {
		usagePct = (float64(diskUsed) / float64(diskTotal)) * 100
	}

	metadata := map[string]any{
		"io_read_bytes_per_sec":  ioReadRate,
		"io_write_bytes_per_sec": ioWriteRate,
		"disk_usage_pct":         usagePct,
		"source":                 "cgroup",
	}
	metaBytes, _ := json.Marshal(metadata)

	return ingest.Event{
		Kind:      ingest.EventKindMetric,
		CreatedAt: now.UnixMilli(),
		Metric: &ingest.MetricPayload{
			CPUPct:        cpuPct,
			MemRSSBytes:   memCurrent,
			MemAvailable:  memAvail,
			MemTotal:      memTotal,
			DiskUsedBytes: diskUsed,
			DiskTotal:     diskTotal,
			DiskFreeBytes: diskFree,
			Metadata:      string(metaBytes),
		},
	}, true, nil
}

func readCPUUsageUsec() (int64, error) {
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[0] == "usage_usec" {
			return strconv.ParseInt(fields[1], 10, 64)
		}
	}
	return 0, fmt.Errorf("usage_usec not found")
}

func readCPUCgroupCores() float64 {
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return float64(runtime.NumCPU())
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] == "max" {
		return float64(runtime.NumCPU())
	}
	quota, err1 := strconv.ParseFloat(fields[0], 64)
	period, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || period <= 0 {
		return float64(runtime.NumCPU())
	}
	cores := quota / period
	if cores < 1 {
		return 1
	}
	return cores
}

func readMemoryCgroup() (current int64, total int64) {
	curBytes, err := os.ReadFile("/sys/fs/cgroup/memory.current")
	if err != nil {
		return 0, 0
	}
	current, _ = strconv.ParseInt(strings.TrimSpace(string(curBytes)), 10, 64)

	maxBytes, err := os.ReadFile("/sys/fs/cgroup/memory.max")
	if err != nil {
		return current, 0
	}
	maxStr := strings.TrimSpace(string(maxBytes))
	if maxStr == "max" {
		return current, 0
	}
	total, _ = strconv.ParseInt(maxStr, 10, 64)
	return current, total
}

func readDiskStats(path string) (used int64, total int64, free int64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, 0
	}
	total = int64(stat.Blocks) * int64(stat.Bsize)
	free = int64(stat.Bavail) * int64(stat.Bsize)
	used = total - free
	return used, total, free
}

func readProcSelfIO() (int64, int64) {
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return 0, 0
	}
	var readBytes, writeBytes int64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "read_bytes":
			readBytes, _ = strconv.ParseInt(fields[1], 10, 64)
		case "write_bytes":
			writeBytes, _ = strconv.ParseInt(fields[1], 10, 64)
		}
	}
	return readBytes, writeBytes
}

func (c *Collector) readIORates(now time.Time) (int64, int64) {
	readBytes, writeBytes := readProcSelfIO()
	cur := &ioSample{readBytes: readBytes, writeBytes: writeBytes, at: now}
	if c.lastIO == nil {
		c.lastIO = cur
		return 0, 0
	}
	seconds := cur.at.Sub(c.lastIO.at).Seconds()
	if seconds <= 0 {
		return 0, 0
	}
	readRate := int64(float64(cur.readBytes-c.lastIO.readBytes) / seconds)
	writeRate := int64(float64(cur.writeBytes-c.lastIO.writeBytes) / seconds)
	c.lastIO = cur
	if readRate < 0 {
		readRate = 0
	}
	if writeRate < 0 {
		writeRate = 0
	}
	return readRate, writeRate
}
