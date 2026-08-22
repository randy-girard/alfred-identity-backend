-- +goose Up
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS token_cipher BYTEA;

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN IF EXISTS token_cipher;
