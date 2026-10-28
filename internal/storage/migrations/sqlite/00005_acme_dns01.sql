-- +goose Up
ALTER TABLE domains ADD COLUMN acme_challenge TEXT NOT NULL DEFAULT '';
ALTER TABLE domains ADD COLUMN dns_provider_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE certificates ADD COLUMN acme_challenge TEXT NOT NULL DEFAULT '';
ALTER TABLE certificates ADD COLUMN dns_provider_ref TEXT NOT NULL DEFAULT '';
UPDATE domains SET acme_challenge = 'http-01' WHERE tls_mode = 'managed';
UPDATE certificates SET acme_challenge = 'http-01' WHERE provider = 'acme';

-- +goose Down
ALTER TABLE certificates DROP COLUMN dns_provider_ref;
ALTER TABLE certificates DROP COLUMN acme_challenge;
ALTER TABLE domains DROP COLUMN dns_provider_ref;
ALTER TABLE domains DROP COLUMN acme_challenge;
