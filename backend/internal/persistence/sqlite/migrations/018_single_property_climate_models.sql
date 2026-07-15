-- Temperature and humidity are no longer separate unified device models.
-- Both legacy types migrate to one generic numeric sensor/value contract;
-- the HomeKit temperature/humidity meaning is selected by Consumer mapping.
DELETE FROM mapping_bindings
WHERE device_type IN ('temperature-sensor', 'humidity-sensor')
  AND model_endpoint_id = 'main'
  AND model_capability_id = 'security'
  AND model_property_id = 'tampered';

UPDATE mapping_bindings
SET model_capability_id = 'sensor', model_property_id = 'value'
WHERE device_type = 'temperature-sensor'
  AND model_endpoint_id = 'main'
  AND model_capability_id = 'temperature'
  AND model_property_id = 'current-temperature';

UPDATE mapping_bindings
SET model_capability_id = 'sensor', model_property_id = 'value'
WHERE device_type = 'humidity-sensor'
  AND model_endpoint_id = 'main'
  AND model_capability_id = 'humidity'
  AND model_property_id = 'current-humidity';

UPDATE mapping_bindings
SET device_type = 'single-property-sensor'
WHERE device_type IN ('temperature-sensor', 'humidity-sensor');

UPDATE target_virtual_devices
SET type = 'single-property-sensor'
WHERE type IN ('temperature-sensor', 'humidity-sensor');

-- If both former models defined the same custom path, keep one row before
-- merging their device_type unique-key namespace.
DELETE FROM custom_model_properties AS humidity
WHERE humidity.device_type = 'humidity-sensor'
  AND EXISTS (
    SELECT 1 FROM custom_model_properties AS temperature
    WHERE temperature.device_type = 'temperature-sensor'
      AND temperature.endpoint_id = humidity.endpoint_id
      AND temperature.capability_id = humidity.capability_id
      AND temperature.property_id = humidity.property_id
  );

UPDATE custom_model_properties
SET device_type = 'single-property-sensor',
    document_json = json_set(document_json, '$.deviceType', 'single-property-sensor')
WHERE device_type IN ('temperature-sensor', 'humidity-sensor');
