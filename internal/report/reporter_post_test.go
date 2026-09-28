package report

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainzero/akash-bme-monitor/internal/config"
	"github.com/chainzero/akash-bme-monitor/internal/types"
)

// recordingAlerter captures Post calls; Send/Resolve are unused by the reporter.
type recordingAlerter struct {
	titles []string
	bodies []string
}

func (a *recordingAlerter) Send(types.Alert)               {}
func (a *recordingAlerter) Resolve(string, string, string) {}
func (a *recordingAlerter) Post(title, body string) {
	a.titles = append(a.titles, title)
	a.bodies = append(a.bodies, body)
}

// fakeChain serves every endpoint the reporter calls. Zero-value fields mean
// "healthy"; set a field to change that endpoint's response.
type fakeChain struct {
	priceAge       time.Duration
	balanceUAKT    int64
	relayerDown    bool
	relayerStopped bool
	bmeBody        string
	hermesIndex    uint32
	hermesDown     bool
	contractIndex  uint32
	oracleDown     bool
	lastOraclePath string
}

func (f *fakeChain) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/akash/oracle/", func(w http.ResponseWriter, r *http.Request) {
		f.lastOraclePath = r.URL.Path
		if f.oracleDown {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ts := time.Now().Add(-f.priceAge).UTC().Format(time.RFC3339)
		fmt.Fprintf(w, `{"prices":[{"id":{"timestamp":%q},"state":{"price":"1.2345"}}]}`, ts)
	})
	mux.HandleFunc("/cosmos/bank/v1beta1/balances/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"balances":[{"denom":"uakt","amount":"%d"}]}`, f.balanceUAKT)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if f.relayerDown {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, `{"isRunning":%t,"address":"akash1relayeraddress0000000000000xyz","priceFeedId":"0x4ea5bb4d2f5900cc2e97"}`, !f.relayerStopped)
	})
	mux.HandleFunc("/akash/bme/v1/status", func(w http.ResponseWriter, r *http.Request) {
		body := f.bmeBody
		if body == "" {
			body = `{"status":"mint_status_healthy","collateral_ratio":"1.50","warn_threshold":"0.95","halt_threshold":"0.90","mints_allowed":true,"refunds_allowed":true}`
		}
		io.WriteString(w, body)
	})
	mux.HandleFunc("/v2/updates/price/latest", func(w http.ResponseWriter, r *http.Request) {
		if f.hermesDown {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		pnau := make([]byte, 20)
		binary.BigEndian.PutUint32(pnau[11:15], f.hermesIndex)
		fmt.Fprintf(w, `{"binary":{"data":[%q]}}`, base64.StdEncoding.EncodeToString(pnau))
	})
	mux.HandleFunc("/cosmwasm/wasm/v1/contract/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":{"router_verifier":{"router_set_index":%d}}}`, f.contractIndex)
	})
	return mux
}

// runReport posts one report against fc using a mainnet-like config
// (guardian monitors disabled, router set + BME + forum enabled).
func runReport(t *testing.T, fc *fakeChain, mutate ...func(*config.Config)) string {
	t.Helper()
	srv := httptest.NewServer(fc.handler())
	defer srv.Close()

	cfg := &config.Config{
		OraclePriceMonitor: config.OraclePriceConfig{OracleAPIVersion: "v2"},
		BMEMonitor:         config.BMEConfig{Enabled: true},
		RouterSetMonitor: config.RouterSetConfig{
			Enabled:      true,
			HermesAPIURL: srv.URL,
			PriceFeedID:  "0xabc",
			HermesAPIKey: "key",
		},
		AnnouncementMonitor: config.AnnouncementConfig{
			Enabled:   true,
			PythForum: config.PythForumConfig{Enabled: true},
		},
		Networks: []config.NetworkConfig{{
			Name:            "mainnet",
			AkashAPINodes:   []string{srv.URL},
			PythVAAContract: "akash1pythvaa",
			HermesRelayers: []config.RelayerConfig{{
				Name:              "hermes-1",
				HealthEndpoint:    srv.URL + "/health",
				Wallet:            "akash1wallet",
				InfoWalletBalance: 1_000_000_000,
				WarnWalletBalance: 500_000_000,
				MinWalletBalance:  100_000_000,
			}},
		}},
	}
	for _, fn := range mutate {
		fn(cfg)
	}

	a := &recordingAlerter{}
	New(cfg, a, slog.New(slog.NewTextHandler(io.Discard, nil))).PostStartup(context.Background())

	if len(a.bodies) != 1 {
		t.Fatalf("expected 1 Post, got %d", len(a.bodies))
	}
	if a.titles[0] != "🔄 BME Price Feed Monitor Restarted" {
		t.Errorf("title = %q", a.titles[0])
	}
	return a.bodies[0]
}

func assertContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("report missing %q\n--- report ---\n%s", w, body)
		}
	}
}

func healthyChain() *fakeChain {
	return &fakeChain{balanceUAKT: 5_000_000_000, hermesIndex: 4, contractIndex: 4}
}

func TestPost_AllHealthy(t *testing.T) {
	fc := healthyChain()
	body := runReport(t, fc)

	assertContains(t, body,
		"━━━ Network: mainnet ━━━",
		"Oracle Price: ✅ $1.2345 AKT/USD",
		"hermes-1: ✅ running  |  wallet: ✅ 5000 AKT",
		"addr: akash1relayeradd...000xyz  |  feed: 0x4ea5bb4d...c2e97",
		"BME: ✅ Healthy  |  collateral: 1.50x  |  mints: ✅  refunds: ✅  (warn: 0.95  halt: 0.90)",
		"Router Set: ✅ index 4  |  pyth-vaa in sync",
		"Pyth Forum: ✅ monitoring active",
		"Pyth Hermes API: ✅ operational",
	)
	if strings.Contains(body, "Guardian Set") || strings.Contains(body, "Etherscan") {
		t.Errorf("guardian/etherscan sections shown while those monitors are disabled:\n%s", body)
	}
	if fc.lastOraclePath != "/akash/oracle/v2/prices" {
		t.Errorf("oracle path = %q, want v2 endpoint", fc.lastOraclePath)
	}
}

func TestPost_OracleV1Path(t *testing.T) {
	fc := healthyChain()
	runReport(t, fc, func(c *config.Config) { c.OraclePriceMonitor.OracleAPIVersion = "" })
	if fc.lastOraclePath != "/akash/oracle/v1/prices" {
		t.Errorf("oracle path = %q, want v1 endpoint", fc.lastOraclePath)
	}
}

func TestPost_OraclePriceAgeIcons(t *testing.T) {
	cases := []struct {
		age  time.Duration
		want string
	}{
		{time.Minute, "Oracle Price: ✅"},
		{6 * time.Minute, "Oracle Price: ⚠️"},
		{20 * time.Minute, "Oracle Price: 🚨"},
	}
	for _, tc := range cases {
		fc := healthyChain()
		fc.priceAge = tc.age
		assertContains(t, runReport(t, fc), tc.want)
	}
}

func TestPost_OracleUnreachable(t *testing.T) {
	fc := healthyChain()
	fc.oracleDown = true
	assertContains(t, runReport(t, fc), "Oracle Price: ❌ unreachable")
}

func TestPost_WalletBalanceIcons(t *testing.T) {
	cases := []struct {
		uakt int64
		want string
	}{
		{2_000_000_000, "wallet: ✅ 2000 AKT"},
		{800_000_000, "wallet: ℹ️ 800 AKT"},
		{300_000_000, "wallet: ⚠️ 300 AKT"},
		{50_000_000, "wallet: 🔴 50 AKT"},
	}
	for _, tc := range cases {
		fc := healthyChain()
		fc.balanceUAKT = tc.uakt
		assertContains(t, runReport(t, fc), tc.want)
	}
}

func TestPost_RelayerStates(t *testing.T) {
	fc := healthyChain()
	fc.relayerStopped = true
	assertContains(t, runReport(t, fc), "hermes-1: 🔴 stopped")

	fc = healthyChain()
	fc.relayerDown = true
	assertContains(t, runReport(t, fc), "hermes-1: ❌ unreachable")
}

func TestPost_BMEStates(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"warn", `{"status":"mint_status_warn","collateral_ratio":"0.93","warn_threshold":"0.95","halt_threshold":"0.90","mints_allowed":true,"refunds_allowed":true}`, "BME: ⚠️ Warn"},
		{"halt", `{"status":"mint_status_halt_cr","collateral_ratio":"0.85","warn_threshold":"0.95","halt_threshold":"0.90","mints_allowed":false,"refunds_allowed":true}`, "BME: 🔴 Halt_cr  |  collateral: 0.85x  |  mints: 🔴"},
		{"bad ratio", `{"collateral_ratio":"abc"}`, "BME Status: ❌ parse error"},
		{"bad json", `not json`, "BME Status: ❌ decode error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := healthyChain()
			fc.bmeBody = tc.body
			assertContains(t, runReport(t, fc), tc.want)
		})
	}
}

func TestPost_RouterSetStates(t *testing.T) {
	fc := healthyChain()
	fc.contractIndex = 3
	assertContains(t, runReport(t, fc), "Router Set: 🔴 index mismatch  |  Hermes: 4  pyth-vaa: 3  |  OUT OF SYNC")

	fc = healthyChain()
	fc.hermesDown = true
	assertContains(t, runReport(t, fc),
		"Router Set: ❌ Hermes unreachable",
		"Pyth Hermes API: ❌ unreachable\n",
	)

	fc = healthyChain()
	fc.hermesDown = true
	assertContains(t, runReport(t, fc, func(c *config.Config) { c.RouterSetMonitor.HermesAPIKey = "" }),
		"Pyth Hermes API: ❌ unreachable (no API key set)",
	)

	fc = healthyChain()
	assertContains(t, runReport(t, fc, func(c *config.Config) { c.Networks[0].PythVAAContract = "" }),
		"Router Set: live index 4  |  pyth-vaa contract not configured",
	)
}

func TestPost_DisabledSectionsOmitted(t *testing.T) {
	body := runReport(t, healthyChain(), func(c *config.Config) {
		c.BMEMonitor.Enabled = false
		c.RouterSetMonitor.Enabled = false
		c.AnnouncementMonitor.Enabled = false
	})
	for _, s := range []string{"BME", "Router Set", "Pyth Forum", "Required API Health Checks"} {
		if strings.Contains(body, s) {
			t.Errorf("report contains %q although that monitor is disabled", s)
		}
	}
}

func TestRunDailySchedule_InvalidTimezoneReturns(t *testing.T) {
	cfg := &config.Config{Report: config.ReportConfig{Timezone: "Not/AZone"}}
	r := New(cfg, &recordingAlerter{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	done := make(chan struct{})
	go func() { r.RunDailySchedule(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunDailySchedule did not return for invalid timezone")
	}
}

func TestRunDailySchedule_StopsOnCancel(t *testing.T) {
	cfg := &config.Config{Report: config.ReportConfig{ScheduleTimes: []string{"08:00", "bad", "20:00"}}}
	r := New(cfg, &recordingAlerter{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.RunDailySchedule(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunDailySchedule did not return after cancel")
	}
}
