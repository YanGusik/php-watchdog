package watcher

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/yangusik/php-watchdog/config"
	"github.com/yangusik/php-watchdog/internal/detector"
	"github.com/yangusik/php-watchdog/internal/proc"
	"github.com/yangusik/php-watchdog/internal/report"
	"github.com/yangusik/php-watchdog/internal/ring"
	"github.com/yangusik/php-watchdog/internal/socket"
	"github.com/yangusik/php-watchdog/internal/webhook"
)

// workerState holds the runtime state of a single tracked process.
type workerState struct {
	buf        *ring.Buffer
	watcherCfg config.Watcher
}

// Watcher is the main monitoring loop.
type Watcher struct {
	cfg    *config.Config
	reader proc.Reader
	jobs   *socket.Store
	states map[int]*workerState // PID → state
}

func New(cfg *config.Config, reader proc.Reader, jobs *socket.Store) *Watcher {
	return &Watcher{
		cfg:    cfg,
		reader: reader,
		jobs:   jobs,
		states: make(map[int]*workerState),
	}
}

// Run starts the main loop. Blocking — run in a goroutine.
func (w *Watcher) Run(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Duration(w.cfg.Interval) * time.Second)
	defer ticker.Stop()

	log.Printf("starting %d watcher(s), interval %ds", len(w.cfg.Watchers), w.cfg.Interval)
	for _, wc := range w.cfg.Watchers {
		log.Printf("  watcher %q: mask=%q rss_limit=%dMB growth_snapshots=%d",
			wc.Name, wc.Mask, wc.Thresholds.RSSAbsoluteMB, wc.Thresholds.GrowthSnapshots)
	}

	for {
		select {
		case <-ticker.C:
			w.tick()
		case <-stop:
			log.Println("watcher stopped")
			return
		}
	}
}

func (w *Watcher) tick() {
	alivePIDs := make(map[int]bool)

	for _, wc := range w.cfg.Watchers {
		processes, err := w.reader.FindByMask(wc.Mask)
		if err != nil {
			log.Printf("scan error for mask %q: %v", wc.Mask, err)
			continue
		}

		for _, p := range processes {
			alivePIDs[p.PID] = true

			if _, exists := w.states[p.PID]; !exists {
				w.states[p.PID] = &workerState{
					buf:        ring.New(w.cfg.RingBuffer),
					watcherCfg: wc,
				}
				log.Printf("[%s] tracking new process PID %d: %s", wc.Name, p.PID, p.Cmdline)
			}

			rss, err := w.reader.ReadRSS(p.PID)
			if err != nil {
				continue // process died between FindByMask and ReadRSS
			}

			w.states[p.PID].buf.Push(ring.Snapshot{
				PID:       p.PID,
				RSSKb:     rss,
				Timestamp: time.Now(),
			})

			w.checkAnomalies(p.PID)
		}
	}

	w.checkPool()

	// Processes that disappeared since last tick → post-mortem report
	for pid := range w.states {
		if !alivePIDs[pid] {
			log.Printf("PID %d disappeared — generating post-mortem report", pid)
			w.generateReport(pid, report.ReasonKilled, "")
			delete(w.states, pid)
			w.jobs.Delete(pid)
		}
	}
}

func (w *Watcher) checkPool() {
	byWatcher := make(map[string]map[int]ring.Snapshot)
	byWatcherCfg := make(map[string]config.Watcher)

	for pid, state := range w.states {
		name := state.watcherCfg.Name
		if _, ok := byWatcher[name]; !ok {
			byWatcher[name] = make(map[int]ring.Snapshot)
			byWatcherCfg[name] = state.watcherCfg
		}
		if snap, ok := state.buf.Latest(); ok {
			byWatcher[name][pid] = snap
		}
	}

	for name, latestSnapshots := range byWatcher {
		wc := byWatcherCfg[name]
		if wc.Thresholds.PoolRSSTotalMB == 0 {
			continue
		}

		d := &detector.PoolDetector{
			ThresholdKb:  wc.Thresholds.PoolRSSTotalMB * 1024,
			KillStrategy: wc.Thresholds.PoolKillStrategy,
		}

		result := d.Check(latestSnapshots)
		if !result.Detected {
			continue
		}

		log.Printf("[%s] pool anomaly: %s", name, result.Message)

		for _, pid := range result.KillPIDs {
			w.generateReport(pid, report.ReasonThreshold, result.Message)
			if wc.OnAnomaly.Kill {
				w.killProcess(pid)
			}
			if wc.OnAnomaly.Exec != "" {
				w.runExec(wc.OnAnomaly.Exec, pid, result.Message, "")
			}
		}
	}
}

func (w *Watcher) checkAnomalies(pid int) {
	state := w.states[pid]
	snapshots := state.buf.All()
	wc := state.watcherCfg

	for _, d := range buildDetectors(wc.Thresholds) {
		result := d.Check(snapshots)
		if !result.Detected {
			continue
		}

		log.Printf("[%s] anomaly detected for PID %d: %s", wc.Name, pid, result.Message)
		dumpFile := w.generateReport(pid, anomalyReason(result.Type), result.Message)

		if wc.OnAnomaly.Kill {
			w.killProcess(pid)
		}

		if wc.OnAnomaly.Exec != "" {
			w.runExec(wc.OnAnomaly.Exec, pid, result.Message, dumpFile)
		}

		break
	}
}

func (w *Watcher) generateReport(pid int, reason report.Reason, detail string) string {
	state, ok := w.states[pid]
	if !ok {
		return ""
	}

	r := report.Report{
		PID:         pid,
		Reason:      reason,
		Detail:      detail,
		Snapshots:   state.buf.All(),
		GeneratedAt: time.Now(),
	}

	if ctx, ok := w.jobs.Get(pid); ok {
		r.Context = &ctx
	}

	if state.watcherCfg.OnAnomaly.DumpPath == "" {
		return ""
	}

	path, err := report.Write(r, state.watcherCfg.OnAnomaly.DumpPath)
	if err != nil {
		log.Printf("failed to write report: %v", err)
		return ""
	}

	log.Printf("report written: %s", path)

	if url := state.watcherCfg.OnAnomaly.Webhook; url != "" {
		go func() {
			if err := webhook.Send(url, r, path); err != nil {
				log.Printf("webhook error: %v", err)
			} else {
				log.Printf("webhook sent: %s", url)
			}
		}()
	}

	return path
}

// runExec runs a custom script passing context via environment variables.
// Fire-and-forget — does not block the watcher.
func (w *Watcher) runExec(bin string, pid int, reason, dumpFile string) {
	state, ok := w.states[pid]
	if !ok {
		return
	}

	latest, _ := state.buf.Latest()

	env := append(os.Environ(),
		fmt.Sprintf("WATCHDOG_PID=%d", pid),
		fmt.Sprintf("WATCHDOG_RSS_MB=%.1f", float64(latest.RSSKb)/1024),
		fmt.Sprintf("WATCHDOG_REASON=%s", reason),
		fmt.Sprintf("WATCHDOG_DUMP_FILE=%s", dumpFile),
	)

	if ctx, ok := w.jobs.Get(pid); ok {
		env = append(env,
			fmt.Sprintf("WATCHDOG_STARTED_AT=%s", ctx.StartedAt.Format(time.RFC3339)),
		)
		for k, v := range ctx.Meta {
			env = append(env, fmt.Sprintf("WATCHDOG_META_%s=%v", strings.ToUpper(k), v))
		}
	}

	cmd := exec.Command(bin)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Printf("exec failed: %v", err)
		return
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("exec exited with error: %v", err)
		}
	}()
}

func (w *Watcher) killProcess(pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := p.Kill(); err != nil {
		log.Printf("failed to kill PID %d: %v", pid, err)
		return
	}
	log.Printf("killed PID %d", pid)
}

func buildDetectors(t config.Thresholds) []detector.Detector {
	var detectors []detector.Detector

	if t.RSSAbsoluteMB > 0 {
		detectors = append(detectors, &detector.ThresholdDetector{
			ThresholdKb: t.RSSAbsoluteMB * 1024,
		})
	}

	if t.GrowthSnapshots > 0 {
		detectors = append(detectors, &detector.TrendDetector{
			MinSnapshots: t.GrowthSnapshots,
		})
	}

	return detectors
}

func anomalyReason(t detector.AnomalyType) report.Reason {
	switch t {
	case detector.AnomalyThreshold:
		return report.ReasonThreshold
	case detector.AnomalyTrend:
		return report.ReasonTrend
	default:
		return report.ReasonKilled
	}
}
