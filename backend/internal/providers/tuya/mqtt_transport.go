package tuya

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/autopaho/queue/memory"
	"github.com/eclipse/paho.golang/paho"
)

func (p *Provider) startMQTT(lifecycle context.Context) {
	p.mu.RLock()
	configured := p.config.MQTT
	timeout := time.Duration(p.config.RequestTimeoutSec) * time.Second
	p.mu.RUnlock()
	if configured == nil || !configured.Enabled {
		return
	}
	done := make(chan struct{})
	p.mu.Lock()
	p.mqttDone = done
	p.mu.Unlock()
	go p.runMQTT(lifecycle, done, *configured, timeout)
}

func (p *Provider) runMQTT(lifecycle context.Context, done chan struct{}, config MQTTConfig, timeout time.Duration) {
	defer close(done)
	if !config.ExpiresAt.IsZero() && !config.ExpiresAt.After(time.Now()) {
		p.recordMQTTError(fmt.Errorf("Tuya MQTT credentials expired at %s", config.ExpiresAt.UTC().Format(time.RFC3339)))
		return
	}
	broker, err := url.Parse(config.URL)
	if err != nil {
		p.recordMQTTError(fmt.Errorf("parse Tuya MQTT URL: %w", err))
		return
	}
	if broker.Scheme == "mqtts" {
		broker.Scheme = "tls"
	}
	var tlsConfig *tls.Config
	if broker.Scheme == "tls" {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: broker.Hostname()}
	}
	clientConfig := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{broker},
		TlsCfg:                        tlsConfig,
		KeepAlive:                     uint16(config.KeepAliveSec),
		ConnectTimeout:                timeout,
		ReconnectBackoff:              autopaho.DefaultExponentialBackoff(),
		Queue:                         memory.New(),
		ConnectUsername:               config.Username,
		ConnectPassword:               []byte(config.Password),
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         3600,
		ClientConfig: paho.ClientConfig{
			ClientID:      config.ClientID,
			PacketTimeout: timeout,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(received paho.PublishReceived) (bool, error) {
				if received.Packet == nil {
					return false, nil
				}
				if err := p.HandleMQTTMessage(received.Packet.Payload); err != nil {
					p.errors.Add(1)
					p.recordMQTTError(err)
				}
				return true, nil
			}},
		},
	}
	clientConfig.OnConnectionUp = func(manager *autopaho.ConnectionManager, _ *paho.Connack) {
		go p.subscribeTuyaMQTT(lifecycle, manager, config.SourceTopic, *config.QoS, timeout)
	}
	clientConfig.OnConnectionDown = func() bool {
		p.mu.Lock()
		p.mqttConnected = false
		p.mu.Unlock()
		p.mqttDisconnects.Add(1)
		return true
	}
	clientConfig.OnConnectError = p.recordMQTTError
	manager, err := autopaho.NewConnection(lifecycle, clientConfig)
	if err != nil {
		p.recordMQTTError(fmt.Errorf("start Tuya MQTT connection: %w", err))
		return
	}
	<-lifecycle.Done()
	disconnectCtx, cancel := context.WithTimeout(context.Background(), timeout)
	_ = manager.Disconnect(disconnectCtx)
	cancel()
}

func (p *Provider) subscribeTuyaMQTT(lifecycle context.Context, manager *autopaho.ConnectionManager, topic string, qos byte, timeout time.Duration) {
	for {
		ctx, cancel := context.WithTimeout(lifecycle, timeout)
		ack, err := manager.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: qos, RetainAsPublished: true}}})
		cancel()
		if err == nil && len(ack.Reasons) == 1 && ack.Reasons[0] < 0x80 {
			p.mu.Lock()
			p.mqttConnected, p.mqttLastError = true, ""
			p.mu.Unlock()
			p.mqttConnections.Add(1)
			return
		}
		if err == nil {
			err = fmt.Errorf("broker rejected topic %q", topic)
		}
		p.recordMQTTError(fmt.Errorf("subscribe Tuya MQTT: %w", err))
		timer := time.NewTimer(time.Second)
		select {
		case <-timer.C:
		case <-lifecycle.Done():
			timer.Stop()
			return
		}
	}
}

func (p *Provider) recordMQTTError(err error) {
	if err == nil {
		return
	}
	message := strings.TrimSpace(err.Error())
	p.mu.Lock()
	p.mqttConnected = false
	p.mqttLastError = message
	p.mu.Unlock()
}
