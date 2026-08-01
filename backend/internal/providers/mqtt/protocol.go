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

var (
	stateTopicTokens   = []string{"endpointId", "capabilityId", "propertyId"}
	commandTopicTokens = []string{"endpointId", "capabilityId", "operationId"}
)

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

func stateTopicTemplate(prefix, deviceID string) string {
	return fmt.Sprintf("%s/state/%s/{endpointId}/{capabilityId}/{propertyId}", prefix, deviceID)
}

func commandTopicTemplate(prefix, deviceID string) string {
	return fmt.Sprintf("%s/command/%s/{endpointId}/{capabilityId}/{operationId}", prefix, deviceID)
}

type mqttSubscription struct {
	Topic string
	QoS   byte
}

func configuredSubscriptions(items []DeviceConfig) []mqttSubscription {
	result := make([]mqttSubscription, 0, len(items)*3)
	for _, item := range items {
		qos := item.effectiveQoS()
		result = append(result,
			mqttSubscription{Topic: item.Topics.Discovery, QoS: qos},
			mqttSubscription{Topic: item.Topics.Availability, QoS: qos},
			mqttSubscription{Topic: topicSubscription(item.Topics.State), QoS: qos},
		)
	}
	return result
}

func validateExactTopic(topic string) error {
	if topic == "" {
		return errors.New("topic is required")
	}
	if strings.ContainsAny(topic, "+#{}\x00") || hasEmptyTopicLevel(topic) {
		return errors.New("topic must contain non-empty literal levels without wildcards or placeholders")
	}
	return nil
}

func validateTopicTemplate(topic string, required []string) error {
	if topic == "" || strings.ContainsAny(topic, "+#\x00") || hasEmptyTopicLevel(topic) {
		return errors.New("topic template must contain non-empty levels without MQTT wildcards")
	}
	allowed := make(map[string]struct{}, len(required))
	for _, token := range required {
		allowed[token] = struct{}{}
		if strings.Count(topic, "{"+token+"}") != 1 {
			return fmt.Errorf("topic template must contain {%s} exactly once", token)
		}
	}
	for _, level := range strings.Split(topic, "/") {
		if !strings.ContainsAny(level, "{}") {
			continue
		}
		if len(level) < 3 || level[0] != '{' || level[len(level)-1] != '}' {
			return errors.New("placeholders must occupy an entire topic level")
		}
		if _, exists := allowed[level[1:len(level)-1]]; !exists {
			return fmt.Errorf("unknown placeholder %s", level)
		}
	}
	return nil
}

func topicSubscription(template string) string {
	result := template
	for _, token := range append(append([]string(nil), stateTopicTokens...), commandTopicTokens...) {
		result = strings.ReplaceAll(result, "{"+token+"}", "+")
	}
	return result
}

func matchTopicTemplate(template, topic string, tokens []string) (map[string]string, bool) {
	templateParts, topicParts := strings.Split(template, "/"), strings.Split(topic, "/")
	if len(templateParts) != len(topicParts) {
		return nil, false
	}
	values := make(map[string]string, len(tokens))
	allowed := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		allowed[token] = struct{}{}
	}
	for index, expected := range templateParts {
		actual := topicParts[index]
		if len(expected) >= 3 && expected[0] == '{' && expected[len(expected)-1] == '}' {
			token := expected[1 : len(expected)-1]
			if _, exists := allowed[token]; !exists || actual == "" {
				return nil, false
			}
			values[token] = actual
			continue
		}
		if expected != actual {
			return nil, false
		}
	}
	return values, true
}

func renderTopicTemplate(template string, values map[string]string) string {
	result := template
	for token, value := range values {
		result = strings.ReplaceAll(result, "{"+token+"}", value)
	}
	return result
}

func topicFiltersOverlap(left, right string) bool {
	leftParts, rightParts := strings.Split(left, "/"), strings.Split(right, "/")
	if len(leftParts) != len(rightParts) {
		return false
	}
	for index := range leftParts {
		if leftParts[index] != "+" && rightParts[index] != "+" && leftParts[index] != rightParts[index] {
			return false
		}
	}
	return true
}

func validTopicID(value string) bool {
	return device.ValidStableID(value)
}
