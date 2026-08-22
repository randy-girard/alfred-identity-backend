-- +goose Up
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS eq_account_id BIGINT;
CREATE INDEX IF NOT EXISTS audit_log_created_at_idx ON audit_log (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS audit_log_account_created_idx ON audit_log (eq_account_id, created_at DESC) WHERE eq_account_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS audit_log_account_created_idx;
DROP INDEX IF EXISTS audit_log_created_at_idx;
ALTER TABLE audit_log DROP COLUMN IF EXISTS eq_account_id;
