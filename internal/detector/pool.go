package detector

import (
	"fmt"
	"sort"

	"github.com/yangusik/php-watchdog/internal/ring"
)

type PoolResult struct {
	Detected    bool
	TotalRSSKb  int64
	Message     string
	KillPIDs    []int // PIDs to kill based on strategy
}

// PoolDetector checks total RSS across all tracked processes.
// Strategy "heaviest": kill the heaviest process, re-check next tick.
// Strategy "all": kill all processes immediately.
type PoolDetector struct {
	ThresholdKb  int64
	KillStrategy string // "heaviest" or "all"
}

// Check receives a map of PID -> latest snapshot and returns a PoolResult.
func (d *PoolDetector) Check(latest map[int]ring.Snapshot) PoolResult {
	if len(latest) == 0 {
		return PoolResult{}
	}

	var totalKb int64
	for _, s := range latest {
		totalKb += s.RSSKb
	}

	if totalKb <= d.ThresholdKb {
		return PoolResult{}
	}

	result := PoolResult{
		Detected:   true,
		TotalRSSKb: totalKb,
		Message: fmt.Sprintf("pool total RSS %.1f MB exceeds limit %.1f MB (%d processes)",
			float64(totalKb)/1024, float64(d.ThresholdKb)/1024, len(latest)),
	}

	switch d.KillStrategy {
	case "all":
		for pid := range latest {
			result.KillPIDs = append(result.KillPIDs, pid)
		}

	case "heaviest":
		// Sort by RSS descending, pick the top one
		type pidRSS struct {
			pid int
			rss int64
		}
		ranked := make([]pidRSS, 0, len(latest))
		for pid, s := range latest {
			ranked = append(ranked, pidRSS{pid, s.RSSKb})
		}
		sort.Slice(ranked, func(i, j int) bool {
			return ranked[i].rss > ranked[j].rss
		})
		result.KillPIDs = []int{ranked[0].pid}
	}

	return result
}
