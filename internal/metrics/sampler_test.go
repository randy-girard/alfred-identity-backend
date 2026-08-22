package metrics

import (
	"context"
	"testing"
	"time"
)

func TestSamplerSampleBuildsValues(t *testing.T) {
	var recorded map[string]float64
	s := &Sampler{
		Store: &fakeMetricsStore{
			record: func(_ context.Context, _ time.Time, values map[string]float64) error {
				recorded = values
				return nil
			},
		},
		Sources: Sources{
			GUIConnections: func() int { return 3 },
			GameSessions:   func() int { return 2 },
			DBPingLatency: func(ctx context.Context) (float64, bool) {
				return 4.5, true
			},
			DBPoolStats: func() (int, int, int) { return 5, 1, 4 },
		},
	}
	s.sample(context.Background())
	if recorded["gui_connections"] != 3 || recorded["game_sessions"] != 2 {
		t.Fatalf("%v", recorded)
	}
	if recorded["db_latency_ms"] != 4.5 || recorded["db_open_connections"] != 5 {
		t.Fatalf("%v", recorded)
	}
}

type fakeMetricsStore struct {
	record func(ctx context.Context, ts time.Time, values map[string]float64) error
}

func (f *fakeMetricsStore) RecordMetricSamples(ctx context.Context, ts time.Time, values map[string]float64) error {
	if f.record != nil {
		return f.record(ctx, ts, values)
	}
	return nil
}

func (f *fakeMetricsStore) PurgeMetricsOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}
