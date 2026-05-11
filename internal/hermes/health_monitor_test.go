package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/chainzero/akash-bme-monitor/internal/config"
	"github.com/chainzero/akash-bme-monitor/internal/types"
)

type mockAlerter struct {
	sends    []types.Alert
	resolves []resolveRecord
}

type resolveRecord struct{ key, title, body string }

func (m *mockAlerter) Send(alert types.Alert)          { m.sends = append(m.sends, alert) }
func (m *mockAlerter) Resolve(key, title, body string) { m.resolves = append(m.resolves, resolveRecord{key, title, body}) }
func (m *mockAlerter) Post(title, body string)         {}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testHealthMonitor(network config.NetworkConfig, a *mockAlerter) *HealthMonitor {
	cfg := config.HermesHealthConfig{
		PollInterval:                 config.Duration{},
		ConsecutiveFailuresThreshold: 3,
	}
	return NewHealthMonitor(network, cfg, a, testLogger())
}

// --- detectMismatches ---

func TestDetectMismatches_NoExpectedValues(t *testing.T) {
	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	relayer := config.RelayerConfig{Name: "r1"} // no expected values set
	health := &types.HermesHealthResponse{
		IsRunning:       true,
		PriceFeedID:     "0xabc",
		ContractAddress: "akash1contract",
	}
	got := m.detectMismatches(relayer, health)
	if len(got) != 0 {
		t.Errorf("expected no mismatches with no expected values, got %v", got)
	}
}

func TestDetectMismatches_PriceFeedIDMatches(t *testing.T) {
	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	relayer := config.RelayerConfig{Name: "r1", ExpectedPriceFeedID: "0xABC"}
	health := &types.HermesHealthResponse{PriceFeedID: "0xabc"} // matches case-insensitively
	got := m.detectMismatches(relayer, health)
	if len(got) != 0 {
		t.Errorf("expected no mismatch for case-insensitive match, got %v", got)
	}
}

func TestDetectMismatches_PriceFeedIDMismatch(t *testing.T) {
	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	relayer := config.RelayerConfig{Name: "r1", ExpectedPriceFeedID: "0xABC"}
	health := &types.HermesHealthResponse{PriceFeedID: "0xDEF"}
	got := m.detectMismatches(relayer, health)
	if len(got) != 1 {
		t.Errorf("expected 1 mismatch, got %d: %v", len(got), got)
	}
}

func TestDetectMismatches_ContractAddressMatches(t *testing.T) {
	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	relayer := config.RelayerConfig{Name: "r1", ExpectedContractAddress: "AKASH1CONTRACT"}
	health := &types.HermesHealthResponse{ContractAddress: "akash1contract"}
	got := m.detectMismatches(relayer, health)
	if len(got) != 0 {
		t.Errorf("expected no mismatch for case-insensitive contract match, got %v", got)
	}
}

func TestDetectMismatches_ContractAddressMismatch(t *testing.T) {
	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	relayer := config.RelayerConfig{Name: "r1", ExpectedContractAddress: "akash1expected"}
	health := &types.HermesHealthResponse{ContractAddress: "akash1actual"}
	got := m.detectMismatches(relayer, health)
	if len(got) != 1 {
		t.Errorf("expected 1 mismatch, got %d: %v", len(got), got)
	}
}

func TestDetectMismatches_BothMismatch(t *testing.T) {
	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	relayer := config.RelayerConfig{
		Name:                    "r1",
		ExpectedPriceFeedID:     "0xAAA",
		ExpectedContractAddress: "akash1expected",
	}
	health := &types.HermesHealthResponse{
		PriceFeedID:     "0xBBB",
		ContractAddress: "akash1actual",
	}
	got := m.detectMismatches(relayer, health)
	if len(got) != 2 {
		t.Errorf("expected 2 mismatches, got %d: %v", len(got), got)
	}
}

// --- fetchHealth ---

func TestFetchHealth_Success(t *testing.T) {
	want := types.HermesHealthResponse{
		IsRunning:       true,
		Address:         "akash1address",
		PriceFeedID:     "0xfeed",
		ContractAddress: "akash1contract",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(want) //nolint:errcheck
	}))
	defer srv.Close()

	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	got, err := m.fetchHealth(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.IsRunning || got.Address != want.Address || got.PriceFeedID != want.PriceFeedID {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestFetchHealth_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	_, err := m.fetchHealth(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestFetchHealth_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	_, err := m.fetchHealth(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFetchHealth_Unreachable(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close()

	m := testHealthMonitor(config.NetworkConfig{Name: "testnet"}, &mockAlerter{})
	_, err := m.fetchHealth(context.Background(), url)
	if err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
}

// --- checkRelayer ---

func makeNetwork(srvURL string) config.NetworkConfig {
	return config.NetworkConfig{
		Name: "testnet",
		HermesRelayers: []config.RelayerConfig{
			{Name: "r1", HealthEndpoint: srvURL},
		},
	}
}

func TestCheckRelayer_Healthy_Resolves(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.HermesHealthResponse{IsRunning: true, Address: "akash1x"}) //nolint:errcheck
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testHealthMonitor(makeNetwork(srv.URL), a)
	m.checkRelayer(context.Background(), m.network.HermesRelayers[0])

	if len(a.sends) != 0 {
		t.Errorf("expected no Send for healthy relayer, got %d", len(a.sends))
	}
	// Should resolve both the unreachable key and the mismatch key.
	if len(a.resolves) < 1 {
		t.Errorf("expected Resolve calls for healthy relayer, got %d", len(a.resolves))
	}
}

func TestCheckRelayer_Unreachable_SendsAlert(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close()

	a := &mockAlerter{}
	m := testHealthMonitor(makeNetwork(url), a)
	m.checkRelayer(context.Background(), m.network.HermesRelayers[0])

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send for unreachable relayer, got %d", len(a.sends))
	}
	if a.sends[0].Severity != types.SeverityCritical {
		t.Errorf("severity = %v, want Critical", a.sends[0].Severity)
	}
}

func TestCheckRelayer_NotRunning_SendsAlert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.HermesHealthResponse{IsRunning: false}) //nolint:errcheck
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testHealthMonitor(makeNetwork(srv.URL), a)
	m.checkRelayer(context.Background(), m.network.HermesRelayers[0])

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send for stopped relayer, got %d", len(a.sends))
	}
	if a.sends[0].Severity != types.SeverityCritical {
		t.Errorf("severity = %v, want Critical", a.sends[0].Severity)
	}
}

func TestCheckRelayer_ConfigMismatch_SendsWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.HermesHealthResponse{ //nolint:errcheck
			IsRunning:   true,
			PriceFeedID: "0xACTUAL",
		})
	}))
	defer srv.Close()

	network := config.NetworkConfig{
		Name: "testnet",
		HermesRelayers: []config.RelayerConfig{
			{Name: "r1", HealthEndpoint: srv.URL, ExpectedPriceFeedID: "0xEXPECTED"},
		},
	}
	a := &mockAlerter{}
	m := testHealthMonitor(network, a)
	m.checkRelayer(context.Background(), m.network.HermesRelayers[0])

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send for config mismatch, got %d", len(a.sends))
	}
	if a.sends[0].Severity != types.SeverityWarning {
		t.Errorf("severity = %v, want Warning", a.sends[0].Severity)
	}
}

func TestCheckRelayer_Unreachable_IncrementsFailures(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close()

	a := &mockAlerter{}
	m := testHealthMonitor(makeNetwork(url), a)

	m.checkRelayer(context.Background(), m.network.HermesRelayers[0])
	m.checkRelayer(context.Background(), m.network.HermesRelayers[0])

	state := m.states["r1"]
	if state.consecutiveFailures != 2 {
		t.Errorf("consecutiveFailures = %d, want 2", state.consecutiveFailures)
	}
}

func TestCheckRelayer_Recovery_ResetsState(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(types.HermesHealthResponse{IsRunning: true}) //nolint:errcheck
	}))
	defer live.Close()

	a := &mockAlerter{}

	// First poll against dead endpoint.
	net1 := makeNetwork(deadURL)
	m := testHealthMonitor(net1, a)
	m.checkRelayer(context.Background(), m.network.HermesRelayers[0])
	if m.states["r1"].consecutiveFailures != 1 {
		t.Fatalf("expected 1 failure after first bad poll")
	}

	// Simulate recovery by pointing the relayer at the live server.
	relayerLive := config.RelayerConfig{Name: "r1", HealthEndpoint: live.URL}
	m.checkRelayer(context.Background(), relayerLive)

	if m.states["r1"].consecutiveFailures != 0 {
		t.Errorf("consecutiveFailures = %d after recovery, want 0", m.states["r1"].consecutiveFailures)
	}
	if m.states["r1"].lastSeen.IsZero() {
		t.Error("lastSeen not updated on recovery")
	}
}
