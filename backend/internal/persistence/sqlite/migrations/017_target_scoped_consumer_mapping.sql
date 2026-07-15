ALTER TABLE mapping_bindings ADD COLUMN target_id TEXT NOT NULL DEFAULT '';
ALTER TABLE mapping_bindings ADD COLUMN consumer_device_id TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS mapping_consumer_target_unique;

-- Preserve existing per-source Consumer routes by materializing one scoped
-- copy for every bridge virtual device that previously published that source.
INSERT INTO mapping_bindings(
    id, stage, profile_id, provider_id, device_id, endpoint_id, capability_id,
    property_id, device_type, model_endpoint_id, model_capability_id,
    model_property_id, consumer_id, target_id, consumer_device_id,
    consumer_property, enabled, created_at, updated_at
)
SELECT binding.id || '-target-' || lower(hex(randomblob(6))), binding.stage,
       binding.profile_id, binding.provider_id, binding.device_id,
       binding.endpoint_id, binding.capability_id, binding.property_id,
       binding.device_type, binding.model_endpoint_id,
       binding.model_capability_id, binding.model_property_id,
       binding.consumer_id, virtual.target_id, virtual.id,
       binding.consumer_property, binding.enabled,
       binding.created_at, binding.updated_at
FROM mapping_bindings AS binding
JOIN target_virtual_devices AS virtual
  ON virtual.source_device_id = binding.device_id
WHERE binding.stage = 'consumer'
  AND binding.target_id = ''
  AND binding.consumer_device_id = '';

CREATE UNIQUE INDEX mapping_consumer_target_unique
    ON mapping_bindings(provider_id, device_id, target_id, consumer_device_id, consumer_id, consumer_property)
    WHERE stage = 'consumer';
