package driver

import (
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/status"
)

const metricsNamespace = "ix_csi"

type provisionedVolume struct {
	pool     string
	protocol string
	capacity int64
}

// Metrics contains the driver-native Prometheus collectors and the in-process
// volume state used to publish capacity gauges.
type Metrics struct {
	registry *prometheus.Registry

	operationsTotal    *prometheus.CounterVec
	operationDuration  *prometheus.HistogramVec
	truenasRequests    *prometheus.CounterVec
	truenasRequestTime *prometheus.HistogramVec
	connectionStatus   prometheus.Gauge
	connectionAttempts *prometheus.CounterVec
	reconnects         *prometheus.CounterVec
	capacityBytes      *prometheus.GaugeVec
	volumeCount        *prometheus.GaugeVec

	volumeMu sync.Mutex
	volumes  map[string]provisionedVolume
}

// NewMetrics creates an isolated registry so multiple drivers can coexist in
// tests without colliding in Prometheus's process-wide default registry.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		operationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "operations_total",
			Help:      "Total number of CSI RPC operations by operation and outcome.",
		}, []string{"operation", "status", "code"}),
		operationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Name:      "operations_duration_seconds",
			Help:      "Duration of CSI RPC operations in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"operation"}),
		truenasRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "truenas_requests_total",
			Help:      "Total number of TrueNAS JSON-RPC requests by method and outcome.",
		}, []string{"method", "status"}),
		truenasRequestTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Name:      "truenas_requests_duration_seconds",
			Help:      "Duration of TrueNAS JSON-RPC requests in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method"}),
		connectionStatus: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "truenas_connection_status",
			Help:      "Whether the driver currently has an authenticated TrueNAS WebSocket connection (1 for connected, 0 otherwise).",
		}),
		connectionAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "truenas_connection_attempts_total",
			Help:      "Total number of TrueNAS WebSocket connection attempts by result.",
		}, []string{"result"}),
		reconnects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Name:      "truenas_reconnects_total",
			Help:      "Total number of TrueNAS WebSocket reconnect attempts by result.",
		}, []string{"result"}),
		capacityBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "provisioned_capacity_bytes",
			Help:      "Capacity of provisioned volumes known by this driver process, grouped by pool and protocol.",
		}, []string{"pool", "protocol"}),
		volumeCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "provisioned_volumes",
			Help:      "Number of provisioned volumes known by this driver process, grouped by pool and protocol.",
		}, []string{"pool", "protocol"}),
		volumes: make(map[string]provisionedVolume),
	}

	m.registry.MustRegister(
		m.operationsTotal,
		m.operationDuration,
		m.truenasRequests,
		m.truenasRequestTime,
		m.connectionStatus,
		m.connectionAttempts,
		m.reconnects,
		m.capacityBytes,
		m.volumeCount,
	)
	return m
}

// Registry returns the isolated Prometheus registry for this driver.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// ObserveCSI records one CSI RPC outcome and its duration.
func (m *Metrics) ObserveCSI(fullMethod string, err error, duration time.Duration) {
	operation := fullMethod
	if index := strings.LastIndexByte(operation, '/'); index >= 0 {
		operation = operation[index+1:]
	}

	code := status.Code(err).String()
	outcome := "success"
	if err != nil {
		outcome = "error"
	}

	m.operationsTotal.WithLabelValues(operation, outcome, code).Inc()
	m.operationDuration.WithLabelValues(operation).Observe(duration.Seconds())
}

// ObserveRequest implements client.MetricsRecorder for TrueNAS requests.
func (m *Metrics) ObserveRequest(method, outcome string, duration time.Duration) {
	m.truenasRequests.WithLabelValues(method, outcome).Inc()
	m.truenasRequestTime.WithLabelValues(method).Observe(duration.Seconds())
}

// SetConnectionStatus implements client.MetricsRecorder.
func (m *Metrics) SetConnectionStatus(connected bool) {
	if connected {
		m.connectionStatus.Set(1)
		return
	}
	m.connectionStatus.Set(0)
}

// ObserveConnectionAttempt implements client.MetricsRecorder.
func (m *Metrics) ObserveConnectionAttempt(result string) {
	m.connectionAttempts.WithLabelValues(result).Inc()
}

// ObserveReconnect implements client.MetricsRecorder.
func (m *Metrics) ObserveReconnect(result string) {
	m.reconnects.WithLabelValues(result).Inc()
}

// SetProvisionedVolume adds or replaces a volume in the capacity gauges.
func (m *Metrics) SetProvisionedVolume(volumeID, pool, protocol string, capacity int64) {
	if pool == "" {
		pool = "unknown"
	}
	if protocol == "" {
		protocol = "unknown"
	}
	if capacity < 0 {
		capacity = 0
	}

	m.volumeMu.Lock()
	defer m.volumeMu.Unlock()

	if previous, ok := m.volumes[volumeID]; ok {
		m.capacityBytes.WithLabelValues(previous.pool, previous.protocol).Add(float64(-previous.capacity))
		m.volumeCount.WithLabelValues(previous.pool, previous.protocol).Dec()
	}

	m.volumes[volumeID] = provisionedVolume{pool: pool, protocol: protocol, capacity: capacity}
	m.capacityBytes.WithLabelValues(pool, protocol).Add(float64(capacity))
	m.volumeCount.WithLabelValues(pool, protocol).Inc()
}

// RemoveProvisionedVolume removes a volume from the capacity gauges.
func (m *Metrics) RemoveProvisionedVolume(volumeID string) {
	m.volumeMu.Lock()
	defer m.volumeMu.Unlock()

	previous, ok := m.volumes[volumeID]
	if !ok {
		return
	}
	delete(m.volumes, volumeID)
	m.capacityBytes.WithLabelValues(previous.pool, previous.protocol).Add(float64(-previous.capacity))
	m.volumeCount.WithLabelValues(previous.pool, previous.protocol).Dec()
}

// RecordProvisionedVolume updates the driver's provisioned-volume gauges.
func (d *Driver) RecordProvisionedVolume(info *VolumeInfo) {
	if d.metrics == nil || info == nil {
		return
	}
	d.metrics.SetProvisionedVolume(info.ID, info.PoolName, info.Protocol, info.CapacityBytes)
}

// RemoveProvisionedVolume removes a volume from the driver's capacity gauges.
func (d *Driver) RemoveProvisionedVolume(volumeID string) {
	if d.metrics == nil {
		return
	}
	d.metrics.RemoveProvisionedVolume(volumeID)
}
