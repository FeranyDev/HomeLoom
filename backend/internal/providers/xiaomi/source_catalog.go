package xiaomi

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
	providersdk "github.com/feranydev/homeloom/backend/internal/provider"
)

type specLoadResult struct {
	configured DeviceConfig
	hub        HubDevice
	document   miotSpecDocument
	fetchedAt  time.Time
	source     string
	err        error
}

func (p *Provider) loadSourceSpecs(ctx context.Context, hubDevices []HubDevice) {
	p.mu.RLock()
	configuredDevices := append([]DeviceConfig(nil), p.config.Devices...)
	resolver := p.resolver
	p.mu.RUnlock()
	byDID := make(map[string]HubDevice, len(hubDevices))
	for _, item := range hubDevices {
		byDID[item.DID] = item
	}
	jobs := make(chan DeviceConfig)
	results := make(chan specLoadResult, len(configuredDevices))
	workerCount := 4
	if len(configuredDevices) < workerCount {
		workerCount = len(configuredDevices)
	}
	var workers sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for configured := range jobs {
				hub := byDID[configured.DID]
				model := hub.Model
				if model == "" {
					model = configured.Model
				}
				if resolver == nil {
					results <- specLoadResult{configured: configured, hub: hub, err: fmt.Errorf("MIoT Spec resolver is unavailable")}
					continue
				}
				document, fetchedAt, source, err := resolver.Resolve(ctx, hub.SpecType, model)
				results <- specLoadResult{configured: configured, hub: hub, document: document, fetchedAt: fetchedAt, source: source, err: err}
			}
		}()
	}
	go func() {
		for _, configured := range configuredDevices {
			jobs <- configured
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	loaded := make([]specLoadResult, 0, len(configuredDevices))
	for result := range results {
		loaded = append(loaded, result)
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].configured.ID < loaded[j].configured.ID })

	p.mu.Lock()
	defer p.mu.Unlock()
	nextDevices := make(map[string]device.Device, len(configuredDevices))
	nextSourceDevices := make(map[string]device.Device, len(configuredDevices))
	nextRawProperties := make(map[string]PropertyMapping)
	nextRawActions := make(map[string]ActionMapping)
	nextCatalog := make(map[string]providersdk.SourceCatalogMetadata, len(configuredDevices))
	for _, result := range loaded {
		item := buildDevice(p.id, result.configured)
		applyCentralStalePolicy(&item, p.config.pollInterval())
		sourceItem := item.Clone()
		model := result.hub.Model
		if model == "" {
			model = result.configured.Model
		}
		metadata := providersdk.SourceCatalogMetadata{Complete: false, Source: "configured-mapping-fallback", Model: model, SpecType: result.hub.SpecType}
		if result.err != nil {
			metadata.Error = result.err.Error()
		} else {
			var properties map[string]PropertyMapping
			var actions map[string]ActionMapping
			sourceItem, properties, actions = mergeMIoTSpec(sourceItem, result.configured, result.document)
			applyCentralStalePolicy(&sourceItem, p.config.pollInterval())
			for key, mapping := range properties {
				nextRawProperties[key] = mapping
			}
			for key, action := range actions {
				nextRawActions[key] = action
			}
			metadata.Complete, metadata.Source, metadata.SpecType, metadata.FetchedAt = true, result.source, result.document.Type, result.fetchedAt
		}
		if previous, ok := p.devices[item.ID]; ok {
			item = preserveDeviceState(item, previous)
		}
		if previous, ok := p.sourceDevices[sourceItem.ID]; ok {
			sourceItem = preserveDeviceState(sourceItem, previous)
		} else {
			sourceItem.Availability, sourceItem.Online = item.Availability, item.Online
			sourceItem.Sequence, sourceItem.LastUpdateAt, sourceItem.RuntimeMode, sourceItem.StateTransport = item.Sequence, item.LastUpdateAt, item.RuntimeMode, item.StateTransport
		}
		nextDevices[item.ID], nextSourceDevices[item.ID], nextCatalog[item.ID] = item, sourceItem, metadata
	}
	p.devices, p.sourceDevices, p.rawProperties, p.rawActions, p.catalog = nextDevices, nextSourceDevices, nextRawProperties, nextRawActions, nextCatalog
}

func (p *Provider) markCatalogError(cause error) {
	p.mu.Lock()
	for id, metadata := range p.catalog {
		metadata.Complete, metadata.Source, metadata.Error = false, "configured-mapping-fallback", cause.Error()
		p.catalog[id] = metadata
	}
	p.mu.Unlock()
}

func (p *Provider) refreshSourceCatalog(ctx context.Context) {
	items, err := p.refreshDirectory(ctx)
	if err != nil {
		p.markCatalogError(err)
		return
	}
	p.loadSourceSpecs(ctx, items)
}

func (p *Provider) SourceCatalog(context.Context) ([]providersdk.SourceCatalogDevice, error) {
	p.mu.RLock()
	result := make([]providersdk.SourceCatalogDevice, 0, len(p.sourceDevices))
	for id, item := range p.sourceDevices {
		metadata := p.catalog[id]
		metadata.Values = make(map[string]providersdk.SourceValueStatus)
		prefix := id + "\x00"
		for key, status := range p.valueStatus {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			parts := strings.Split(strings.TrimPrefix(key, prefix), "\x00")
			if len(parts) != 3 {
				continue
			}
			if !item.IsOnline() {
				status.Available = false
			}
			metadata.Values[providersdk.SourceValueKey(parts[0], parts[1], parts[2])] = status
		}
		result = append(result, providersdk.SourceCatalogDevice{Device: item.Clone(), Catalog: metadata})
	}
	p.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func mergeMIoTSpec(item device.Device, configured DeviceConfig, document miotSpecDocument) (device.Device, map[string]PropertyMapping, map[string]ActionMapping) {
	rawProperties := make(map[string]PropertyMapping)
	rawActions := make(map[string]ActionMapping)
	mappedProperties := make(map[string]struct{})
	for _, mapping := range configured.Properties {
		mappedProperties[miotKey(mapping.SIID, mapping.PIID)] = struct{}{}
	}
	mappedActions := make(map[string]struct{})
	for _, action := range configured.Actions {
		mappedActions[miotKey(action.SIID, action.AIID)] = struct{}{}
	}
	for _, service := range document.Services {
		endpointID, capabilityID := "miot-"+strconv.Itoa(service.IID), "service-"+strconv.Itoa(service.IID)
		endpoint := device.Endpoint{ID: endpointID, Name: displayName(service.Description, service.Type), Type: urnName(service.Type)}
		capability := device.Capability{ID: capabilityID, Type: urnName(service.Type)}
		propertyTypes := make(map[int]device.ValueType, len(service.Properties))
		for _, property := range service.Properties {
			valueType := miotValueType(property)
			propertyTypes[property.IID] = valueType
			if _, alreadyMapped := mappedProperties[miotKey(service.IID, property.IID)]; alreadyMapped {
				continue
			}
			mapping := propertyMappingFromSpec(endpointID, capabilityID, service, property, valueType)
			capability.Properties = append(capability.Properties, device.Property{Definition: definitionFromSpec(mapping), Value: zeroValue(mapping)})
			rawProperties[sourcePropertyKey(configured.ID, endpointID, capabilityID, mapping.PropertyID)] = mapping
		}
		for _, action := range service.Actions {
			if _, alreadyMapped := mappedActions[miotKey(service.IID, action.IID)]; alreadyMapped {
				continue
			}
			parameters := make([]device.CommandParameter, 0, len(action.In))
			parameterIDs := make([]string, 0, len(action.In))
			for _, piid := range action.In {
				id := "property-" + strconv.Itoa(piid)
				valueType := propertyTypes[piid]
				if valueType == "" {
					valueType = device.ValueTypeString
				}
				parameterIDs = append(parameterIDs, id)
				parameters = append(parameters, device.CommandParameter{ID: id, Name: id, Type: valueType, Required: true})
			}
			commandID := "action-" + strconv.Itoa(action.IID)
			capability.Commands = append(capability.Commands, device.CommandDefinition{ID: commandID, Name: displayName(action.Description, action.Type), Parameters: parameters})
			rawActions[sourceActionKey(configured.ID, endpointID, capabilityID, commandID)] = ActionMapping{EndpointID: endpointID, CapabilityID: capabilityID, CommandID: commandID, Name: displayName(action.Description, action.Type), SIID: service.IID, AIID: action.IID, Parameters: parameterIDs}
		}
		for _, event := range service.Events {
			payload := device.ValueTypeString
			if len(event.Arguments) == 1 && propertyTypes[event.Arguments[0]] != "" {
				payload = propertyTypes[event.Arguments[0]]
			}
			capability.Events = append(capability.Events, device.EventDefinition{ID: "event-" + strconv.Itoa(event.IID), Name: displayName(event.Description, event.Type), Payload: payload})
		}
		if len(capability.Properties)+len(capability.Commands)+len(capability.Events) > 0 {
			endpoint.Capabilities = []device.Capability{capability}
			item.Endpoints = append(item.Endpoints, endpoint)
		}
	}
	sort.Slice(item.Endpoints, func(i, j int) bool { return item.Endpoints[i].ID < item.Endpoints[j].ID })
	return item, rawProperties, rawActions
}

func propertyMappingFromSpec(endpointID, capabilityID string, service miotSpecService, property miotSpecProperty, valueType device.ValueType) PropertyMapping {
	readable, notifiable := contains(property.Access, "read"), contains(property.Access, "notify")
	mapping := PropertyMapping{EndpointID: endpointID, CapabilityID: capabilityID, CapabilityType: urnName(service.Type), PropertyID: "property-" + strconv.Itoa(property.IID), Name: displayName(property.Description, property.Type), ValueType: valueType, SIID: service.IID, PIID: property.IID, Unit: property.Unit, Readable: &readable, Writable: contains(property.Access, "write"), Notifiable: &notifiable}
	if len(property.ValueRange) >= 2 {
		minimum, maximum := property.ValueRange[0], property.ValueRange[1]
		mapping.Min, mapping.Max = &minimum, &maximum
		if len(property.ValueRange) >= 3 {
			step := property.ValueRange[2]
			mapping.Step = &step
		}
	}
	if len(property.ValueList) > 0 {
		mapping.Enum = make(map[string]any, len(property.ValueList))
		for _, option := range property.ValueList {
			name := strings.TrimSpace(option.Description)
			if name == "" {
				name = fmt.Sprint(option.Value)
			}
			if _, duplicate := mapping.Enum[name]; duplicate {
				name += "-" + fmt.Sprint(option.Value)
			}
			mapping.Enum[name] = option.Value
		}
	}
	return mapping
}

func definitionFromSpec(mapping PropertyMapping) device.PropertyDefinition {
	readable, notifiable := true, true
	if mapping.Readable != nil {
		readable = *mapping.Readable
	}
	if mapping.Notifiable != nil {
		notifiable = *mapping.Notifiable
	}
	enum := make([]string, 0, len(mapping.Enum))
	for name := range mapping.Enum {
		enum = append(enum, name)
	}
	sort.Strings(enum)
	unit := mapping.Unit
	if unit == "none" {
		unit = ""
	}
	return device.PropertyDefinition{ID: mapping.PropertyID, Name: mapping.Name, Type: mapping.ValueType, Unit: unit, Readable: readable, Writable: mapping.Writable, Notifiable: notifiable, Min: mapping.Min, Max: mapping.Max, Step: mapping.Step, Enum: enum, StaleAfterSeconds: defaultPollInterval * 2}
}

func miotValueType(property miotSpecProperty) device.ValueType {
	if len(property.ValueList) > 0 {
		return device.ValueTypeEnum
	}
	switch strings.ToLower(property.Format) {
	case "bool":
		return device.ValueTypeBool
	case "float", "double":
		return device.ValueTypeNumber
	case "uint8", "uint16", "uint32", "uint64", "int8", "int16", "int32", "int64":
		return device.ValueTypeInt
	default:
		return device.ValueTypeString
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func displayName(description, urn string) string {
	if value := strings.TrimSpace(description); value != "" {
		return value
	}
	return urnName(urn)
}

func miotKey(siid, iid int) string { return strconv.Itoa(siid) + "." + strconv.Itoa(iid) }
func sourcePropertyKey(deviceID, endpointID, capabilityID, propertyID string) string {
	return strings.Join([]string{deviceID, endpointID, capabilityID, propertyID}, "\x00")
}
func sourceActionKey(deviceID, endpointID, capabilityID, commandID string) string {
	return strings.Join([]string{deviceID, endpointID, capabilityID, commandID}, "\x00")
}

var _ providersdk.SourceCataloger = (*Provider)(nil)
