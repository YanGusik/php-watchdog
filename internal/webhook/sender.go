package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/yangusik/php-watchdog/internal/report"
)

// Payload is the JSON body sent to the webhook URL.
type Payload struct {
	PID         int            `json:"pid"`
	Reason      string         `json:"reason"`
	Detail      string         `json:"detail,omitempty"`
	GeneratedAt time.Time      `json:"generated_at"`
	RSS         RSSInfo        `json:"rss"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"` // forwarded as-is from framework module
	DumpFile    string         `json:"dump_file,omitempty"`
}

type RSSInfo struct {
	CurrentMB float64 `json:"current_mb"`
	StartMB   float64 `json:"start_mb"`
	GrowthMB  float64 `json:"growth_mb"`
}

// Send sends the report as a JSON POST to the given URL.
// Non-blocking — should be called in a goroutine.
func Send(url string, r report.Report, dumpFile string) error {
	payload := buildPayload(r, dumpFile)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}

	return nil
}

func buildPayload(r report.Report, dumpFile string) Payload {
	p := Payload{
		PID:         r.PID,
		Reason:      string(r.Reason),
		Detail:      r.Detail,
		GeneratedAt: r.GeneratedAt,
		DumpFile:    dumpFile,
	}

	if len(r.Snapshots) > 0 {
		first := r.Snapshots[0]
		last := r.Snapshots[len(r.Snapshots)-1]
		p.RSS = RSSInfo{
			CurrentMB: float64(last.RSSKb) / 1024,
			StartMB:   float64(first.RSSKb) / 1024,
			GrowthMB:  float64(last.RSSKb-first.RSSKb) / 1024,
		}
	}

	if r.Context != nil {
		p.StartedAt = &r.Context.StartedAt
		p.Meta = r.Context.Meta
	}

	return p
}
