package mqtt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/feranydev/homeloom/backend/internal/domain/device"
)

const protocolSchemaVersion = 1

type stateMessage struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Value         *device.PropertyValue `json:"value"`
	Sequence      uint64                `json:"sequence"`
	ObservedAt    time.Time             `json:"observedAt"`
	CorrelationID string                `json:"correlationId,omitempty"`
}

type availabilityMessage struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Availability  device.Availability `json:"availability"`
	Sequence      uint64              `json:"sequence"`
	ObservedAt    time.Time           `json:"observedAt"`
}

type commandMessage struct {
	SchemaVersion  int                             `json:"schemaVersion"`
	Kind           string                          `json:"kind"`
	CorrelationID  string                          `json:"correlationId"`
	IdempotencyKey string                          `json:"idempotencyKey,omitempty"`
	Value          *device.PropertyValue           `json:"value,omitempty"`
	Parameters     map[string]device.PropertyValue `json:"parameters,omitempty"`
	CreatedAt      time.Time                       `json:"createdAt"`
}

type inboundMessage struct {
	topic    string
	payload  []byte
	retained bool
}

func decodeJSON(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func topicParts(topic, prefix string) ([]string, bool) {
	if !strings.HasPrefix(topic, prefix+"/") {
		return nil, false
	}
	suffix := strings.TrimPrefix(topic, prefix+"/")
	if suffix == "" {
		return nil, false
	}
	parts := strings.Split(suffix, "/")
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
	}
	return parts, true
}

func discoveryTopic(prefix, deviceID string) string {
	return fmt.Sprintf("%s/discovery/%s", prefix, deviceID)
}

func availabilityTopic(prefix, deviceID string) string {
	return fmt.Sprintf("%s/availability/%s", prefix, deviceID)
}

func stateTopic(prefix, deviceID, endpointID, capabilityID, propertyID string) string {
	return fmt.Sprintf("%s/state/%s/%s/%s/%s", prefix, deviceID, endpointID, capabilityID, propertyID)
}

func commandTopic(prefix, deviceID, endpointID, capabilityID, operationID string) string {
	return fmt.Sprintf("%s/command/%s/%s/%s/%s", prefix, deviceID, endpointID, capabilityID, operationID)
}

func subscriptionTopics(prefix string) []string {
	return []string{
		prefix + "/discovery/+",
		prefix + "/state/+/+/+/+",
		prefix + "/availability/+",
	}
}
