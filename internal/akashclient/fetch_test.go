package akashclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetch_NoNodes(t *testing.T) {
	_, err := Fetch(context.Background(), http.DefaultClient, nil, "/path")
	if err == nil {
		t.Fatal("expected error for no nodes")
	}
	if !strings.Contains(err.Error(), "no akash API nodes configured") {
		t.Errorf("error = %q, want 'no akash API nodes configured'", err)
	}
}

func TestFetch_EmptyNodes(t *testing.T) {
	_, err := Fetch(context.Background(), http.DefaultClient, []string{}, "/path")
	if err == nil {
		t.Fatal("expected error for empty node list")
	}
}

func TestFetch_FirstNodeSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := Fetch(context.Background(), http.DefaultClient, []string{srv.URL}, "/test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestFetch_PathForwarded(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.RequestURI
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	Fetch(context.Background(), http.DefaultClient, []string{srv.URL}, "/akash/oracle/v1/prices?limit=1") //nolint:errcheck
	if gotPath != "/akash/oracle/v1/prices?limit=1" {
		t.Errorf("path = %q, want /akash/oracle/v1/prices?limit=1", gotPath)
	}
}

func TestFetch_FirstNode5xx_FallsBackToSecond(t *testing.T) {
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()

	resp, err := Fetch(context.Background(), http.DefaultClient, []string{srv1.URL, srv2.URL}, "/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestFetch_503_FallsBackToNext(t *testing.T) {
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()

	resp, err := Fetch(context.Background(), http.DefaultClient, []string{srv1.URL, srv2.URL}, "/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestFetch_Non5xxNotRetried(t *testing.T) {
	// A 404 is a client error — should be returned immediately, not retried.
	calls := 0
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()

	resp, err := Fetch(context.Background(), http.DefaultClient, []string{srv1.URL, srv2.URL}, "/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (non-5xx must not fall through to next node)", calls)
	}
}

func TestFetch_200NotRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := Fetch(context.Background(), http.DefaultClient, []string{srv.URL, srv.URL}, "/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestFetch_AllNodes5xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), http.DefaultClient, []string{srv.URL, srv.URL}, "/path")
	if err == nil {
		t.Fatal("expected error when all nodes return 5xx")
	}
	if !strings.Contains(err.Error(), "all akash API nodes failed") {
		t.Errorf("error = %q, want 'all akash API nodes failed'", err)
	}
}

func TestFetch_NetworkError_FallsBackToNext(t *testing.T) {
	// Create and immediately close a server to get a "connection refused" URL.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer live.Close()

	resp, err := Fetch(context.Background(), http.DefaultClient, []string{deadURL, live.URL}, "/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestFetch_AllNodesUnreachable(t *testing.T) {
	dead1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url1 := dead1.URL
	dead1.Close()

	dead2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url2 := dead2.URL
	dead2.Close()

	_, err := Fetch(context.Background(), http.DefaultClient, []string{url1, url2}, "/path")
	if err == nil {
		t.Fatal("expected error when all nodes unreachable")
	}
	if !strings.Contains(err.Error(), "all akash API nodes failed") {
		t.Errorf("error = %q, want 'all akash API nodes failed'", err)
	}
}

func TestFetch_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Fetch(ctx, http.DefaultClient, []string{srv.URL}, "/path")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestFetch_MalformedPath_NoRetry(t *testing.T) {
	// A null byte in the path causes NewRequestWithContext to fail immediately.
	// The function must return that error without attempting other nodes.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), http.DefaultClient, []string{srv.URL, srv.URL}, "/path\x00bad")
	if err == nil {
		t.Fatal("expected error for malformed path")
	}
	if calls != 0 {
		t.Errorf("calls = %d; malformed URL should error before any request is sent", calls)
	}
}
