package driver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/go-logr/logr"
	"github.com/ishioni/ix-csi/pkg/client"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// idempotencyRPC is one expected read. A strict script catches mutations and
// source lookups/copies even when CreateVolume ignores their errors.
type idempotencyRPC struct {
	method string
	params string
	result any
}

func newIdempotencyController(t *testing.T, calls []idempotencyRPC) (*ControllerServer, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	var mu sync.Mutex
	next := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/versions" {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode([]string{client.MinAPIVersion}); err != nil {
				t.Errorf("write API versions: %v", err)
			}
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		for {
			var req struct {
				ID     uint64 `json:"id"`
				Method string `json:"method"`
				Params any    `json:"params"`
			}
			if err := wsjson.Read(ctx, conn, &req); err != nil {
				return
			}
			resp := map[string]any{"id": req.ID, "jsonrpc": "2.0"}
			if req.Method == "auth.login_with_api_key" {
				resp["result"] = reflect.DeepEqual(req.Params, []any{"test-api-key"})
			} else {
				mu.Lock()
				if next >= len(calls) || req.Method != calls[next].method {
					t.Errorf("unexpected backend RPC #%d: %s (%v)", next+1, req.Method, req.Params)
					resp["error"] = &client.RPCError{Code: -32601, Message: "unexpected test RPC"}
				} else {
					call := calls[next]
					next++
					var wantParams any
					if err := json.Unmarshal([]byte(call.params), &wantParams); err != nil {
						t.Errorf("invalid fixture params for %s: %v", call.method, err)
					} else if !reflect.DeepEqual(req.Params, wantParams) {
						t.Errorf("%s params = %v, want %v", call.method, req.Params, wantParams)
					}
					resp["result"] = call.result
				}
				mu.Unlock()
			}
			if err := wsjson.Write(ctx, conn, resp); err != nil {
				return
			}
		}
	}))
	backend := client.New(client.Config{
		URL:                  "ws" + strings.TrimPrefix(server.URL, "http"),
		APIKey:               "test-api-key",
		CallTimeout:          2 * time.Second,
		PingInterval:         time.Hour,
		MaxReconnectAttempts: 1,
	})
	t.Cleanup(func() {
		_ = backend.Close()
		server.Close()
		mu.Lock()
		defer mu.Unlock()
		if next != len(calls) {
			t.Errorf("consumed %d backend RPCs, want %d", next, len(calls))
		}
	})
	if err := backend.Connect(ctx); err != nil {
		t.Fatalf("connect test client: %v", err)
	}
	return NewControllerServer(&Driver{
		client: backend, log: logr.Discard(), metrics: NewMetrics(),
		defaultPool: "tank", detachedSnapshotParentDataset: "tank/detached",
		// Seed the discovery cache as a running driver would; only the volume's
		// existing export chain needs to be served by this fixture.
		iscsiPortal: "192.0.2.10:3260", iscsiPortalID: 1,
		iscsiBasename: "iqn.2005-10.org.freenas.ctl",
		volumeCaps:    []*csi.VolumeCapability_AccessMode{{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}},
	}), ctx
}

func idempotencyDatasetRPC(protocol string, capacity int64) idempotencyRPC {
	datasetType, capacityProperty := datasetTypeVolume, "volsize"
	if protocol == ProtocolNFS {
		datasetType, capacityProperty = datasetTypeFilesystem, "refquota"
	}
	return idempotencyRPC{
		method: "pool.dataset.get_instance",
		params: `["tank/csi/pvc-existing",{"extra":{"properties":["refquota","volsize","refreservation"],"retrieve_user_props":true}}]`,
		result: map[string]any{
			"id": "tank/csi/pvc-existing", "name": "tank/csi/pvc-existing", "pool": "tank",
			"type": datasetType, "mountpoint": "/mnt/tank/csi/pvc-existing",
			capacityProperty: map[string]any{"parsed": capacity},
		},
	}
}

func idempotencyExistingRPCs(protocol string, capacity int64) []idempotencyRPC {
	calls := []idempotencyRPC{idempotencyDatasetRPC(protocol, capacity)}
	switch protocol {
	case ProtocolISCSI:
		calls = append(calls,
			idempotencyRPC{
				method: "iscsi.extent.query", params: `[[["disk","=","zvol/tank/csi/pvc-existing"]],{}]`,
				result: []client.ISCSIExtent{{ID: 11, Name: "existing-extent", Type: "DISK", Disk: "zvol/tank/csi/pvc-existing", Enabled: true}},
			},
			idempotencyRPC{
				method: "iscsi.targetextent.query", params: `[[["extent","=",11]],{}]`,
				result: []client.ISCSITargetExtent{{ID: 12, Target: 13, Extent: 11, LunID: 7}},
			},
			idempotencyRPC{
				method: "iscsi.target.query", params: `[[["id","=",13]],{}]`,
				result: []client.ISCSITarget{{ID: 13, Name: "existing-target", Groups: []client.ISCSITargetGroup{{Portal: 1, AuthMethod: "CHAP", Auth: 14}}}},
			},
		)
	case ProtocolNVMeOF:
		calls = append(calls, idempotencyRPC{
			method: "nvmet.namespace.query", params: `[[["device_path","=","zvol/tank/csi/pvc-existing"]],{}]`,
			result: []client.NVMeNamespace{{
				ID: 21, NSID: 1, DeviceType: client.NVMeDeviceTypeZVOL,
				DevicePath: "zvol/tank/csi/pvc-existing", Enabled: true,
				Subsys: &client.NVMeSubsystem{ID: 22, Name: "existing-subsystem", SubNQN: "nqn.2011-06.com.truenas:existing"},
			}},
		})
	}
	return calls
}

func idempotencyRequest(protocol string, source *csi.VolumeContentSource) *csi.CreateVolumeRequest {
	capability := &csi.VolumeCapability{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{}},
	}
	if protocol != ProtocolNFS {
		capability.AccessType = &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}}
	}
	return &csi.CreateVolumeRequest{
		Name: "pvc-existing", CapacityRange: &csi.CapacityRange{RequiredBytes: 2 * GiB},
		VolumeCapabilities: []*csi.VolumeCapability{capability}, VolumeContentSource: source,
		// Use the default pool so validation does not need an unrelated pool query.
		Parameters: map[string]string{paramProtocol: protocol, paramDatasetPath: "csi", "csi.storage.k8s.io/pvc/name": "existing"},
		Secrets:    map[string]string{paramEncryptionPassphrase: "test-only-passphrase"},
	}
}

func assertIdempotencyInventory(t *testing.T, metrics *Metrics, protocol string, capacity int64, count int) {
	t.Helper()
	metrics.volumeMu.Lock()
	got, exists := metrics.volumes["tank/csi/pvc-existing"]
	inventorySize := len(metrics.volumes)
	metrics.volumeMu.Unlock()
	if inventorySize != count || exists != (count == 1) {
		t.Errorf("inventory size = %d, volume present = %v; want %d", inventorySize, exists, count)
	}
	if count == 1 && got != (provisionedVolume{pool: "tank", protocol: protocol, capacity: capacity}) {
		t.Errorf("inventory entry = %+v, want tank/%s with capacity %d", got, protocol, capacity)
	}
	if got := testutil.ToFloat64(metrics.volumeCount.WithLabelValues("tank", protocol)); got != float64(count) {
		t.Errorf("volume count gauge = %v, want %d", got, count)
	}
	if got := testutil.ToFloat64(metrics.capacityBytes.WithLabelValues("tank", protocol)); got != float64(capacity) {
		t.Errorf("capacity gauge = %v, want %d", got, capacity)
	}
}

func TestCreateVolumeExistingContentSource(t *testing.T) {
	sources := []struct {
		name   string
		source *csi.VolumeContentSource
	}{
		{name: "nil"},
		{name: "native snapshot", source: &csi.VolumeContentSource{Type: &csi.VolumeContentSource_Snapshot{
			Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: "tank/csi/pvc-source@snap-existing"},
		}}},
		{name: "detached snapshot", source: &csi.VolumeContentSource{Type: &csi.VolumeContentSource_Snapshot{
			Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: "tank/csi/pvc-source/snap-existing"},
		}}},
		{name: "volume", source: &csi.VolumeContentSource{Type: &csi.VolumeContentSource_Volume{
			Volume: &csi.VolumeContentSource_VolumeSource{VolumeId: "tank/csi/pvc-source"},
		}}},
	}
	for _, protocol := range []string{ProtocolNFS, ProtocolISCSI, ProtocolNVMeOF} {
		for _, source := range sources {
			t.Run(protocol+"/"+source.name, func(t *testing.T) {
				const capacity = 4 * GiB
				calls := idempotencyExistingRPCs(protocol, capacity)
				controller, ctx := newIdempotencyController(t, append(calls, calls...))
				req := idempotencyRequest(protocol, source.source)
				if protocol == ProtocolISCSI {
					req.Parameters[paramISCSIChapUser] = "existing-user"
					req.Secrets[paramISCSIChapSecret] = "test-chap-secret"
				}
				if protocol == ProtocolNVMeOF {
					req.Parameters[paramNVMeOFHostNQN] = "nqn.2014-08.org.nvmexpress:uuid:existing-host"
					// Mutual auth requires the host key supplied only via Secrets;
					// dropping Secrets before parameter validation must fail this test.
					req.Parameters[paramNVMeOFDHCHAPCtrlKey] = "DHHC-1:01:test-controller:"
					req.Secrets[paramNVMeOFDHCHAPKey] = "DHHC-1:01:test-host:"
				}
				original := proto.Clone(req).(*csi.CreateVolumeRequest)
				wantContext := make(map[string]string, len(req.Parameters)+3)
				for key, value := range req.Parameters {
					wantContext[key] = value
				}
				if protocol == ProtocolISCSI {
					wantContext[PublishContextTargetPortal] = "192.0.2.10:3260"
					wantContext[PublishContextTargetIQN] = "iqn.2005-10.org.freenas.ctl:existing-target"
					wantContext[PublishContextLUN] = "7"
				}
				for attempt := 1; attempt <= 2; attempt++ {
					resp, err := controller.CreateVolume(ctx, req)
					if err != nil {
						t.Fatalf("CreateVolume attempt %d: %v", attempt, err)
					}
					if resp.GetVolume() == nil {
						t.Fatalf("CreateVolume attempt %d returned no volume", attempt)
					}
					volume := resp.Volume
					if volume.VolumeId != "tank/csi/pvc-existing" || volume.CapacityBytes != capacity {
						t.Errorf("volume ID/capacity = %q/%d, want tank/csi/pvc-existing/%d", volume.VolumeId, volume.CapacityBytes, capacity)
					}
					if !proto.Equal(volume.ContentSource, original.VolumeContentSource) {
						t.Errorf("ContentSource = %v, want %v", volume.ContentSource, original.VolumeContentSource)
					}
					// Exact context equality also prevents provisioner secrets leaking
					// into the PV, while retaining inline CHAP parameters.
					if !reflect.DeepEqual(volume.VolumeContext, wantContext) {
						t.Errorf("VolumeContext = %v, want %v", volume.VolumeContext, wantContext)
					}
					if !proto.Equal(req, original) {
						t.Error("CreateVolume mutated its request")
					}
					assertIdempotencyInventory(t, controller.driver.metrics, protocol, capacity, 1)
					if got := testutil.ToFloat64(controller.driver.metrics.volumeOperations.WithLabelValues(protocol, volumeOperationCreate, "success")); got != float64(attempt) {
						t.Errorf("create operation count = %v, want %d", got, attempt)
					}
				}
			})
		}
	}
}

func TestCreateVolumeExistingUnlimitedNFS(t *testing.T) {
	controller, ctx := newIdempotencyController(t, idempotencyExistingRPCs(ProtocolNFS, 0))
	req := idempotencyRequest(ProtocolNFS, nil)
	resp, err := controller.CreateVolume(ctx, req)
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if resp.GetVolume().GetCapacityBytes() != req.CapacityRange.RequiredBytes {
		t.Errorf("capacity = %d, want requested %d", resp.GetVolume().GetCapacityBytes(), req.CapacityRange.RequiredBytes)
	}
	assertIdempotencyInventory(t, controller.driver.metrics, ProtocolNFS, req.CapacityRange.RequiredBytes, 1)
}

func TestCreateVolumeExistingUndersized(t *testing.T) {
	for _, protocol := range []string{ProtocolNFS, ProtocolISCSI, ProtocolNVMeOF} {
		t.Run(protocol, func(t *testing.T) {
			controller, ctx := newIdempotencyController(t, []idempotencyRPC{idempotencyDatasetRPC(protocol, GiB)})
			req := idempotencyRequest(protocol, &csi.VolumeContentSource{Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{VolumeId: "tank/csi/pvc-source"},
			}})
			resp, err := controller.CreateVolume(ctx, req)
			if status.Code(err) != codes.AlreadyExists || resp != nil {
				t.Errorf("CreateVolume = (%v, %v), want nil response and AlreadyExists", resp, err)
			}
			assertIdempotencyInventory(t, controller.driver.metrics, protocol, 0, 0)
			if got := testutil.ToFloat64(controller.driver.metrics.volumeOperations.WithLabelValues(protocol, volumeOperationCreate, "error")); got != 1 {
				t.Errorf("failed create operation count = %v, want 1", got)
			}
		})
	}
}

func TestCreateVolumeNFSBlockBeforeBackendQuery(t *testing.T) {
	// No backend reads are allowed, even if an existing filesystem would make
	// the old early-return path appear successful. Authentication is separate.
	controller, ctx := newIdempotencyController(t, nil)
	req := idempotencyRequest(ProtocolNFS, nil)
	// Keep a mount capability first to ensure every capability is validated.
	req.VolumeCapabilities = append(req.VolumeCapabilities, &csi.VolumeCapability{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
		AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
	})
	resp, err := controller.CreateVolume(ctx, req)
	if status.Code(err) != codes.InvalidArgument || resp != nil {
		t.Errorf("CreateVolume = (%v, %v), want nil response and InvalidArgument", resp, err)
	}
	assertIdempotencyInventory(t, controller.driver.metrics, ProtocolNFS, 0, 0)
}
