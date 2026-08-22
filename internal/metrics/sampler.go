package metrics

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/alfred-identity/web/internal/store"
)

// Sources supplies live counts for periodic sampling.
type Sources struct {
	GUIConnections func() int
	GameSessions   func() int
	DBPingLatency  func(ctx context.Context) (latencyMs float64, ok bool)
	DBPoolStats    func() (open, inUse, idle int)
}

// MetricRecorder persists sampled metrics.
type MetricRecorder interface {
	RecordMetricSamples(ctx context.Context, ts time.Time, values map[string]float64) error
	PurgeMetricsOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Sampler records daemon metrics on a fixed interval.
type Sampler struct {
	Store     MetricRecorder
	Sources   Sources
	Log       *slog.Logger
	Interval  time.Duration
	Retention time.Duration
}

func (s *Sampler) Run(ctx context.Context) {
	if s == nil || s.Store == nil {
		return
	}
	interval := s.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	retention := s.Retention
	if retention <= 0 {
		retention = 90 * 24 * time.Hour
	}
	s.sample(ctx)
	ticker := time.NewTicker(interval)
	purgeTicker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	defer purgeTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sample(ctx)
		case <-purgeTicker.C:
			cutoff := time.Now().UTC().Add(-retention)
			if n, err := s.Store.PurgeMetricsOlderThan(ctx, cutoff); err != nil && s.Log != nil {
				s.Log.Warn("metrics purge", "err", err)
			} else if n > 0 && s.Log != nil {
				s.Log.Debug("metrics purged", "rows", n)
			}
		}
	}
}

func (s *Sampler) sample(ctx context.Context) {
	if s == nil || s.Store == nil || s.Sources.GUIConnections == nil || s.Sources.GameSessions == nil {
		return
	}
	sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	values := map[string]float64{
		store.MetricGUIConnections: float64(s.Sources.GUIConnections()),
		store.MetricGameSessions:   float64(s.Sources.GameSessions()),
	}
	if s.Sources.DBPingLatency != nil {
		if ms, ok := s.Sources.DBPingLatency(sampleCtx); ok {
			values[store.MetricDBLatencyMS] = ms
		}
	}
	if s.Sources.DBPoolStats != nil {
		open, inUse, idle := s.Sources.DBPoolStats()
		values[store.MetricDBOpenConns] = float64(open)
		values[store.MetricDBInUseConns] = float64(inUse)
		values[store.MetricDBIdleConns] = float64(idle)
	}
	if err := s.Store.RecordMetricSamples(sampleCtx, time.Now().UTC(), values); err != nil && s.Log != nil {
		s.Log.Warn("metrics sample", "err", err)
	}
}

// PingLatencyMS measures database round-trip latency in milliseconds.
func PingLatencyMS(db *sql.DB) func(ctx context.Context) (float64, bool) {
	return func(ctx context.Context) (float64, bool) {
		if db == nil {
			return 0, false
		}
		start := time.Now()
		if err := db.PingContext(ctx); err != nil {
			return 0, false
		}
		return float64(time.Since(start).Milliseconds()), true
	}
}

// PoolStats reads sql.DB pool counters.
func PoolStats(db *sql.DB) func() (open, inUse, idle int) {
	return func() (open, inUse, idle int) {
		if db == nil {
			return 0, 0, 0
		}
		st := db.Stats()
		return st.OpenConnections, st.InUse, st.Idle
	}
}
