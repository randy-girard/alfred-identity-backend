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

// OverviewMetrics lists series returned to the admin overview charts.
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

// QueryMetricSeries returns averaged bucketed samples since the given time.
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
       avg(value)
FROM metrics_samples
WHERE ts >= $2
GROUP BY metric, bucket
ORDER BY bucket`, bucketSec, since.UTC())
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
		var avg float64
		if err := rows.Scan(&name, &bucketTS, &avg); err != nil {
			return nil, err
		}
		out[name] = append(out[name], MetricPoint{T: bucketTS.UTC(), V: avg})
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
