package proc

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Process holds information about a discovered process.
type Process struct {
	PID     int
	Cmdline string
}

// Reader is the interface for reading process data from the OS.
// Watcher depends on Reader, not FSReader directly — allows swapping in tests.
type Reader interface {
	FindByMask(mask string) ([]Process, error)
	ReadRSS(pid int) (int64, error)
	IsAlive(pid int) bool
}

// FSReader implements Reader using the Linux /proc filesystem.
type FSReader struct{}

func New() *FSReader {
	return &FSReader{}
}

// FindByMask scans /proc and returns processes whose cmdline matches the mask.
func (r *FSReader) FindByMask(mask string) ([]Process, error) {
	entries, err := filepath.Glob("/proc/*/cmdline")
	if err != nil {
		return nil, err
	}

	var result []Process

	for _, cmdlinePath := range entries {
		pid, err := extractPID(cmdlinePath)
		if err != nil {
			continue // non-numeric entry like /proc/self — skip
		}

		cmdline, err := readCmdlineFile(cmdlinePath)
		if err != nil {
			continue // process died while reading — normal during scan
		}

		if !matchMask(mask, cmdline) {
			continue
		}

		result = append(result, Process{PID: pid, Cmdline: cmdline})
	}

	return result, nil
}

// ReadRSS reads VmRSS from /proc/PID/status. Returns value in kilobytes.
func (r *FSReader) ReadRSS(pid int) (int64, error) {
	path := fmt.Sprintf("/proc/%d/status", pid)

	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			// "VmRSS:    123456 kB"
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			return strconv.ParseInt(fields[1], 10, 64)
		}
	}

	return 0, fmt.Errorf("VmRSS not found in %s", path)
}

// IsAlive reports whether the process with the given PID still exists.
func (r *FSReader) IsAlive(pid int) bool {
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}

// matchMask checks whether cmdline matches the mask.
// Without * it is a simple substring check.
// With * each part separated by * must appear in order in cmdline.
// Examples:
//
//	"horizon:work"             → substring match
//	"horizon:work*--queue=ai"  → both parts must appear in order
func matchMask(mask, cmdline string) bool {
	if !strings.Contains(mask, "*") {
		return strings.Contains(cmdline, mask)
	}

	parts := strings.Split(mask, "*")
	pos := 0
	for _, part := range parts {
		if part == "" {
			continue
		}
		idx := strings.Index(cmdline[pos:], part)
		if idx == -1 {
			return false
		}
		pos += idx + len(part)
	}
	return true
}

func extractPID(path string) (int, error) {
	// "/proc/1234/cmdline" → ["", "proc", "1234", "cmdline"]
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return 0, fmt.Errorf("unexpected path: %s", path)
	}
	return strconv.Atoi(parts[2])
}

// readCmdlineFile reads /proc/PID/cmdline replacing null bytes with spaces.
// The Linux kernel stores argv[] separated by \0.
func readCmdlineFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("empty cmdline")
	}
	return strings.TrimSpace(strings.ReplaceAll(string(data), "\x00", " ")), nil
}
