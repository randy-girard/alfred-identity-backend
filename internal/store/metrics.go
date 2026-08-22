package store

import (
	"context"
	"fmt"
	"time"
)

const (
	MetricGUIConnections  = "gui_connections"
	MetricGameSessions    = "game_sessions"
	MetricDBLatencyMS     = "db_latency_ms"
	MetricDBOpenConns     = "db_open_connections"
	MetricDBInUseConns    = "db_in_use_connections"
	MetricDBIdleConns     = "db_idle_connections"
)

// CountMetrics are whole-number gauges (connections, sessions, pool size).
var CountMetrics = map[string]bool{
	MetricGUIConnections: true,
	MetricGameSessions:   true,
	MetricDBOpenConns:    true,
	MetricDBInUseConns:   true,
	MetricDBIdleConns:    true,
}

// IsCountMetric reports whether values should be aggregated and shown as integers.
func IsCountMetric(name string) bool {
	return CountMetrics[name]
}
var OverviewMetrics = []string{
	MetricGUIConnections,
	MetricGameSessions,
	MetricDBLatencyMS,
	MetricDBOpenConns,
	MetricDBInUseConns,
	MetricDBIdleConns,
}

type MetricPoint struct {
	T time.Time
	V float64
}

// RecordMetricSamples inserts one sample per metric at ts.
func (s *Store) RecordMetricSamples(ctx context.Context, ts time.Time, values map[string]float64) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("store not ready")
	}
	if len(values) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for name, val := range values {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO metrics_samples (ts, metric, value) VALUES ($1, $2, $3)`,
			ts.UTC(), name, val,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// QueryMetricSeries returns bucketed samples since the given time.
// Count metrics use max per bucket; latency uses average.
func (s *Store) QueryMetricSeries(ctx context.Context, since time.Time, bucket time.Duration) (map[string][]MetricPoint, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("store not ready")
	}
	bucketSec := int64(bucket.Seconds())
	if bucketSec < 1 {
		bucketSec = 60
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT metric,
       to_timestamp((floor(extract(epoch from ts) / $1) * $1)) AT TIME ZONE 'UTC' AS bucket,
       CASE
         WHEN metric IN ($3, $4, $5, $6, $7) THEN max(value)
         ELSE avg(value)
       END
FROM metrics_samples
WHERE ts >= $2
GROUP BY metric, bucket
ORDER BY bucket`,
		bucketSec, since.UTC(),
		MetricGUIConnections, MetricGameSessions,
		MetricDBOpenConns, MetricDBInUseConns, MetricDBIdleConns,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]MetricPoint, len(OverviewMetrics))
	for _, name := range OverviewMetrics {
		out[name] = []MetricPoint{}
	}
	for rows.Next() {
		var name string
		var bucketTS time.Time
		var val float64
		if err := rows.Scan(&name, &bucketTS, &val); err != nil {
			return nil, err
		}
		if IsCountMetric(name) {
			val = float64(int64(val + 0.5)) // round max/sample to whole number
		}
		out[name] = append(out[name], MetricPoint{T: bucketTS.UTC(), V: val})
	}
	return out, rows.Err()
}

// PurgeMetricsOlderThan deletes samples before cutoff.
func (s *Store) PurgeMetricsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	if s == nil || s.DB == nil {
		return 0, fmt.Errorf("store not ready")
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM metrics_samples WHERE ts < $1`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
