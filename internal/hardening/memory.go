package hardening

import (
	"bufio"
	"errors"
	"os"
	"strconv"
	"strings"
)

// CurrentRSSBytes returns VmRSS bytes from /proc/self/status (Linux only).
func CurrentRSSBytes() (int64, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, errors.New("VmRSS parse failure")
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		return kb * 1024, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("VmRSS not found")
}
