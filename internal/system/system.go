// Package system reads host resource information (memory, disk) so the daemon
// can report a node's real capacity to the panel for automatic detection.
package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// MemoryMB returns the host's total RAM in megabytes, read from /proc/meminfo.
func MemoryMB() (uint64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		// Format: "MemTotal:       32791234 kB"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("unexpected MemTotal line: %q", line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse MemTotal: %w", err)
		}
		return kb / 1024, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("MemTotal not found in /proc/meminfo")
}

// DiskMB returns the total size in megabytes of the filesystem that contains
// path. If path is empty it falls back to the root filesystem.
func DiskMB(path string) (uint64, error) {
	if path == "" {
		path = "/"
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	totalBytes := st.Blocks * uint64(st.Bsize)
	return totalBytes / 1024 / 1024, nil
}
