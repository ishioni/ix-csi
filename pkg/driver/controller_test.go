package driver

import (
	"testing"

	"github.com/ishioni/ix-csi/pkg/client"
)

func TestHasSourceDependentSnapshot(t *testing.T) {
	managed := client.Snapshot{Properties: map[string]any{
		managedSnapshotProperty: map[string]any{"value": managedSnapshotPropertyValue},
	}}

	if !hasSourceDependentSnapshot([]client.Snapshot{{}, managed}) {
		t.Fatal("hasSourceDependentSnapshot() did not find a managed snapshot")
	}
	if !hasSourceDependentSnapshot([]client.Snapshot{{Name: volumeCloneSnapshotPrefix + "pvc-clone"}}) {
		t.Fatal("hasSourceDependentSnapshot() did not find a legacy clone snapshot")
	}
	if !hasSourceDependentSnapshot([]client.Snapshot{{ID: "tank/csi/pvc-source@" + volumeCloneSnapshotPrefix + "pvc-clone"}}) {
		t.Fatal("hasSourceDependentSnapshot() did not find a legacy clone snapshot by ID")
	}
	if hasSourceDependentSnapshot([]client.Snapshot{{Properties: map[string]any{
		managedSnapshotProperty: map[string]any{"value": "false"},
	}}}) {
		t.Fatal("hasSourceDependentSnapshot() accepted an unmanaged snapshot")
	}
}
