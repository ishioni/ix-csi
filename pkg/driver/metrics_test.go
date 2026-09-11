package driver

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMetricsServerReadHeaderTimeout(t *testing.T) {
	server, err := NewMetricsServer("127.0.0.1:0", NewMetrics())
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()

	if got := server.server.ReadHeaderTimeout; got != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want 5s", got)
	}
}

func TestMetricsObserveCSI(t *testing.T) {
	metrics := NewMetrics()

	metrics.ObserveCSI("/csi.v1.Controller/CreateVolume", nil, time.Second)
	metrics.ObserveCSI("/csi.v1.Controller/DeleteVolume", status.Error(codes.Internal, "backend failed"), 2*time.Second)

	if got := testutil.ToFloat64(metrics.operationsTotal.WithLabelValues("CreateVolume", "success", "OK")); got != 1 {
		t.Fatalf("CreateVolume success count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.operationsTotal.WithLabelValues("DeleteVolume", "error", "Internal")); got != 1 {
		t.Fatalf("DeleteVolume error count = %v, want 1", got)
	}

}

func TestMetricsLongOperationBuckets(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveCSI("/csi.v1.Controller/CreateVolume", nil, 90*time.Second)
	metrics.ObserveRequest("pool.dataset.create", "success", 90*time.Second)
	families, err := metrics.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, family := range families {
		switch family.GetName() {
		case "ix_csi_operations_duration_seconds", "ix_csi_truenas_requests_duration_seconds":
			checked++
			want := map[float64]uint64{10: 0, 30: 0, 60: 0, 120: 1, 300: 1}
			for _, bucket := range family.Metric[0].GetHistogram().Bucket {
				if count, ok := want[bucket.GetUpperBound()]; ok {
					if bucket.GetCumulativeCount() != count {
						t.Errorf("%s bucket %v = %d, want %d", family.GetName(), bucket.GetUpperBound(), bucket.GetCumulativeCount(), count)
					}
					delete(want, bucket.GetUpperBound())
				}
			}
			if len(want) != 0 {
				t.Errorf("%s missing buckets: %v", family.GetName(), want)
			}
		}
	}
	if checked != 2 {
		t.Fatalf("found %d latency histograms, want 2", checked)
	}
}

func TestMetricsVolumeOperation(t *testing.T) {
	metrics := NewMetrics()
	metrics.RecordVolumeOperation(ProtocolISCSI, volumeOperationExpand, status.Error(codes.Internal, "backend failed"))
	metrics.RecordVolumeOperation("", volumeOperationDelete, nil)
	if got := testutil.ToFloat64(metrics.volumeOperations.WithLabelValues(ProtocolISCSI, volumeOperationExpand, "error")); got != 1 {
		t.Fatalf("failed expand count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.volumeOperations.WithLabelValues("unknown", volumeOperationDelete, "success")); got != 1 {
		t.Fatalf("unknown protocol delete count = %v, want 1", got)
	}
}

func TestMetricsProvisionedCapacity(t *testing.T) {
	metrics := NewMetrics()

	metrics.SetProvisionedVolume("tank/volume-a", "tank", ProtocolNFS, 10)
	metrics.SetProvisionedVolume("tank/volume-b", "tank", ProtocolISCSI, 20)
	metrics.SetProvisionedVolume("tank/volume-a", "tank", ProtocolNFS, 15)

	if got := testutil.ToFloat64(metrics.capacityBytes.WithLabelValues("tank", ProtocolNFS)); got != 15 {
		t.Fatalf("NFS capacity = %v, want 15", got)
	}
	if got := testutil.ToFloat64(metrics.capacityBytes.WithLabelValues("tank", ProtocolISCSI)); got != 20 {
		t.Fatalf("iSCSI capacity = %v, want 20", got)
	}
	if got := testutil.ToFloat64(metrics.volumeCount.WithLabelValues("tank", ProtocolNFS)); got != 1 {
		t.Fatalf("NFS volume count = %v, want 1", got)
	}

	metrics.RemoveProvisionedVolume("tank/volume-b")
	if got := testutil.ToFloat64(metrics.capacityBytes.WithLabelValues("tank", ProtocolISCSI)); got != 0 {
		t.Fatalf("iSCSI capacity after removal = %v, want 0", got)
	}
}
