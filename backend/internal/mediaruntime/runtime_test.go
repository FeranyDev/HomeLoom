package runtime

import (
	"testing"

	"github.com/feranydev/homeloom/backend/internal/mediaruntime/contract"
	"github.com/feranydev/homeloom/backend/internal/mediaruntime/worker"
)

type closeAdapter struct {
	replacements int
	lastCount    int
}

func (a *closeAdapter) Replace(streams []contract.StreamSpec) error {
	a.replacements++
	a.lastCount = len(streams)
	return nil
}
func (*closeAdapter) Upsert(contract.StreamSpec) error { return nil }
func (*closeAdapter) Delete(string) error              { return nil }

func TestCloseReplaysEmptyStateToReleaseAllSessions(t *testing.T) {
	adapter := &closeAdapter{}
	manager := worker.NewManager(adapter)
	if _, err := manager.Replay(contract.ReplayParams{
		SchemaVersion: 1, Generation: 1, Revision: 1,
		Streams: []contract.StreamSpec{{
			SchemaVersion: 1, ID: "camera-main", DeviceID: "camera-1",
			Protocol: "rtsp", Profile: "main", Mode: "on_demand",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{manager: manager}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if adapter.replacements != 2 || adapter.lastCount != 0 {
		t.Fatalf("close replacements=%d last=%d", adapter.replacements, adapter.lastCount)
	}
	if runtime.Ready() {
		t.Fatal("closed runtime reported ready")
	}
}
