package driver

import "testing"

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
