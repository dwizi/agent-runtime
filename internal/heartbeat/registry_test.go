package heartbeat

import (
	"testing"
	"time"
)

func TestSnapshotMarksStaleComponent(t *testing.T) {
	registry := NewRegistry()
	registry.Beat("scheduler", "ok")

	registry.mu.Lock()
	record := registry.components["scheduler"]
	record.lastBeatAt = time.Now().UTC().Add(-3 * time.Minute)
	record.updatedAt = record.lastBeatAt
	registry.components["scheduler"] = record
	registry.mu.Unlock()

	snapshot := registry.Snapshot(60 * time.Second)
	if snapshot.Overall != StateDegraded {
		t.Fatalf("expected degraded overall state, got %s", snapshot.Overall)
	}
	if len(snapshot.Components) != 1 {
		t.Fatalf("expected one component, got %d", len(snapshot.Components))
	}
	if snapshot.Components[0].State != StateStale {
		t.Fatalf("expected stale component state, got %s", snapshot.Components[0].State)
	}
}

func TestSnapshotIdleForDisabledComponents(t *testing.T) {
	registry := NewRegistry()
	registry.Disabled("connector:telegram", "token missing")
	registry.Disabled("connector:discord", "token missing")

	snapshot := registry.Snapshot(60 * time.Second)
	if snapshot.Overall != "idle" {
		t.Fatalf("expected idle overall state, got %s", snapshot.Overall)
	}
}
