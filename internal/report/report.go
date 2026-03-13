package report

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yangusik/php-watchdog/internal/ring"
	"github.com/yangusik/php-watchdog/internal/socket"
)

type Reason string

const (
	ReasonThreshold Reason = "RSS threshold exceeded"
	ReasonTrend     Reason = "Steady RSS growth detected"
	ReasonKilled    Reason = "Process disappeared (OOM kill or crash)"
)

type Report struct {
	PID         int
	Reason      Reason
	Detail      string
	Snapshots   []ring.Snapshot
	Context     *socket.ProcessContext // nil if no framework module is connected
	GeneratedAt time.Time
}

func Write(r Report, dumpPath string) (string, error) {
	content := format(r)

	if err := os.MkdirAll(dumpPath, 0755); err != nil {
		return "", err
	}

	filename := fmt.Sprintf("watchdog-%d-%s.txt",
		r.PID, r.GeneratedAt.Format("20060102-150405"))
	path := filepath.Join(dumpPath, filename)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}

	return path, nil
}

func format(r Report) string {
	var b strings.Builder
	sep := strings.Repeat("═", 51)

	b.WriteString(sep + "\n")
	b.WriteString(fmt.Sprintf("WATCHDOG REPORT — PID %d\n", r.PID))
	b.WriteString(sep + "\n")
	b.WriteString(fmt.Sprintf("Time:    %s\n", r.GeneratedAt.Format("2006-01-02 15:04:05")))
	b.WriteString(fmt.Sprintf("Reason:  %s\n", r.Reason))
	if r.Detail != "" {
		b.WriteString(fmt.Sprintf("Detail:  %s\n", r.Detail))
	}

	// Process context from framework module (Laravel, Symfony, or any other)
	if r.Context != nil {
		b.WriteString("\n")
		duration := r.GeneratedAt.Sub(r.Context.StartedAt).Round(time.Second)
		b.WriteString(fmt.Sprintf("Started: %s (ran %s before report)\n",
			r.Context.StartedAt.Format("15:04:05"), duration))

		if len(r.Context.Meta) > 0 {
			b.WriteString("\nContext:\n")
			// Sort keys for consistent output
			keys := make([]string, 0, len(r.Context.Meta))
			for k := range r.Context.Meta {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				b.WriteString(fmt.Sprintf("  %s: %v\n", k, r.Context.Meta[k]))
			}
		}
	}

	// Memory summary
	if len(r.Snapshots) > 0 {
		first := r.Snapshots[0]
		last := r.Snapshots[len(r.Snapshots)-1]
		growthKb := last.RSSKb - first.RSSKb

		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("RSS at report: %s\n", fmtKb(last.RSSKb)))
		b.WriteString(fmt.Sprintf("RSS at start:  %s\n", fmtKb(first.RSSKb)))
		b.WriteString(fmt.Sprintf("Growth:        +%s over %s\n",
			fmtKb(growthKb),
			last.Timestamp.Sub(first.Timestamp).Round(time.Second)))
	}

	// Timeline
	b.WriteString(fmt.Sprintf("\nRSS Timeline (last %d snapshots):\n", len(r.Snapshots)))
	for i, s := range r.Snapshots {
		marker := ""
		if i > 0 && s.RSSKb > r.Snapshots[i-1].RSSKb {
			diff := s.RSSKb - r.Snapshots[i-1].RSSKb
			marker = fmt.Sprintf("  ▲ +%s", fmtKb(diff))
		}
		if i == len(r.Snapshots)-1 {
			marker += "  ← REPORT POINT"
		}
		b.WriteString(fmt.Sprintf("  %s  %s%s\n",
			s.Timestamp.Format("15:04:05"), fmtKb(s.RSSKb), marker))
	}

	b.WriteString(sep + "\n")
	return b.String()
}

func fmtKb(kb int64) string {
	switch {
	case kb >= 1024*1024:
		return fmt.Sprintf("%.1f GB", float64(kb)/1024/1024)
	case kb >= 1024:
		return fmt.Sprintf("%.1f MB", float64(kb)/1024)
	default:
		return fmt.Sprintf("%d KB", kb)
	}
}
