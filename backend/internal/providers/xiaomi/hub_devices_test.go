package xiaomi

import (
	"encoding/json"
	"testing"
)

func TestParseHubDeviceList(t *testing.T) {
	devices, err := parseHubDeviceList(json.RawMessage(`{"code":0,"result":{"list":[{"did":"2","name":"卧室灯","model":"vendor.light.v1","homeId":"main","homeName":"我的家","roomName":"卧室","spec_type":"urn:miot-spec-v2:device:light:0000A001"},{"did":1,"deviceName":"客厅开关","model":"vendor.switch.v1","room_id":"living"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[0].DID != "1" || devices[0].Name != "客厅开关" || devices[1].RoomName != "卧室" || devices[1].SpecType == "" {
		t.Fatalf("unexpected devices: %#v", devices)
	}
	if devices[1].HomeID != "main" || devices[1].HomeName != "我的家" {
		t.Fatalf("unexpected device home: %#v", devices[1])
	}
}

func TestParseHubDeviceListRejectsGatewayError(t *testing.T) {
	if _, err := parseHubDeviceList(json.RawMessage(`{"code":-1,"message":"not ready"}`)); err == nil {
		t.Fatal("expected gateway error")
	}
}

func TestParseHubDeviceListAcceptsDirectResultArrayWithDIDOnly(t *testing.T) {
	devices, err := parseHubDeviceList(json.RawMessage(`{"code":0,"result":[{"did":"123"},{"did":456}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[0].DID != "123" || devices[0].Name != "123" || devices[1].DID != "456" {
		t.Fatalf("devices = %#v", devices)
	}
}

func TestParseHubDeviceListAcceptsTopLevelArrayAndFirmwareAliases(t *testing.T) {
	devices, err := parseHubDeviceList(json.RawMessage(`[{"miot_did":"2","devName":"台灯","productModel":"vendor.light.v2","room_did":"room-1","is_online":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Name != "台灯" || devices[0].Model != "vendor.light.v2" || devices[0].RoomID != "room-1" || devices[0].Online == nil || !*devices[0].Online {
		t.Fatalf("devices = %#v", devices)
	}
}

func TestParseHubDeviceListAcceptsEncodedDevList(t *testing.T) {
	devices, err := parseHubDeviceList(json.RawMessage(`{"code":0,"result":"{\"dev_list\":[{\"did\":\"7\",\"device_name\":\"门磁\",\"online\":\"offline\"}]}"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].DID != "7" || devices[0].Name != "门磁" || devices[0].Online == nil || *devices[0].Online {
		t.Fatalf("devices = %#v", devices)
	}
}

func TestParseHubDeviceListAcceptsDIDKeyedDirectory(t *testing.T) {
	devices, err := parseHubDeviceList(json.RawMessage(`{"code":0,"data":{"lumi.123":{"name":"无线开关","model":"lumi.sensor_switch"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].DID != "lumi.123" || devices[0].Name != "无线开关" {
		t.Fatalf("devices = %#v", devices)
	}
}
