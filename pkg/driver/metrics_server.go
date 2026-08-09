package driver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsServer exposes the driver metrics registry over HTTP.
type MetricsServer struct {
	server   *http.Server
	listener net.Listener
	errors   chan error
}

// NewMetricsServer binds a metrics HTTP server to addr. The caller must call
// Start to serve requests and Stop to release it.
func NewMetricsServer(addr string, metrics *Metrics) (*MetricsServer, error) {
	if addr == "" {
		return nil, nil
	}
	if metrics == nil {
		return nil, fmt.Errorf("metrics registry is required")
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen for metrics on %s: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))

	return &MetricsServer{
		server:   &http.Server{Handler: mux},
		listener: listener,
		errors:   make(chan error, 1),
	}, nil
}

// Start serves metrics requests asynchronously after the listener has been
// bound successfully by NewMetricsServer.
func (s *MetricsServer) Start() {
	if s == nil {
		return
	}
	go func() {
		if err := s.server.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.errors <- err
		}
	}()
}

// Stop gracefully shuts down the metrics endpoint.
func (s *MetricsServer) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

// Errors returns fatal HTTP server errors.
func (s *MetricsServer) Errors() <-chan error {
	if s == nil {
		return nil
	}
	return s.errors
}
