package driver

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
