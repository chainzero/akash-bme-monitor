package oracle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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

func (m *mockAlerter) Send(alert types.Alert)              { m.sends = append(m.sends, alert) }
func (m *mockAlerter) Resolve(key, title, body string)     { m.resolves = append(m.resolves, resolveRecord{key, title, body}) }
func (m *mockAlerter) Post(title, body string)             {}

// --- formatAge ---

func TestFormatAge_SecondsOnly(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{1 * time.Second, "1s"},
		{59 * time.Second, "59s"},
	}
	for _, tc := range cases {
		got := formatAge(tc.d)
		if got != tc.want {
			t.Errorf("formatAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFormatAge_WithMinutes(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{60 * time.Second, "1m 0s"},
		{61 * time.Second, "1m 1s"},
		{5*time.Minute + 30*time.Second, "5m 30s"},
		{time.Hour, "60m 0s"},
	}
	for _, tc := range cases {
		got := formatAge(tc.d)
		if got != tc.want {
			t.Errorf("formatAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// --- helpers ---

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testMonitor(t *testing.T, srvURL string, alerter *mockAlerter) *PriceMonitor {
	t.Helper()
	network := config.NetworkConfig{
		Name:          "testnet",
		AkashAPINodes: []string{srvURL},
	}
	cfg := config.OraclePriceConfig{
		PollInterval: config.Duration{Duration: 60 * time.Second},
		Thresholds: config.PriceThresholds{
			WarningAge:   config.Duration{Duration: 5 * time.Minute},
			CriticalAge:  config.Duration{Duration: 10 * time.Minute},
			EmergencyAge: config.Duration{Duration: 15 * time.Minute},
		},
	}
	return NewPriceMonitor(network, cfg, alerter, testLogger())
}

func priceJSON(ageAgo time.Duration) string {
	ts := time.Now().UTC().Add(-ageAgo).Format(time.RFC3339)
	return fmt.Sprintf(`{"prices":[{"id":{"denom":"uakt","base_denom":"usd","source":0,"height":"1000"},"state":{"price":"0.5432","timestamp":"%s"}}]}`, ts)
}

// --- checkPrice ---

func TestCheckPrice_Healthy_SendsResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, priceJSON(1*time.Minute))
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkPrice(context.Background())

	if len(a.sends) != 0 {
		t.Errorf("expected no Send calls for healthy price, got %d", len(a.sends))
	}
	if len(a.resolves) != 1 {
		t.Errorf("expected 1 Resolve call, got %d", len(a.resolves))
	}
}

func TestCheckPrice_Warning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, priceJSON(6*time.Minute))
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkPrice(context.Background())

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send call, got %d", len(a.sends))
	}
	if a.sends[0].Severity != types.SeverityWarning {
		t.Errorf("severity = %v, want Warning", a.sends[0].Severity)
	}
}

func TestCheckPrice_Critical(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, priceJSON(11*time.Minute))
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkPrice(context.Background())

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send call, got %d", len(a.sends))
	}
	if a.sends[0].Severity != types.SeverityCritical {
		t.Errorf("severity = %v, want Critical", a.sends[0].Severity)
	}
}

func TestCheckPrice_Emergency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, priceJSON(16*time.Minute))
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkPrice(context.Background())

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send call, got %d", len(a.sends))
	}
	if a.sends[0].Severity != types.SeverityEmergency {
		t.Errorf("severity = %v, want Emergency", a.sends[0].Severity)
	}
}

func TestCheckPrice_AlertKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, priceJSON(6*time.Minute))
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkPrice(context.Background())

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send, got %d", len(a.sends))
	}
	want := "oracle_price_stale_testnet"
	if a.sends[0].Key != want {
		t.Errorf("key = %q, want %q", a.sends[0].Key, want)
	}
}

func TestCheckPrice_FetchError_NoAlert(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	a := &mockAlerter{}
	m := testMonitor(t, deadURL, a)
	m.checkPrice(context.Background())

	if len(a.sends) != 0 || len(a.resolves) != 0 {
		t.Errorf("expected no alerter calls on fetch error, got sends=%d resolves=%d", len(a.sends), len(a.resolves))
	}
}

func TestCheckPrice_InvalidJSON_NoAlert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkPrice(context.Background())

	if len(a.sends) != 0 {
		t.Errorf("expected no alerts on bad JSON, got %d", len(a.sends))
	}
}

func TestCheckPrice_EmptyPrices_NoAlert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"prices":[]}`)
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkPrice(context.Background())

	if len(a.sends) != 0 {
		t.Errorf("expected no alerts for empty prices, got %d", len(a.sends))
	}
}

func TestCheckPrice_InvalidTimestamp_NoAlert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"prices":[{"id":{},"state":{"price":"0.5","timestamp":"not-a-time"}}]}`)
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkPrice(context.Background())

	if len(a.sends) != 0 {
		t.Errorf("expected no alerts for invalid timestamp, got %d", len(a.sends))
	}
}

// --- checkWalletBalance ---

type walletBalanceResponse struct {
	Balances []struct {
		Denom  string `json:"denom"`
		Amount string `json:"amount"`
	} `json:"balances"`
}

func walletServer(t *testing.T, uaktBalance int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := walletBalanceResponse{
			Balances: []struct {
				Denom  string `json:"denom"`
				Amount string `json:"amount"`
			}{
				{Denom: "uakt", Amount: fmt.Sprintf("%d", uaktBalance)},
			},
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
}

func testRelayer(wallet string) config.RelayerConfig {
	return config.RelayerConfig{
		Name:              "relayer1",
		HealthEndpoint:    "http://localhost:8080/health",
		Wallet:            wallet,
		InfoWalletBalance: 1_000_000_000, // 1000 AKT
		WarnWalletBalance: 500_000_000,   // 500 AKT
		MinWalletBalance:  100_000_000,   // 100 AKT
	}
}

func TestCheckWalletBalance_Healthy_Resolves(t *testing.T) {
	srv := walletServer(t, 2_000_000_000) // 2000 AKT — above all thresholds
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkWalletBalance(context.Background(), testRelayer("akash1abc"))

	if len(a.sends) != 0 {
		t.Errorf("expected no Send, got %d", len(a.sends))
	}
	if len(a.resolves) != 1 {
		t.Errorf("expected 1 Resolve, got %d", len(a.resolves))
	}
}

func TestCheckWalletBalance_InfoAlert(t *testing.T) {
	srv := walletServer(t, 750_000_000) // 750 AKT — below info(1000), above warn(500)
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkWalletBalance(context.Background(), testRelayer("akash1abc"))

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send, got %d", len(a.sends))
	}
	if a.sends[0].Severity != types.SeverityInfo {
		t.Errorf("severity = %v, want Info", a.sends[0].Severity)
	}
}

func TestCheckWalletBalance_WarningAlert(t *testing.T) {
	srv := walletServer(t, 200_000_000) // 200 AKT — below warn(500), above min(100)
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkWalletBalance(context.Background(), testRelayer("akash1abc"))

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send, got %d", len(a.sends))
	}
	if a.sends[0].Severity != types.SeverityWarning {
		t.Errorf("severity = %v, want Warning", a.sends[0].Severity)
	}
}

func TestCheckWalletBalance_CriticalAlert(t *testing.T) {
	srv := walletServer(t, 50_000_000) // 50 AKT — below min(100)
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkWalletBalance(context.Background(), testRelayer("akash1abc"))

	if len(a.sends) != 1 {
		t.Fatalf("expected 1 Send, got %d", len(a.sends))
	}
	if a.sends[0].Severity != types.SeverityCritical {
		t.Errorf("severity = %v, want Critical", a.sends[0].Severity)
	}
}

func TestCheckWalletBalance_NoWallet_Skipped(t *testing.T) {
	a := &mockAlerter{}
	m := testMonitor(t, "http://unused", a)
	// Empty wallet address — should be a no-op.
	relayer := testRelayer("")
	m.checkWalletBalance(context.Background(), relayer)

	if len(a.sends) != 0 || len(a.resolves) != 0 {
		t.Error("expected no alerter calls when wallet is empty")
	}
}

func TestCheckWalletBalance_NoMinThreshold_Skipped(t *testing.T) {
	a := &mockAlerter{}
	m := testMonitor(t, "http://unused", a)
	relayer := config.RelayerConfig{
		Name:   "relayer1",
		Wallet: "akash1abc",
		// MinWalletBalance = 0 — skipped
	}
	m.checkWalletBalance(context.Background(), relayer)

	if len(a.sends) != 0 || len(a.resolves) != 0 {
		t.Error("expected no alerter calls when MinWalletBalance is 0")
	}
}

func TestCheckWalletBalance_NoUAKT_Resolves(t *testing.T) {
	// Wallet has only a different denom — balance treated as 0, which is below min.
	// But actually if balance = 0 (no uakt), it would be < minWalletBalance → Critical.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"balances":[{"denom":"ibc/ABC","amount":"1000"}]}`)
	}))
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkWalletBalance(context.Background(), testRelayer("akash1abc"))

	// balance = 0 < min (100 AKT) → Critical alert
	if len(a.sends) != 1 || a.sends[0].Severity != types.SeverityCritical {
		t.Errorf("expected Critical alert for zero uakt balance, got sends=%d", len(a.sends))
	}
}

func TestCheckWalletBalance_AlertKey(t *testing.T) {
	srv := walletServer(t, 50_000_000)
	defer srv.Close()

	a := &mockAlerter{}
	m := testMonitor(t, srv.URL, a)
	m.checkWalletBalance(context.Background(), testRelayer("akash1abc"))

	want := "wallet_balance_low_testnet_relayer1"
	if len(a.sends) < 1 || a.sends[0].Key != want {
		t.Errorf("key = %q, want %q", func() string {
			if len(a.sends) > 0 {
				return a.sends[0].Key
			}
			return "(no alert)"
		}(), want)
	}
}
