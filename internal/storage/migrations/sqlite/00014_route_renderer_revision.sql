-- +goose Up
-- Route rendering changed in v7. Advance the configuration identity so an
-- existing revision cannot be rebuilt with a different snapshot digest.
UPDATE gateway_pools SET desired_revision=desired_revision+1,revision=revision+1;
UPDATE gateways SET desired_revision=(SELECT desired_revision FROM gateway_pools WHERE id=gateways.pool_id),revision=revision+1;

-- +goose Down
SELECT 1;
