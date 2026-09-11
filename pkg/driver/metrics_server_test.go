package driver

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/go-logr/logr"
	"github.com/ishioni/ix-csi/pkg/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

// Publish the metrics server from an RPC handler to synchronize with Run's
// startup writes before the test closes its listener.
type metricsTestIdentity struct {
	*IdentityServer
	ready chan *MetricsServer
	once  sync.Once
}

func (s *metricsTestIdentity) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	s.once.Do(func() { s.ready <- s.driver.metricsServer })
	return s.IdentityServer.GetPluginInfo(ctx, req)
}

func newMetricsTestDriver(endpoint, metricsAddr string) *Driver {
	d := &Driver{
		name:             "metrics-test.csi",
		version:          "test",
		endpoint:         endpoint,
		metricsAddr:      metricsAddr,
		metrics:          NewMetrics(),
		log:              logr.Discard(),
		client:           client.New(client.Config{}),
		controllerServer: &csi.UnimplementedControllerServer{},
		nodeServer:       &csi.UnimplementedNodeServer{},
	}
	d.identityServer = NewIdentityServer(d)
	return d
}

func TestDriverRunMetricsFailure(t *testing.T) {
	for _, failure := range []string{"bind", "serve"} {
		t.Run(failure, func(t *testing.T) {
			metricsAddr := "127.0.0.1:0"
			if failure == "bind" {
				occupied, err := net.Listen("tcp", metricsAddr)
				if err != nil {
					t.Fatal(err)
				}
				defer occupied.Close()
				metricsAddr = occupied.Addr().String()
			}

			endpoint := "unix://" + filepath.Join(t.TempDir(), "csi.sock")
			d := newMetricsTestDriver(endpoint, metricsAddr)
			identity := &metricsTestIdentity{
				IdentityServer: NewIdentityServer(d),
				ready:          make(chan *MetricsServer, 1),
			}
			d.identityServer = identity
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			var runErr error
			go func() {
				runErr = d.Run(ctx)
				close(done)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("Driver.Run did not stop after cancellation")
				}
				d.client.Close()
			})

			conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			checkIdentity := func() {
				t.Helper()
				rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer rpcCancel()
				info, err := csi.NewIdentityClient(conn).GetPluginInfo(rpcCtx, &csi.GetPluginInfoRequest{}, grpc.WaitForReady(true))
				if err != nil {
					t.Fatalf("CSI identity RPC: %v", err)
				}
				if info.Name != d.name || info.VendorVersion != d.version {
					t.Fatalf("unexpected plugin info: %v", info)
				}
			}
			checkIdentity()
			server := <-identity.ready
			if failure == "serve" {
				if server == nil {
					t.Fatal("metrics server did not start")
				}
				// Closing the listener (not shutting down HTTP) makes Serve fail.
				if err := server.listener.Close(); err != nil {
					t.Fatal(err)
				}
			}

			select {
			case <-done:
				t.Fatalf("Driver.Run exited after metrics %s failure: %v", failure, runErr)
			case <-time.After(100 * time.Millisecond):
			}
			checkIdentity()

			cancel()
			select {
			case <-done:
				if runErr != nil {
					t.Fatalf("Driver.Run after cancellation: %v", runErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Driver.Run did not stop after cancellation")
			}
			if !d.client.Closed() {
				t.Fatal("cancellation did not close the TrueNAS client")
			}
		})
	}
}

func TestDriverRunMetricsCSIBindFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	d := newMetricsTestDriver("tcp://"+occupied.Addr().String(), "127.0.0.1:0")
	defer d.client.Close()
	if err := d.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "failed to listen on") {
		t.Fatalf("Driver.Run with occupied CSI address = %v, want CSI listen error", err)
	}
}

func TestMetricsServerServeFailure(t *testing.T) {
	server, err := NewMetricsServer("127.0.0.1:0", NewMetrics())
	if err != nil {
		t.Fatal(err)
	}
	defer server.listener.Close()
	server.Start()
	if err := server.listener.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-server.Errors():
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Serve error = %v, want closed listener error", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("metrics Serve failure was not reported")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Stop(ctx); err != nil {
		t.Fatalf("Stop after Serve failure: %v", err)
	}
}
