package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testClient returns a SlackClient pointed at a server that replies with the
// given responses in order (repeating the last one if calls continue), plus a
// counter of requests received.
func testClient(t *testing.T, responses []func(w http.ResponseWriter)) (*SlackClient, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := calls
		if i >= len(responses) {
			i = len(responses) - 1
		}
		calls++
		responses[i](w)
	}))
	t.Cleanup(server.Close)

	client := NewSlackClient("xoxp-test")
	client.baseURL = server.URL + "/"
	client.retryBackoff = time.Millisecond
	return client, &calls
}

func ok(w http.ResponseWriter) {
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func status(code int) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`error`))
	}
}

func TestRetryThrottledThenSuccess(t *testing.T) {
	client, calls := testClient(t, []func(w http.ResponseWriter){status(429), ok})
	if err := client.JSONRequest(context.Background(), "test", struct{}{}, nil); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if *calls != 2 {
		t.Fatalf("expected 2 calls, got %d", *calls)
	}
}

func TestRetryRatelimitedEnvelope(t *testing.T) {
	ratelimited := func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"ratelimited"}`))
	}
	client, calls := testClient(t, []func(w http.ResponseWriter){ratelimited, ok})
	if err := client.JSONRequest(context.Background(), "test", struct{}{}, nil); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if *calls != 2 {
		t.Fatalf("expected 2 calls, got %d", *calls)
	}
}

func TestRetryServerErrorsThenSuccess(t *testing.T) {
	client, calls := testClient(t, []func(w http.ResponseWriter){status(500), status(503), ok})
	if err := client.JSONRequest(context.Background(), "test", struct{}{}, nil); err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if *calls != 3 {
		t.Fatalf("expected 3 calls, got %d", *calls)
	}
}

func TestServerErrorGivesUpAfterMaxAttempts(t *testing.T) {
	client, calls := testClient(t, []func(w http.ResponseWriter){status(500)})
	err := client.JSONRequest(context.Background(), "test", struct{}{}, nil)
	if err == nil || !strings.Contains(err.Error(), "giving up after 5 attempts") {
		t.Fatalf("expected giving-up error, got %v", err)
	}
	if *calls != maxAttempts {
		t.Fatalf("expected %d calls, got %d", maxAttempts, *calls)
	}
}

func TestNotFoundRetriedExactlyOnce(t *testing.T) {
	client, calls := testClient(t, []func(w http.ResponseWriter){status(404), ok})
	if err := client.JSONRequest(context.Background(), "test", struct{}{}, nil); err != nil {
		t.Fatalf("expected success after one retry, got %v", err)
	}
	if *calls != 2 {
		t.Fatalf("expected 2 calls, got %d", *calls)
	}

	client, calls = testClient(t, []func(w http.ResponseWriter){status(404)})
	err := client.JSONRequest(context.Background(), "test", struct{}{}, nil)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
	if *calls != 2 {
		t.Fatalf("expected exactly 2 calls (one retry), got %d", *calls)
	}
}

func TestAPIErrorNotRetried(t *testing.T) {
	notFound := func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"app_not_found"}`))
	}
	client, calls := testClient(t, []func(w http.ResponseWriter){notFound, ok})
	err := client.JSONRequest(context.Background(), "test", struct{}{}, nil)
	if err == nil || err.Error() != "app_not_found" {
		t.Fatalf("expected app_not_found, got %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected 1 call (no retry), got %d", *calls)
	}
}
