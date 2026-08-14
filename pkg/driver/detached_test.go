package driver

import (
	"testing"

	"github.com/ishioni/ix-csi/pkg/client"
)

func TestDetachedBoolParameter(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "true", want: true},
		{value: "TRUE", want: true},
		{value: "false", want: false},
		{value: "", want: false},
	} {
		got, err := detachedBoolParameter(map[string]string{"flag": test.value}, "flag")
		if err != nil {
			t.Fatalf("detachedBoolParameter(%q) returned error: %v", test.value, err)
		}
		if got != test.want {
			t.Errorf("detachedBoolParameter(%q) = %v, want %v", test.value, got, test.want)
		}
	}

	if _, err := detachedBoolParameter(map[string]string{"flag": "yes"}, "flag"); err == nil {
		t.Fatal("detachedBoolParameter accepted an invalid value")
	}
}

func TestDatasetPathsOverlap(t *testing.T) {
	for _, test := range []struct {
		first, second string
		overlap       bool
	}{
		{first: "tank/csi", second: "tank/csi", overlap: true},
		{first: "tank/csi", second: "tank/csi/volumes", overlap: true},
		{first: "tank/csi/volumes", second: "tank/csi", overlap: true},
		{first: "tank/csi", second: "tank/csi-other", overlap: false},
		{first: "tank/csi", second: "tank/other", overlap: false},
	} {
		if got := datasetPathsOverlap(test.first, test.second); got != test.overlap {
			t.Errorf("datasetPathsOverlap(%q, %q) = %v, want %v", test.first, test.second, got, test.overlap)
		}
	}
}

func TestValidateDetachedVolumePaths(t *testing.T) {
	if err := validateDetachedVolumePaths("tank/volumes/source", "tank/volumes/target"); err != nil {
		t.Fatalf("validateDetachedVolumePaths returned an unexpected error: %v", err)
	}

	for _, test := range []struct {
		name   string
		source string
		target string
	}{
		{name: "source overlaps target", source: "tank/volumes", target: "tank/volumes/target"},
		{name: "invalid source", source: "tank/volumes/../source", target: "tank/volumes/target"},
		{name: "invalid target", source: "tank/volumes/source", target: "tank/volumes/../target"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateDetachedVolumePaths(test.source, test.target); err == nil {
				t.Fatalf("validateDetachedVolumePaths(%q, %q) accepted invalid paths", test.source, test.target)
			}
		})
	}
}

func TestDetachedSnapshotParts(t *testing.T) {
	source, name, err := detachedSnapshotParts("tank/volumes/pvc-a/daily")
	if err != nil {
		t.Fatalf("detachedSnapshotParts returned error: %v", err)
	}
	if source != "tank/volumes/pvc-a" || name != "daily" {
		t.Fatalf("detachedSnapshotParts returned %q/%q", source, name)
	}

	for _, id := range []string{"tank/volumes/pvc-a@daily", "tank/volumes/pvc-a/", "../pvc-a/daily", "tank/volumes/../pvc-a/daily"} {
		if _, _, err := detachedSnapshotParts(id); err == nil {
			t.Errorf("detachedSnapshotParts accepted invalid ID %q", id)
		}
	}
}

func TestDetachedSnapshotDataset(t *testing.T) {
	dataset, err := detachedSnapshotDataset("tank/csi-detached", "tank/volumes/pvc-a/daily")
	if err != nil {
		t.Fatalf("detachedSnapshotDataset returned error: %v", err)
	}
	if want := "tank/csi-detached/tank/volumes/pvc-a/daily"; dataset != want {
		t.Fatalf("detachedSnapshotDataset = %q, want %q", dataset, want)
	}
}

func TestIsDetachedSnapshotDataset(t *testing.T) {
	if isDetachedSnapshotDataset(nil) {
		t.Fatal("isDetachedSnapshotDataset(nil) = true")
	}

	for _, test := range []struct {
		name       string
		properties map[string]string
		want       bool
	}{
		{name: "unmarked", properties: nil, want: false},
		{name: "wrong value", properties: map[string]string{detachedSnapshotProperty: "false"}, want: false},
		{name: "marked", properties: map[string]string{detachedSnapshotProperty: detachedSnapshotPropertyValue}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := isDetachedSnapshotDataset(&client.Dataset{UserProperties: test.properties})
			if got != test.want {
				t.Fatalf("isDetachedSnapshotDataset() = %v, want %v", got, test.want)
			}
		})
	}
}
