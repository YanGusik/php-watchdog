package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Snapshot struct {
	PID     int
	Cmdline string
	RSSKb   int64
}

func main() {
	filter := flag.String("filter", "", "filter by cmdline (e.g.: php, queue:work)")
	flag.Parse()

	snapshots, err := scanProc(*filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(snapshots) == 0 {
		fmt.Println("no processes found")
		return
	}

	printTable(snapshots)
}

func scanProc(filter string) ([]Snapshot, error) {
	entries, err := filepath.Glob("/proc/*/cmdline")
	if err != nil {
		return nil, err
	}

	var result []Snapshot

	for _, cmdlinePath := range entries {
		pid, err := extractPID(cmdlinePath)
		if err != nil {
			continue
		}

		cmdline, err := readCmdline(cmdlinePath)
		if err != nil {
			continue
		}

		if filter != "" && !strings.Contains(cmdline, filter) {
			continue
		}

		rss, err := readRSS(pid)
		if err != nil {
			continue
		}

		result = append(result, Snapshot{
			PID:     pid,
			Cmdline: truncate(cmdline, 60),
			RSSKb:   rss,
		})
	}

	return result, nil
}

func extractPID(path string) (int, error) {
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		return 0, fmt.Errorf("unexpected path: %s", path)
	}
	return strconv.Atoi(parts[2])
}

// readCmdline reads /proc/PID/cmdline.
// Arguments are separated by null bytes (\0) — this is how the Linux kernel stores argv[].
func readCmdline(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("empty cmdline")
	}
	cmdline := strings.ReplaceAll(string(data), "\x00", " ")
	return strings.TrimSpace(cmdline), nil
}

// readRSS reads VmRSS from /proc/PID/status.
// VmRSS is the Resident Set Size — actual RAM used by the process.
// This is what the OOM killer sees, not what memory_get_usage() reports.
func readRSS(pid int) (int64, error) {
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

func printTable(snapshots []Snapshot) {
	fmt.Printf("%-8s  %-10s  %-60s\n", "PID", "RSS", "CMDLINE")
	fmt.Println(strings.Repeat("─", 82))

	for _, s := range snapshots {
		fmt.Printf("%-8d  %-10s  %-60s\n", s.PID, formatRSS(s.RSSKb), s.Cmdline)
	}

	fmt.Println(strings.Repeat("─", 82))
	fmt.Printf("Total processes: %d\n", len(snapshots))
}

func formatRSS(kb int64) string {
	switch {
	case kb >= 1024*1024:
		return fmt.Sprintf("%.1f GB", float64(kb)/1024/1024)
	case kb >= 1024:
		return fmt.Sprintf("%.1f MB", float64(kb)/1024)
	default:
		return fmt.Sprintf("%d KB", kb)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
