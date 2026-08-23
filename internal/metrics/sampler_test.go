package metrics

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alfred-identity/web/internal/store"
)

type memRecorder struct {
	mu     sync.Mutex
	samples []map[string]float64
	purged int64
}

func (m *memRecorder) RecordMetricSamples(ctx context.Context, ts time.Time, values map[string]float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]float64, len(values))
	for k, v := range values {
		cp[k] = v
	}
	m.samples = append(m.samples, cp)
	return nil
}

func (m *memRecorder) PurgeMetricsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purged++
	return 2, nil
}

func TestSamplerSampleAndRun(t *testing.T) {
	rec := &memRecorder{}
	s := &Sampler{
		Store: rec,
		Sources: Sources{
			GUIConnections: func() int { return 3 },
			GameSessions:   func() int { return 2 },
			DBPingLatency:  func(ctx context.Context) (float64, bool) { return 1.5, true },
			DBPoolStats:    func() (int, int, int) { return 4, 1, 3 },
		},
		Log:      slog.Default(),
		Interval: 20 * time.Millisecond,
		Retention: time.Hour,
	}
	s.sample(context.Background())
	rec.mu.Lock()
	if len(rec.samples) != 1 || rec.samples[0][store.MetricGUIConnections] != 3 {
		rec.mu.Unlock()
		t.Fatalf("%#v", rec.samples)
	}
	rec.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit")
	}
	rec.mu.Lock()
	n := len(rec.samples)
	rec.mu.Unlock()
	if n < 2 {
		t.Fatalf("expected multiple samples, got %d", n)
	}
}

func TestSamplerNilSafe(t *testing.T) {
	var s *Sampler
	s.Run(context.Background())
	s = &Sampler{}
	s.Run(context.Background())
	s.sample(context.Background())
}
