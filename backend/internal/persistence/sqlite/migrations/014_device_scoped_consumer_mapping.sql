DROP INDEX IF EXISTS mapping_consumer_target_unique;

-- The initial Consumer route preview was model-wide and cannot be safely
-- assigned to concrete devices during migration because runtime devices are
-- intentionally not persisted. Remove only those legacy global routes.
DELETE FROM mapping_bindings
WHERE stage = 'consumer' AND (provider_id = '' OR device_id = '');

CREATE UNIQUE INDEX mapping_consumer_target_unique
    ON mapping_bindings(provider_id, device_id, consumer_id, consumer_property)
    WHERE stage = 'consumer';
