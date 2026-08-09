package driver

import "testing"

func TestDatasetPropertiesFromParameters_DescriptionTemplate(t *testing.T) {
	properties, err := datasetPropertiesFromParameters(map[string]string{
		paramDatasetDescription:            "{{ .pvc.namespace }}/{{ .pvc.name }}",
		"protocol":                         ProtocolNFS,
		"csi.storage.k8s.io/pvc/name":      "postgres-data",
		"csi.storage.k8s.io/pvc/namespace": "database",
	})
	if err != nil {
		t.Fatalf("datasetPropertiesFromParameters() error = %v", err)
	}

	if got := properties[datasetDescriptionProperty]; got != "database/postgres-data" {
		t.Fatalf("description = %v, want %q", got, "database/postgres-data")
	}
}

func TestDatasetPropertiesFromParameters_ParameterPathAndLiteral(t *testing.T) {
	properties, err := datasetPropertiesFromParameters(map[string]string{
		paramDatasetDescription:       "PVC {{ .parameters.foo }} in {{ index .parameters \"csi.storage.k8s.io/pvc/name\" }}",
		"foo":                         "storage",
		"csi.storage.k8s.io/pvc/name": "pvc-name",
	})
	if err != nil {
		t.Fatalf("datasetPropertiesFromParameters() error = %v", err)
	}

	if got := properties[datasetDescriptionProperty]; got != "PVC storage in pvc-name" {
		t.Fatalf("description = %v, want %q", got, "PVC storage in pvc-name")
	}
}

func TestDatasetPropertiesFromParameters_PVAlias(t *testing.T) {
	properties, err := datasetPropertiesFromParameters(map[string]string{
		paramDatasetDescription:            "{{ .pvc.namespace }}/{{ .pvc.name }} -> {{ .pv.name }}",
		"csi.storage.k8s.io/pvc/name":      "postgres-data",
		"csi.storage.k8s.io/pvc/namespace": "database",
		"csi.storage.k8s.io/pv/name":       "pvc-123",
	})
	if err != nil {
		t.Fatalf("datasetPropertiesFromParameters() error = %v", err)
	}

	if got := properties[datasetDescriptionProperty]; got != "database/postgres-data -> pvc-123" {
		t.Fatalf("description = %v, want %q", got, "database/postgres-data -> pvc-123")
	}
}

func TestDatasetPropertiesFromParameters_ZFSProperties(t *testing.T) {
	properties, err := datasetPropertiesFromParameters(map[string]string{
		"zfs.atime":      "off",
		"zfs.recordsize": "1M",
	})
	if err != nil {
		t.Fatalf("datasetPropertiesFromParameters() error = %v", err)
	}

	want := map[string]any{
		"atime":      "off",
		"recordsize": "1M",
	}
	if len(properties) != len(want) {
		t.Fatalf("got dataset properties %#v, want %#v", properties, want)
	}
	for key, value := range want {
		if properties[key] != value {
			t.Errorf("property %q = %v, want %v", key, properties[key], value)
		}
	}
}

func TestDatasetPropertiesFromParameters_DescriptionOverridesZFSProperty(t *testing.T) {
	properties, err := datasetPropertiesFromParameters(map[string]string{
		"zfs.org.freenas:description": "literal description",
		paramDatasetDescription:       "templated {{ .pvc.name }}",
		"csi.storage.k8s.io/pvc/name": "pvc-name",
	})
	if err != nil {
		t.Fatalf("datasetPropertiesFromParameters() error = %v", err)
	}

	if got := properties[datasetDescriptionProperty]; got != "templated pvc-name" {
		t.Fatalf("description = %v, want %q", got, "templated pvc-name")
	}
}

func TestDatasetPropertiesFromParameters_WithoutDescriptionTemplate(t *testing.T) {
	properties, err := datasetPropertiesFromParameters(map[string]string{
		"protocol": "nfs",
	})
	if err != nil {
		t.Fatalf("datasetPropertiesFromParameters() error = %v", err)
	}
	if len(properties) != 0 {
		t.Fatalf("got dataset properties without description template: %#v", properties)
	}
}

func TestDatasetPropertiesFromParameters_InvalidTemplate(t *testing.T) {
	if _, err := datasetPropertiesFromParameters(map[string]string{
		paramDatasetDescription: "{{ if .parameters.foo }}missing end",
	}); err == nil {
		t.Fatal("datasetPropertiesFromParameters() accepted an invalid template")
	}
}

func TestDatasetDescriptionUpdateOptions(t *testing.T) {
	options, err := datasetDescriptionUpdateOptions(map[string]string{})
	if err != nil {
		t.Fatalf("datasetDescriptionUpdateOptions() error = %v", err)
	}
	if options != nil {
		t.Fatalf("missing description template returned update options: %#v", options)
	}

	options, err = datasetDescriptionUpdateOptions(map[string]string{
		paramDatasetDescription: "{{ .parameters.foo }}",
		"foo":                   "pvc-name",
	})
	if err != nil {
		t.Fatalf("datasetDescriptionUpdateOptions() error = %v", err)
	}
	if options == nil {
		t.Fatal("description template did not return update options")
	}
	if len(options.UserPropertiesUpdate) != 1 {
		t.Fatalf("got %d user properties, want 1", len(options.UserPropertiesUpdate))
	}
	property := options.UserPropertiesUpdate[0]
	if property["key"] != datasetDescriptionProperty {
		t.Fatalf("property key = %q, want %q", property["key"], datasetDescriptionProperty)
	}
	if property["value"] != "pvc-name" {
		t.Fatalf("property value = %q, want %q", property["value"], "pvc-name")
	}
}

func TestBuildZVOLCreateOptionsIncludesOnlyConfiguredDescription(t *testing.T) {
	options, err := (&ControllerServer{}).buildZVOLCreateOptions(
		"tank/volumes/volume",
		1<<30,
		map[string]string{
			paramDatasetDescription: "dataset for {{ .parameters.foo }}",
			"foo":                   "pvc-name",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("buildZVOLCreateOptions() error = %v", err)
	}

	if got := options.Properties[datasetDescriptionProperty]; got != "dataset for pvc-name" {
		t.Fatalf("description = %v, want %q", got, "dataset for pvc-name")
	}
	if len(options.Properties) != 1 {
		t.Fatalf("got unexpected dataset properties: %#v", options.Properties)
	}
}
