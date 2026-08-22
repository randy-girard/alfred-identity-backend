-- +goose Up
CREATE TABLE IF NOT EXISTS metrics_samples (
  id BIGSERIAL PRIMARY KEY,
  ts TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  metric TEXT NOT NULL,
  value DOUBLE PRECISION NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_metrics_samples_metric_ts
  ON metrics_samples (metric, ts DESC);

CREATE INDEX IF NOT EXISTS idx_metrics_samples_ts
  ON metrics_samples (ts DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_metrics_samples_ts;
DROP INDEX IF EXISTS idx_metrics_samples_metric_ts;
DROP TABLE IF EXISTS metrics_samples;
