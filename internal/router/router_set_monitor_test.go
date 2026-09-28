package router

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainzero/akash-bme-monitor/internal/config"
	"github.com/chainzero/akash-bme-monitor/internal/types"
)

// mockAlerter records every alerting call for assertion.
type mockAlerter struct {
	sends    []types.Alert
	resolves []resolveRecord
}

type resolveRecord struct{ key, title, body string }

func (m *mockAlerter) Send(alert types.Alert) { m.sends = append(m.sends, alert) }
func (m *mockAlerter) Resolve(key, title, body string) {
	m.resolves = append(m.resolves, resolveRecord{key, title, body})
}
func (m *mockAlerter) Post(title, body string) {}

func (m *mockAlerter) sendKeys() []string {
	keys := make([]string, len(m.sends))
	for i, a := range m.sends {
		keys[i] = a.Key
	}
	return keys
}

func (m *mockAlerter) resolveKeys() []string {
	keys := make([]string, len(m.resolves))
	for i, r := range m.resolves {
		keys[i] = r.key
	}
	return keys
}

// makePNAU builds a minimal PNAU binary carrying the given router set index
// at bytes 11-14, matching the layout decodeRouterSetIndex expects.
func makePNAU(index uint32) []byte {
	b := make([]byte, 20)
	copy(b, "PNAU")
	b[10] = 1 // embedded VAA version
	binary.BigEndian.PutUint32(b[11:15], index)
	return b
}

// fakeBackend serves both the Hermes updates endpoint and the pyth-vaa
// contract get_config query, with mutable state for multi-poll scenarios.
type fakeBackend struct {
	mu             sync.Mutex
	hermesIndex    uint32
	hermesStatus   int
	contractIndex  uint32
	contractStatus int
	gotAuth        string
	gotHermesPath  string
}

func newFakeBackend(hermesIndex, contractIndex uint32) *fakeBackend {
	return &fakeBackend{
		hermesIndex:    hermesIndex,
		hermesStatus:   http.StatusOK,
		contractIndex:  contractIndex,
		contractStatus: http.StatusOK,
	}
}

func (f *fakeBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case strings.HasPrefix(r.URL.Path, "/v2/updates/price/latest"):
		f.gotAuth = r.Header.Get("Authorization")
		f.gotHermesPath = r.URL.RequestURI()
		if f.hermesStatus != http.StatusOK {
			w.WriteHeader(f.hermesStatus)
			return
		}
		fmt.Fprintf(w, `{"binary":{"encoding":"base64","data":[%q]}}`,
			base64.StdEncoding.EncodeToString(makePNAU(f.hermesIndex)))

	case strings.HasPrefix(r.URL.Path, "/cosmwasm/wasm/v1/contract/"):
		if f.contractStatus != http.StatusOK {
			w.WriteHeader(f.contractStatus)
			return
		}
		fmt.Fprintf(w, `{"data":{"router_verifier":{"router_set_index":%d}}}`, f.contractIndex)

	default:
		http.NotFound(w, r)
	}
}

func (f *fakeBackend) set(fn func(*fakeBackend)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func testRouterMonitor(t *testing.T, srvURL string, a *mockAlerter, networks ...config.NetworkConfig) *RouterSetMonitor {
	t.Helper()
	if networks == nil {
		networks = []config.NetworkConfig{{
			Name:            "mainnet",
			AkashAPINodes:   []string{srvURL},
			PythVAAContract: "akash1pythvaa",
		}}
	}
	cfg := config.RouterSetConfig{
		Enabled:      true,
		PollInterval: config.Duration{Duration: time.Minute},
		HermesAPIURL: srvURL,
		PriceFeedID:  "0xabc123",
		HermesAPIKey: "test-key",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRouterSetMonitor(cfg, networks, a, logger)
}

// --- decodeRouterSetIndex ---

func TestDecodeRouterSetIndex(t *testing.T) {
	for _, idx := range []uint32{0, 1, 4, 0xFFFFFFFF} {
		got, err := decodeRouterSetIndex(makePNAU(idx))
		if err != nil {
			t.Fatalf("index %d: unexpected error: %v", idx, err)
		}
		if got != idx {
			t.Errorf("decodeRouterSetIndex() = %d, want %d", got, idx)
		}
	}
}

func TestDecodeRouterSetIndex_TooShort(t *testing.T) {
	if _, err := decodeRouterSetIndex(make([]byte, 14)); err == nil {
		t.Error("expected error for 14-byte PNAU, got nil")
	}
	if _, err := decodeRouterSetIndex(make([]byte, 15)); err != nil {
		t.Errorf("unexpected error for 15-byte PNAU: %v", err)
	}
}

// --- HermesClient ---

func TestHermesClient_SendsAuthAndStripsFeedPrefix(t *testing.T) {
	fb := newFakeBackend(7, 7)
	srv := httptest.NewServer(fb)
	defer srv.Close()

	c := NewHermesClient(srv.URL, "secret-key", "0xabc123")
	got, err := c.GetCurrentRouterSetIndex(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 7 {
		t.Errorf("index = %d, want 7", got)
	}
	if fb.gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want %q", fb.gotAuth, "Bearer secret-key")
	}
	if !strings.Contains(fb.gotHermesPath, "ids[]=abc123") || strings.Contains(fb.gotHermesPath, "0xabc123") {
		t.Errorf("request URI %q should contain feed ID without 0x prefix", fb.gotHermesPath)
	}
}

func TestHermesClient_NoAPIKeyOmitsAuthHeader(t *testing.T) {
	fb := newFakeBackend(1, 1)
	srv := httptest.NewServer(fb)
	defer srv.Close()

	if _, err := NewHermesClient(srv.URL, "", "abc").GetCurrentRouterSetIndex(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fb.gotAuth != "" {
		t.Errorf("Authorization = %q, want empty", fb.gotAuth)
	}
}

func TestHermesClient_Errors(t *testing.T) {
	cases := []struct {
		name string
		body string
		code int
	}{
		{"non-200 status", "", http.StatusUnauthorized},
		{"invalid JSON", "not json", http.StatusOK},
		{"empty data", `{"binary":{"data":[]}}`, http.StatusOK},
		{"bad base64", `{"binary":{"data":["!!!"]}}`, http.StatusOK},
		{"short PNAU", fmt.Sprintf(`{"binary":{"data":[%q]}}`, base64.StdEncoding.EncodeToString([]byte("short"))), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			if _, err := NewHermesClient(srv.URL, "", "abc").GetCurrentRouterSetIndex(context.Background()); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// --- PythVAAClient ---

func TestPythVAAClient_GetRouterSetIndex(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"router_verifier": map[string]any{"router_set_index": 5}},
		})
	}))
	defer srv.Close()

	got, err := NewPythVAAClient([]string{srv.URL}, "mainnet", "akash1pythvaa").GetRouterSetIndex(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 5 {
		t.Errorf("index = %d, want 5", got)
	}
	want := "/cosmwasm/wasm/v1/contract/akash1pythvaa/smart/" + getConfigQuery
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestGetConfigQueryEncoding(t *testing.T) {
	decoded, err := base64.StdEncoding.DecodeString(getConfigQuery)
	if err != nil {
		t.Fatalf("getConfigQuery is not valid base64: %v", err)
	}
	if string(decoded) != `{"get_config":{}}` {
		t.Errorf("getConfigQuery decodes to %q, want %q", decoded, `{"get_config":{}}`)
	}
}

func TestPythVAAClient_Errors(t *testing.T) {
	t.Run("no contract configured", func(t *testing.T) {
		if _, err := NewPythVAAClient([]string{"http://unused"}, "mainnet", "").GetRouterSetIndex(context.Background()); err == nil {
			t.Error("expected error, got nil")
		}
	})
	t.Run("non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		if _, err := NewPythVAAClient([]string{srv.URL}, "mainnet", "c").GetRouterSetIndex(context.Background()); err == nil {
			t.Error("expected error, got nil")
		}
	})
	t.Run("invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "not json")
		}))
		defer srv.Close()
		if _, err := NewPythVAAClient([]string{srv.URL}, "mainnet", "c").GetRouterSetIndex(context.Background()); err == nil {
			t.Error("expected error, got nil")
		}
	})
}

// --- RouterSetMonitor.check ---

func TestCheck_InSync_ResolvesOnly(t *testing.T) {
	srv := httptest.NewServer(newFakeBackend(4, 4))
	defer srv.Close()

	a := &mockAlerter{}
	m := testRouterMonitor(t, srv.URL, a)
	m.check(context.Background())

	if len(a.sends) != 0 {
		t.Errorf("expected no sends, got %v", a.sendKeys())
	}
	if got := a.resolveKeys(); len(got) != 1 || got[0] != "router_set_sync_mainnet" {
		t.Errorf("resolves = %v, want [router_set_sync_mainnet]", got)
	}
	if !m.initialized || m.lastKnownIndex != 4 {
		t.Errorf("baseline = (%v, %d), want (true, 4)", m.initialized, m.lastKnownIndex)
	}
}

func TestCheck_OutOfSync_SendsCritical(t *testing.T) {
	srv := httptest.NewServer(newFakeBackend(5, 4))
	defer srv.Close()

	a := &mockAlerter{}
	m := testRouterMonitor(t, srv.URL, a)
	m.check(context.Background())

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 send, got %v", a.sendKeys())
	}
	got := a.sends[0]
	if got.Key != "router_set_sync_mainnet" || got.Severity != types.SeverityCritical {
		t.Errorf("alert = (%s, %s), want (router_set_sync_mainnet, Critical)", got.Key, got.Severity)
	}
	if !strings.Contains(got.Body, "Active Hermes Index: 5") || !strings.Contains(got.Body, "Contract Index:      4") {
		t.Errorf("alert body missing index details:\n%s", got.Body)
	}
}

func TestCheck_FirstPollDoesNotAlertRotation(t *testing.T) {
	// A non-zero index on the first poll is the baseline, not a rotation.
	srv := httptest.NewServer(newFakeBackend(9, 9))
	defer srv.Close()

	a := &mockAlerter{}
	testRouterMonitor(t, srv.URL, a).check(context.Background())

	for _, s := range a.sends {
		if s.Key == "router_set_rotation" {
			t.Error("rotation alert sent on first poll")
		}
	}
}

func TestCheck_RotationDetected(t *testing.T) {
	fb := newFakeBackend(4, 4)
	srv := httptest.NewServer(fb)
	defer srv.Close()

	a := &mockAlerter{}
	m := testRouterMonitor(t, srv.URL, a)
	m.check(context.Background())

	fb.set(func(f *fakeBackend) { f.hermesIndex = 5 })
	m.check(context.Background())

	keys := a.sendKeys()
	if len(keys) != 2 || keys[0] != "router_set_rotation" || keys[1] != "router_set_sync_mainnet" {
		t.Fatalf("sends = %v, want [router_set_rotation router_set_sync_mainnet]", keys)
	}
	if a.sends[0].Title != "ROUTER SET ROTATION: INDEX 4 → 5" {
		t.Errorf("rotation title = %q", a.sends[0].Title)
	}
	if m.lastKnownIndex != 5 {
		t.Errorf("lastKnownIndex = %d, want 5", m.lastKnownIndex)
	}
}

func TestCheck_IndexDecreaseIsNotRotation(t *testing.T) {
	fb := newFakeBackend(5, 5)
	srv := httptest.NewServer(fb)
	defer srv.Close()

	a := &mockAlerter{}
	m := testRouterMonitor(t, srv.URL, a)
	m.check(context.Background())

	fb.set(func(f *fakeBackend) { f.hermesIndex = 4; f.contractIndex = 4 })
	m.check(context.Background())

	if len(a.sends) != 0 {
		t.Errorf("expected no sends on index decrease, got %v", a.sendKeys())
	}
}

func TestCheck_HermesFailureEscalation(t *testing.T) {
	fb := newFakeBackend(4, 4)
	fb.hermesStatus = http.StatusServiceUnavailable
	srv := httptest.NewServer(fb)
	defer srv.Close()

	a := &mockAlerter{}
	m := testRouterMonitor(t, srv.URL, a)
	for i := 0; i < 5; i++ {
		m.check(context.Background())
	}

	want := []types.Severity{types.SeverityWarning, types.SeverityCritical, types.SeverityEmergency}
	if len(a.sends) != len(want) {
		t.Fatalf("expected %d sends (alerts stop after the 3rd failure), got %d", len(want), len(a.sends))
	}
	for i, sev := range want {
		if a.sends[i].Key != "router_hermes_unreachable" || a.sends[i].Severity != sev {
			t.Errorf("send %d = (%s, %s), want (router_hermes_unreachable, %s)",
				i, a.sends[i].Key, a.sends[i].Severity, sev)
		}
	}
	if len(a.resolves) != 0 {
		t.Errorf("expected no resolves while Hermes is down, got %v", a.resolveKeys())
	}
	if m.consecutiveHermesFailures != 5 {
		t.Errorf("consecutiveHermesFailures = %d, want 5", m.consecutiveHermesFailures)
	}
}

func TestCheck_HermesRecoveryResolves(t *testing.T) {
	fb := newFakeBackend(4, 4)
	fb.hermesStatus = http.StatusServiceUnavailable
	srv := httptest.NewServer(fb)
	defer srv.Close()

	a := &mockAlerter{}
	m := testRouterMonitor(t, srv.URL, a)
	m.check(context.Background())

	fb.set(func(f *fakeBackend) { f.hermesStatus = http.StatusOK })
	m.check(context.Background())

	got := a.resolveKeys()
	if len(got) != 2 || got[0] != "router_hermes_unreachable" || got[1] != "router_set_sync_mainnet" {
		t.Errorf("resolves = %v, want [router_hermes_unreachable router_set_sync_mainnet]", got)
	}
	if m.consecutiveHermesFailures != 0 {
		t.Errorf("consecutiveHermesFailures = %d, want 0", m.consecutiveHermesFailures)
	}
}

func TestCheck_HermesFailureSkipsNetworkComparison(t *testing.T) {
	fb := newFakeBackend(5, 4) // would be out of sync if compared
	fb.hermesStatus = http.StatusServiceUnavailable
	srv := httptest.NewServer(fb)
	defer srv.Close()

	a := &mockAlerter{}
	testRouterMonitor(t, srv.URL, a).check(context.Background())

	for _, s := range a.sends {
		if s.Key == "router_set_sync_mainnet" {
			t.Error("sync alert sent even though Hermes was unreachable")
		}
	}
}

func TestCheck_ContractUnreachable_LogsOnly(t *testing.T) {
	fb := newFakeBackend(4, 4)
	fb.contractStatus = http.StatusNotFound
	srv := httptest.NewServer(fb)
	defer srv.Close()

	a := &mockAlerter{}
	testRouterMonitor(t, srv.URL, a).check(context.Background())

	if len(a.sends) != 0 || len(a.resolves) != 0 {
		t.Errorf("expected no alerts when contract query fails, got sends=%v resolves=%v",
			a.sendKeys(), a.resolveKeys())
	}
}

func TestCheck_SkipsNetworkWithoutContract(t *testing.T) {
	srv := httptest.NewServer(newFakeBackend(5, 4))
	defer srv.Close()

	a := &mockAlerter{}
	networks := []config.NetworkConfig{
		{Name: "mainnet", AkashAPINodes: []string{srv.URL}, PythVAAContract: "akash1pythvaa"},
		{Name: "testnet", AkashAPINodes: []string{srv.URL}}, // no contract
	}
	testRouterMonitor(t, srv.URL, a, networks...).check(context.Background())

	if got := a.sendKeys(); len(got) != 1 || got[0] != "router_set_sync_mainnet" {
		t.Errorf("sends = %v, want only [router_set_sync_mainnet]", got)
	}
}

func TestRun_StopsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(newFakeBackend(1, 1))
	defer srv.Close()

	m := testRouterMonitor(t, srv.URL, &mockAlerter{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}
