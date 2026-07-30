package client

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// Test timeout for operations
const testTimeout = 5 * time.Second

// testContext returns a context with a test timeout.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx
}

// newTestClient creates a Client configured to connect to the mock server.
func newTestClient(mock *MockTrueNASServer) *Client {
	return New(Config{
		URL:          mock.URL,
		APIKey:       "test-api-key",
		CallTimeout:  testTimeout,
		PingInterval: 1 * time.Hour, // Disable ping during tests
	})
}

// connectTestClient creates and connects a client to the mock server.
func connectTestClient(t *testing.T, mock *MockTrueNASServer) *Client {
	t.Helper()
	client := newTestClient(mock)
	if err := client.Connect(testContext(t)); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	t.Cleanup(func() {
		client.Close()
	})
	return client
}

// assertNoError fails the test if err is not nil.
func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertError fails the test if err is nil.
func assertError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// assertErrorContains fails if err is nil or doesn't contain the substring.
func assertErrorContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got: %v", substr, err)
	}
}

// assertEqual fails if got != want.
func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// assertNil fails if got is not nil.
func assertNil(t *testing.T, got any) {
	t.Helper()
	if got == nil {
		return
	}
	// Handle interface containing nil pointer
	v := reflect.ValueOf(got)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return
	}
	t.Fatalf("expected nil, got %v", got)
}

// assertNotNil fails if got is nil.
func assertNotNil(t *testing.T, got any) {
	t.Helper()
	if got == nil {
		t.Fatal("expected non-nil value, got nil")
	}
	// Handle interface containing nil pointer
	v := reflect.ValueOf(got)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		t.Fatal("expected non-nil value, got nil pointer")
	}
}

// assertTrue fails if got is false.
func assertTrue(t *testing.T, got bool) {
	t.Helper()
	if !got {
		t.Fatal("expected true, got false")
	}
}

// assertFalse fails if got is true.
func assertFalse(t *testing.T, got bool) {
	t.Helper()
	if got {
		t.Fatal("expected false, got true")
	}
}

// assertLen fails if the slice/map doesn't have the expected length.
func assertLen[T any](t *testing.T, slice []T, want int) {
	t.Helper()
	if len(slice) != want {
		t.Fatalf("expected length %d, got %d", want, len(slice))
	}
}

// assertRequestMethod verifies the mock received a request for the given method.
func assertRequestMethod(t *testing.T, mock *MockTrueNASServer, method string) {
	t.Helper()
	requests := mock.GetRequestsByMethod(method)
	if len(requests) == 0 {
		t.Fatalf("expected request for method %q, but none found", method)
	}
}

// assertRequestCount verifies the mock received exactly n requests for the given method.
func assertRequestCount(t *testing.T, mock *MockTrueNASServer, method string, count int) {
	t.Helper()
	requests := mock.GetRequestsByMethod(method)
	if len(requests) != count {
		t.Fatalf("expected %d request(s) for method %q, got %d", count, method, len(requests))
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Table-driven test helpers

// TestCase represents a generic test case for table-driven tests.
type TestCase[I, O any] struct {
	Name     string
	Input    I
	Expected O
	WantErr  bool
	ErrMsg   string
}

// BoolTestCase is a test case for boolean results.
type BoolTestCase struct {
	Name     string
	Input    error
	Expected bool
}

// runBoolTableTests runs a slice of boolean test cases.
func runBoolTableTests(t *testing.T, tests []BoolTestCase, fn func(error) bool) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			got := fn(tc.Input)
			assertEqual(t, got, tc.Expected)
		})
	}
}
