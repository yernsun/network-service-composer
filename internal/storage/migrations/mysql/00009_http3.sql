-- +goose Up
ALTER TABLE domains ADD COLUMN http3_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE gateways ADD COLUMN http3_supported BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE gateways ADD COLUMN http3_advertised_port INTEGER NOT NULL DEFAULT 0;
UPDATE gateway_pools SET desired_revision=desired_revision+1,revision=revision+1;

-- +goose Down
ALTER TABLE gateways DROP COLUMN http3_advertised_port;
ALTER TABLE gateways DROP COLUMN http3_supported;
ALTER TABLE domains DROP COLUMN http3_enabled;
