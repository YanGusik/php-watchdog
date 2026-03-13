package detector

import (
	"fmt"

	"github.com/yangusik/php-watchdog/internal/ring"
)

type AnomalyType string

const (
	AnomalyThreshold AnomalyType = "threshold"
	AnomalyTrend     AnomalyType = "trend"
)

type Result struct {
	Detected bool
	Type     AnomalyType
	Message  string
}

type Detector interface {
	Check(snapshots []ring.Snapshot) Result
}

// ThresholdDetector triggers when the latest RSS exceeds an absolute limit.
type ThresholdDetector struct {
	ThresholdKb int64
}

func (d *ThresholdDetector) Check(snapshots []ring.Snapshot) Result {
	if len(snapshots) == 0 {
		return Result{}
	}

	latest := snapshots[len(snapshots)-1]
	if latest.RSSKb > d.ThresholdKb {
		return Result{
			Detected: true,
			Type:     AnomalyThreshold,
			Message: fmt.Sprintf("RSS %.1f MB exceeds threshold %.1f MB",
				float64(latest.RSSKb)/1024, float64(d.ThresholdKb)/1024),
		}
	}

	return Result{}
}

// TrendDetector triggers when RSS grows for N consecutive snapshots (slow leak).
type TrendDetector struct {
	MinSnapshots int
}

func (d *TrendDetector) Check(snapshots []ring.Snapshot) Result {
	if len(snapshots) < d.MinSnapshots {
		return Result{}
	}

	recent := snapshots[len(snapshots)-d.MinSnapshots:]

	for i := 1; i < len(recent); i++ {
		if recent[i].RSSKb <= recent[i-1].RSSKb {
			return Result{}
		}
	}

	first := recent[0].RSSKb
	last := recent[len(recent)-1].RSSKb
	growthMB := float64(last-first) / 1024

	return Result{
		Detected: true,
		Type:     AnomalyTrend,
		Message: fmt.Sprintf("RSS growing for %d consecutive snapshots (+%.1f MB)",
			d.MinSnapshots, growthMB),
	}
}
