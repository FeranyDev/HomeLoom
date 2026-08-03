package lan

import (
	"encoding/json"
	"testing"
)

func TestJoinTXTDataConcatenatesData1ToData4(t *testing.T) {
	txt := map[string]string{
		"data3": `"b":2}`,
		"data1": `{"a":1,`,
		"data4": "",
		"data2": `"c":3,`,
	}
	got := JoinTXTData(txt)
	if got != `{"a":1,"c":3,"b":2}` {
		t.Fatalf("JoinTXTData() = %q", got)
	}
	if got := JoinTXTData("data1={", `"switch":"on"`, "}", ""); got != `{"switch":"on"}` {
		t.Fatalf("positional JoinTXTData() = %q", got)
	}
	if got := JoinTXTData([]string{"data2=two", "data1=one"}); got != "onetwo" {
		t.Fatalf("record JoinTXTData() = %q", got)
	}
}

func TestParseAndDecodeServiceData(t *testing.T) {
	service := Service{
		Address: "192.0.2.10",
		Port:    8081,
		TXT: map[string]string{
			"id":      "1000abcd",
			"type":    "diy_plug",
			"apivers": "1",
			"seq":     "7",
			"encrypt": "false",
			"data1":   `{"switch":"on"}`,
		},
	}
	device, err := ParseService(service)
	if err != nil {
		t.Fatal(err)
	}
	if device.DeviceID != "1000abcd" || device.Host != "192.0.2.10" || device.Port != 8081 || device.Encrypt {
		t.Fatalf("parsed device = %+v", device)
	}
	data, err := DecodeServiceData(service, "")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil || decoded["switch"] != "on" {
		t.Fatalf("decoded service data = %s, %v", data, err)
	}
}
