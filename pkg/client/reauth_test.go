package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsNotAuthenticatedError(t *testing.T) {
	rpc := func(message, data string) error {
		return &RPCError{Code: -1, Message: message, Data: json.RawMessage(data)}
	}
	for _, tc := range []BoolTestCase{
		{Name: "nil", Input: nil},
		{Name: "typed nil RPC", Input: (*RPCError)(nil)},
		{Name: "generic method code with errno", Input: &RPCError{Code: -32001, Message: "Method call error", Data: json.RawMessage(`{"error":207,"errname":"ENOTAUTHENTICATED"}`)}, Expected: true},
		{Name: "errno", Input: rpc("Call error", `{"error":207}`), Expected: true},
		{Name: "errname", Input: rpc("Call error", `{"errname":"ENOTAUTHENTICATED"}`), Expected: true},
		{Name: "wrapped errno", Input: fmt.Errorf("query: %w", rpc("Call error", `{"error":207}`)), Expected: true},
		{Name: "wrapped errname", Input: fmt.Errorf("query: %w", rpc("Call error", `{"errname":"ENOTAUTHENTICATED"}`)), Expected: true},
		{Name: "message fallback", Input: rpc("[ENOTAUTHENTICATED] Session expired", ""), Expected: true},
		{Name: "lowercase message fallback", Input: rpc("[enotauthenticated] Session expired", ""), Expected: true},
		{Name: "wrapped fallback", Input: fmt.Errorf("query: %w", rpc("ENOTAUTHENTICATED", "")), Expected: true},
		{Name: "malformed data fallback", Input: rpc("ENOTAUTHENTICATED", "{"), Expected: true},
		{Name: "non RPC name", Input: errors.New("ENOTAUTHENTICATED")},
		{Name: "wrapper text only", Input: fmt.Errorf("ENOTAUTHENTICATED: %w", rpc("Permission denied", ""))},
		{Name: "permission denied", Input: rpc("Permission denied", `{"error":13,"errname":"EACCES"}`)},
		{Name: "operation not permitted", Input: rpc("Operation not permitted", `{"error":1,"errname":"EPERM"}`)},
		{Name: "generic authentication text", Input: rpc("Authentication failed", "")},
		{Name: "generic unauthenticated text", Input: rpc("Not authenticated", "")},
		{Name: "top level code is not errno", Input: &RPCError{Code: 207, Message: "Call error"}},
		{Name: "unstructured data", Input: rpc("Call error", `"ENOTAUTHENTICATED"`)},
		{Name: "unrelated data field", Input: rpc("Call error", `{"reason":"ENOTAUTHENTICATED","error":13}`)},
		{Name: "nested unrelated errno", Input: rpc("Call error", `{"extra":{"error":207}}`)},
		{Name: "malformed data", Input: rpc("Call error", "{")},
		{Name: "null data", Input: rpc("Call error", "null")},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			assertEqual(t, IsNotAuthenticatedError(tc.Input), tc.Expected)
		})
	}
}

func reauthRPCError() *RPCError {
	return &RPCError{Code: -1, Message: "Session expired", Data: json.RawMessage(`{"error":207,"errname":"ENOTAUTHENTICATED"}`)}
}

func assertReauthRPCError(t *testing.T, err error, want *RPCError) {
	t.Helper()
	var got *RPCError
	if !errors.As(err, &got) {
		t.Fatalf("expected original authentication RPC error, got %v", err)
	}
	assertEqual(t, got.Code, want.Code)
	assertEqual(t, got.Message, want.Message)
	assertEqual(t, string(got.Data), string(want.Data))
}

func newReauthClient(t *testing.T, timeout time.Duration, metrics MetricsRecorder) (*Client, *MockTrueNASServer) {
	t.Helper()
	mock := NewMockTrueNASServer()
	t.Cleanup(mock.Close)
	client := New(Config{
		URL: mock.URL, APIKey: "test-api-key", CallTimeout: timeout,
		PingInterval: time.Hour, ReconnectMin: time.Millisecond, ReconnectMax: time.Millisecond,
		MaxReconnectAttempts: 1, Metrics: metrics,
	})
	t.Cleanup(func() { _ = client.Close() })
	assertNoError(t, client.Connect(testContext(t)))
	return client, mock
}

func TestCall_ReauthRecovery(t *testing.T) {
	for _, rejection := range []*RPCError{
		{Code: -32001, Message: "Method call error", Data: json.RawMessage(`{"error":207,"errname":"ENOTAUTHENTICATED"}`)},
		{Code: -1, Message: "Call error", Data: json.RawMessage(`{"error":207}`)},
		{Code: -1, Message: "Call error", Data: json.RawMessage(`{"errname":"ENOTAUTHENTICATED"}`)},
		{Code: -1, Message: "[ENOTAUTHENTICATED] Session expired"},
	} {
		t.Run(rejection.Error(), func(t *testing.T) {
			metrics := newReauthMetrics()
			client, mock := newReauthClient(t, testTimeout, metrics)
			client.connMu.RLock()
			oldConn := client.conn
			client.connMu.RUnlock()
			var attempts atomic.Int32
			mock.SetResponseFunc(func(_ string, _ json.RawMessage) MockResponse {
				if attempts.Add(1) == 1 {
					return MockResponse{Error: rejection}
				}
				return MockResponse{Result: "recovered"}
			})

			var result string
			assertNoError(t, client.Call(testContext(t), "test.reauth", []any{"unchanged", 42}, &result))
			assertEqual(t, result, "recovered")
			assertEqual(t, mock.ConnectionCount(), 2)
			requests := mock.GetRequestsByMethod("test.reauth")
			assertLen(t, requests, 2)
			assertEqual(t, string(requests[0].Params), string(requests[1].Params))
			client.connMu.RLock()
			newConn := client.conn
			client.connMu.RUnlock()
			assertNotNil(t, newConn)
			assertTrue(t, newConn != oldConn)

			metrics.waitFor(t, "reconnect:success")
			assertNoError(t, client.Close())
			events := metrics.snapshot()
			counts := make(map[string]int)
			var requestsObserved []string
			for _, event := range events {
				counts[event]++
				if strings.HasPrefix(event, "request:") {
					requestsObserved = append(requestsObserved, event)
				}
			}
			assertEqual(t, strings.Join(requestsObserved, ","), "request:test.reauth:error,request:test.reauth:success")
			assertEqual(t, counts["attempt:success"], 2)
			assertEqual(t, counts["attempt:failure"], 0)
			assertEqual(t, counts["connection:true"], 2)
			assertEqual(t, counts["connection:false"], 2) // Expiry and explicit Close.
			assertEqual(t, counts["reconnect:success"], 1)
			assertEqual(t, counts["reconnect:failure"], 0)
		})
	}
}

func TestCall_ReauthConcurrentExpiry(t *testing.T) {
	const callers = 4
	entered, release := make(chan struct{}, callers), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	metrics := &reauthHookMetrics{
		reauthMetrics: newReauthMetrics(),
		hook: func(method, status string) {
			if method == "test.concurrent" && status == "error" {
				entered <- struct{}{}
				<-release
			}
		},
	}
	client, mock := newReauthClient(t, testTimeout, metrics)
	t.Cleanup(unblock)
	var attempts atomic.Int32
	mock.SetResponseFunc(func(_ string, params json.RawMessage) MockResponse {
		if attempts.Add(1) <= callers {
			return MockResponse{Error: reauthRPCError()}
		}
		var values []int
		if err := json.Unmarshal(params, &values); err != nil || len(values) != 1 {
			return MockResponse{Error: &RPCError{Code: -32602, Message: "invalid test parameters"}}
		}
		return MockResponse{Result: values[0]}
	})

	ctx := testContext(t)
	finished := make(chan error, callers)
	for id := range callers {
		go func() {
			var got int
			err := client.Call(ctx, "test.concurrent", []int{id}, &got)
			if err == nil && got != id {
				err = fmt.Errorf("result = %d, want %d", got, id)
			}
			finished <- err
		}()
	}
	// Deliver all expiry responses before any caller initiates recovery, then
	// release them together to exercise competing handlers for the same socket.
	for range callers {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("not all callers received their expiry response")
		}
	}
	unblock()
	for range callers {
		select {
		case err := <-finished:
			assertNoError(t, err)
		case <-ctx.Done():
			t.Fatal("concurrent authentication recovery did not finish")
		}
	}
	assertEqual(t, mock.ConnectionCount(), 2)
	assertRequestCount(t, mock, "test.concurrent", 2*callers)
	assertTrue(t, client.Connected())
}

func TestCall_ReauthPersistentRejection(t *testing.T) {
	client, mock := newReauthClient(t, testTimeout, nil)
	rejection := reauthRPCError()
	mock.SetResponse("test.reauth", MockResponse{Error: rejection})
	// Each Call has its own one-retry allowance, not a client-wide budget.
	for call := 1; call <= 2; call++ {
		err := client.Call(testContext(t), "test.reauth", nil, nil)
		assertReauthRPCError(t, err, rejection)
		assertRequestCount(t, mock, "test.reauth", 2*call)
		assertEqual(t, mock.ConnectionCount(), call+1)
	}
}

func TestCall_ReauthIgnoresPermissionErrors(t *testing.T) {
	client, mock := newReauthClient(t, testTimeout, nil)
	for _, rejection := range []*RPCError{
		{Code: -32001, Message: "Method call error", Data: json.RawMessage(`{"error":13,"errname":"EACCES"}`)},
		{Code: -1, Message: "Permission denied", Data: json.RawMessage(`{"error":13,"errname":"EACCES"}`)},
		{Code: -1, Message: "Operation not permitted", Data: json.RawMessage(`{"error":1,"errname":"EPERM"}`)},
		{Code: -1, Message: "Authentication failed"},
	} {
		t.Run(rejection.Message, func(t *testing.T) {
			mock.ClearRequests()
			mock.SetResponse("test.reauth", MockResponse{Error: rejection})
			err := client.Call(testContext(t), "test.reauth", nil, nil)
			assertReauthRPCError(t, err, rejection)
			assertRequestCount(t, mock, "test.reauth", 1)
			assertEqual(t, mock.ConnectionCount(), 1)
			assertTrue(t, client.Connected())
		})
	}
}

func TestCall_ReauthRecoveryInterrupted(t *testing.T) {
	for _, interrupt := range []string{"cancel", "close"} {
		t.Run(interrupt, func(t *testing.T) {
			metrics := newReauthMetrics()
			client, mock := newReauthClient(t, testTimeout, metrics)
			rejection := reauthRPCError()
			mock.SetResponse("test.reauth", MockResponse{Error: rejection})
			mock.SetAuthFailure(true) // Initial login succeeded; only replacement login fails.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- client.Call(ctx, "test.reauth", nil, nil) }()

			// This event proves Call received the RPC error and recovery has started.
			metrics.waitFor(t, "reconnect:failure")
			if interrupt == "cancel" {
				cancel()
			} else {
				assertNoError(t, client.Close())
			}
			select {
			case err := <-finished:
				assertReauthRPCError(t, err, rejection)
			case <-time.After(time.Second):
				t.Fatal("Call did not promptly return after recovery was interrupted")
			}
			assertRequestCount(t, mock, "test.reauth", 1)
			assertEqual(t, mock.ConnectionCount(), 2)
		})
	}
}

func TestCall_ReauthFailedLoginBounded(t *testing.T) {
	const timeout = 250 * time.Millisecond
	metrics := newReauthMetrics()
	client, mock := newReauthClient(t, timeout, metrics)
	rejection := reauthRPCError()
	mock.SetResponse("test.reauth", MockResponse{Error: rejection})
	mock.SetAuthFailure(true)
	finished := make(chan error, 1)
	started := time.Now()
	go func() { finished <- client.Call(context.Background(), "test.reauth", nil, nil) }()
	select {
	case err := <-finished:
		assertReauthRPCError(t, err, rejection)
		if elapsed := time.Since(started); elapsed < timeout {
			t.Fatalf("Call returned before its recovery timeout: %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Background Call exceeded its bounded authentication recovery wait")
	}
	metrics.waitFor(t, "reconnect:failure")
	assertRequestCount(t, mock, "test.reauth", 1)
	assertEqual(t, mock.ConnectionCount(), 2)
	assertFalse(t, client.Connected())
}

func TestWaitForJob_ReauthResumesPolling(t *testing.T) {
	client, mock := newReauthClient(t, testTimeout, nil)
	var polls atomic.Int32
	mock.SetResponseFunc(func(method string, _ json.RawMessage) MockResponse {
		if method == methodReplicationRunOnetime {
			return MockResponse{Result: 42}
		}
		switch polls.Add(1) {
		case 1:
			return MockResponse{Result: []Job{{ID: 42, State: "RUNNING"}}}
		case 2:
			return MockResponse{Error: reauthRPCError()}
		default:
			return MockResponse{Result: []Job{{ID: 42, State: "SUCCESS"}}}
		}
	})
	ctx := testContext(t)
	id, err := client.RunReplicationOnetime(ctx, &ReplicationRunOnetimeOptions{
		Direction: "PUSH", Transport: "LOCAL", SourceDatasets: []string{"tank/source"}, TargetDataset: "tank/target",
	})
	assertNoError(t, err)
	job, err := client.WaitForJob(ctx, id, time.Millisecond)
	assertNoError(t, err)
	assertEqual(t, job.ID, id)
	assertEqual(t, job.State, "SUCCESS")
	assertRequestCount(t, mock, methodReplicationRunOnetime, 1)
	requests := mock.GetRequestsByMethod(methodCoreGetJobs)
	assertLen(t, requests, 3)
	for _, req := range requests {
		var params []json.RawMessage
		assertNoError(t, json.Unmarshal(req.Params, &params))
		assertEqual(t, string(params[0]), `[["id","=",42]]`)
	}
	assertEqual(t, mock.ConnectionCount(), 2)
}

func TestCall_ReauthStaleDisconnectPreservesPendingRequest(t *testing.T) {
	client, mock := newReauthClient(t, testTimeout, nil)
	client.connMu.RLock()
	oldConn := client.conn
	client.connMu.RUnlock()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock() // Release the mock's RLock even if an assertion fails.
	var attempts atomic.Int32
	mock.SetResponseFunc(func(method string, _ json.RawMessage) MockResponse {
		if method == "test.pending" {
			close(entered)
			<-release
		} else if attempts.Add(1) == 1 {
			return MockResponse{Error: reauthRPCError()}
		}
		return MockResponse{Result: "ok"}
	})
	assertNoError(t, client.Call(testContext(t), "test.reauth", nil, nil))
	client.connMu.RLock()
	newConn, newDone := client.conn, client.connDone
	client.connMu.RUnlock()
	assertTrue(t, newConn != oldConn)
	ctx := testContext(t)
	finished := make(chan error, 1)
	go func() { finished <- client.Call(ctx, "test.pending", nil, nil) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("request never reached the replacement connection")
	}
	client.handleDisconnect(oldConn)
	select {
	case <-newDone:
		t.Fatal("stale disconnect stopped the replacement connection")
	default:
	}
	unblock()
	select {
	case err := <-finished:
		assertNoError(t, err)
	case <-ctx.Done():
		t.Fatal("pending request did not complete after stale disconnect")
	}
	assertTrue(t, client.Connected())
	assertRequestCount(t, mock, "test.pending", 1)
	assertEqual(t, mock.ConnectionCount(), 2)
}

func TestCall_ReauthCanceledAfterReplacement(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	metrics := &reauthHookMetrics{
		reauthMetrics: newReauthMetrics(),
		hook: func(method, status string) {
			if method == "test.reauth" && status == "error" {
				pauseOnce.Do(func() {
					close(entered)
					<-release
				})
			}
		},
	}
	client, mock := newReauthClient(t, testTimeout, metrics)
	t.Cleanup(unblock)
	rejection := reauthRPCError()
	mock.SetResponse("test.reauth", MockResponse{Error: rejection})
	client.connMu.RLock()
	oldConn := client.conn
	client.connMu.RUnlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- client.Call(ctx, "test.reauth", nil, nil) }()
	select {
	case <-entered:
	case <-time.After(testTimeout):
		t.Fatal("Call never reached the authentication-error metrics handoff")
	}

	// Replace the connection while Call still holds the old connection's auth error.
	client.handleDisconnect(oldConn)
	metrics.waitFor(t, "reconnect:success")
	client.connMu.RLock()
	replacement, replacementDone := client.conn, client.connDone
	client.connMu.RUnlock()
	assertNotNil(t, replacement)
	assertTrue(t, replacement != oldConn)
	cancel()
	unblock()
	select {
	case err := <-finished:
		assertReauthRPCError(t, err, rejection)
	case <-time.After(time.Second):
		t.Fatal("canceled Call did not return promptly at the recovery handoff")
	}

	assertRequestCount(t, mock, "test.reauth", 1)
	var requestsObserved []string
	for _, event := range metrics.snapshot() {
		if strings.HasPrefix(event, "request:") {
			requestsObserved = append(requestsObserved, event)
		}
	}
	assertEqual(t, strings.Join(requestsObserved, ","), "request:test.reauth:error")
	assertEqual(t, mock.ConnectionCount(), 2)
	assertTrue(t, client.Connected())
	client.connMu.RLock()
	current := client.conn
	client.connMu.RUnlock()
	assertEqual(t, current, replacement)
	select {
	case <-replacementDone:
		t.Fatal("canceled Call disconnected the replacement connection")
	default:
	}
}

type reauthHookMetrics struct {
	*reauthMetrics
	hook func(method, status string)
}

func (m *reauthHookMetrics) ObserveRequest(method, status string, duration time.Duration) {
	m.reauthMetrics.ObserveRequest(method, status, duration)
	m.hook(method, status)
}

// Record all callbacks safely; changes synchronizes tests with the reconnect goroutine.
type reauthMetrics struct {
	mu      sync.Mutex
	events  []string
	changes chan struct{}
}

func newReauthMetrics() *reauthMetrics {
	return &reauthMetrics{changes: make(chan struct{}, 1)}
}

func (m *reauthMetrics) record(event string) {
	m.mu.Lock()
	m.events = append(m.events, event)
	m.mu.Unlock()
	select {
	case m.changes <- struct{}{}:
	default:
	}
}

func (m *reauthMetrics) ObserveRequest(method, status string, _ time.Duration) {
	m.record("request:" + method + ":" + status)
}
func (m *reauthMetrics) SetConnectionStatus(connected bool) {
	m.record(fmt.Sprintf("connection:%t", connected))
}
func (m *reauthMetrics) ObserveConnectionAttempt(result string) { m.record("attempt:" + result) }
func (m *reauthMetrics) ObserveReconnect(result string)         { m.record("reconnect:" + result) }

func (m *reauthMetrics) snapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.events...)
}

func (m *reauthMetrics) waitFor(t *testing.T, want string) {
	t.Helper()
	ctx := testContext(t)
	for {
		for _, event := range m.snapshot() {
			if event == want {
				return
			}
		}
		select {
		case <-m.changes:
		case <-ctx.Done():
			t.Fatalf("missing metrics event %q; got %v", want, m.snapshot())
		}
	}
}
