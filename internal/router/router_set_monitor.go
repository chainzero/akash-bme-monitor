package router

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/chainzero/akash-bme-monitor/internal/alerting"
	"github.com/chainzero/akash-bme-monitor/internal/config"
	"github.com/chainzero/akash-bme-monitor/internal/types"
)

// RouterSetMonitor implements the Pyth router set currency check.
//
// It replaces Components 3 (Ethereum RPC guardian sync) and 5 (Wormholescan)
// after the Pyth core upgrade. Instead of comparing against the Ethereum
// Wormhole contract, it:
//
//  1. Fetches a live price update VAA from the authenticated Hermes endpoint
//     (pyth.dourolabs.app/hermes) and decodes the active router_set_index
//     from the PNAU binary — this is the authoritative current index.
//
//  2. Queries each Akash network's pyth-vaa contract for its stored
//     router_set_index.
//
//  3. Alerts if they differ — meaning the contract is behind the active router
//     set and price submissions will start failing with InvalidRouterSetIndex.
type RouterSetMonitor struct {
	cfg                       config.RouterSetConfig
	networks                  []config.NetworkConfig
	hermesClient              *HermesClient
	alerter                   alerting.Alerter
	logger                    *slog.Logger
	lastKnownIndex            uint32
	initialized               bool
	consecutiveHermesFailures int
}

func NewRouterSetMonitor(
	cfg config.RouterSetConfig,
	networks []config.NetworkConfig,
	alerter alerting.Alerter,
	logger *slog.Logger,
) *RouterSetMonitor {
	return &RouterSetMonitor{
		cfg:          cfg,
		networks:     networks,
		hermesClient: NewHermesClient(cfg.HermesAPIURL, cfg.HermesAPIKey, cfg.PriceFeedID),
		alerter:      alerter,
		logger:       logger.With("component", "router_set_monitor"),
	}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (m *RouterSetMonitor) Run(ctx context.Context) {
	m.logger.Info("router set monitor started",
		"poll_interval", m.cfg.PollInterval.Duration,
		"hermes_api_url", m.cfg.HermesAPIURL,
		"price_feed_id", m.cfg.PriceFeedID,
	)

	m.check(ctx)

	ticker := time.NewTicker(m.cfg.PollInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.check(ctx)
		}
	}
}

func (m *RouterSetMonitor) check(ctx context.Context) {
	// --- Step 1: Get authoritative router set index from live Hermes VAA ---
	liveIndex, err := m.hermesClient.GetCurrentRouterSetIndex(ctx)
	if err != nil {
		m.consecutiveHermesFailures++
		m.logger.Error("failed to fetch router set index from Hermes",
			"consecutive_failures", m.consecutiveHermesFailures,
			"error", err,
		)

		var sev types.Severity
		var title string
		switch m.consecutiveHermesFailures {
		case 1:
			sev = types.SeverityWarning
			title = "HERMES API UNREACHABLE — WARNING"
		case 2:
			sev = types.SeverityCritical
			title = "HERMES API UNREACHABLE — CRITICAL"
		case 3:
			sev = types.SeverityEmergency
			title = "HERMES API UNREACHABLE — EMERGENCY (final alert)"
		default:
			return
		}

		m.alerter.Send(types.Alert{
			Key:      "router_hermes_unreachable",
			Severity: sev,
			Title:    title,
			Body: fmt.Sprintf(
				"Cannot reach Hermes API to verify router set index.\n"+
					"API: %s\n"+
					"Consecutive failures: %d\n"+
					"Error: %s\n\n"+
					"Risk: A router set rotation may go undetected while this endpoint is down.\n"+
					"No further alerts will be sent until the endpoint recovers.",
				m.cfg.HermesAPIURL, m.consecutiveHermesFailures, err.Error(),
			),
		})
		return
	}

	// Hermes is responding — reset failure counter and clear any outstanding alert.
	if m.consecutiveHermesFailures > 0 {
		m.consecutiveHermesFailures = 0
		m.alerter.Resolve(
			"router_hermes_unreachable",
			"HERMES API REACHABLE",
			fmt.Sprintf("Hermes API is responding again.\nAPI: %s", m.cfg.HermesAPIURL),
		)
	}

	m.logger.Info("live router set index fetched from Hermes", "router_set_index", liveIndex)

	// --- Step 2: Detect router set rotation (index increase) ---
	if m.initialized && liveIndex > m.lastKnownIndex {
		m.logger.Warn("router set rotation detected via Hermes VAA",
			"previous_index", m.lastKnownIndex,
			"new_index", liveIndex,
		)
		m.alerter.Send(types.Alert{
			Key:      "router_set_rotation",
			Severity: types.SeverityCritical,
			Title:    fmt.Sprintf("ROUTER SET ROTATION: INDEX %d → %d", m.lastKnownIndex, liveIndex),
			Body: fmt.Sprintf(
				"The Pyth router set has rotated.\n\n"+
					"Previous Index: %d\n"+
					"New Index:      %d\n\n"+
					"Action required: Submit the router set upgrade VAA to each network's\n"+
					"pyth-vaa contract. Price feed submissions will fail until updated.\n\n"+
					"Contact Pyth/Douro Labs for the router set upgrade VAA and\n"+
					"the submission procedure (tx wasm execute on pyth-vaa contract).",
				m.lastKnownIndex, liveIndex,
			),
		})
	} else if !m.initialized {
		m.logger.Info("router set baseline established", "router_set_index", liveIndex)
	}

	m.lastKnownIndex = liveIndex
	m.initialized = true

	// --- Step 3: Verify each Akash network's pyth-vaa contract is in sync ---
	for _, network := range m.networks {
		m.compareWithNetwork(ctx, network, liveIndex)
	}
}

// compareWithNetwork checks whether the pyth-vaa contract on a given Akash network
// has the same router_set_index as the currently active Hermes router set.
// Runs on every poll cycle to catch drift even after a monitor restart.
func (m *RouterSetMonitor) compareWithNetwork(ctx context.Context, network config.NetworkConfig, liveIndex uint32) {
	alertKey := fmt.Sprintf("router_set_sync_%s", network.Name)

	if network.PythVAAContract == "" {
		m.logger.Warn("pyth_vaa_contract not configured, skipping sync check", "network", network.Name)
		return
	}

	client := NewPythVAAClient(network.AkashAPINodes, network.Name, network.PythVAAContract)
	contractIndex, err := client.GetRouterSetIndex(ctx)
	if err != nil {
		// Log only — node reachability alerts are handled by the BME/oracle monitors.
		m.logger.Warn("could not fetch router set index from pyth-vaa contract",
			"network", network.Name,
			"contract", network.PythVAAContract,
			"error", err,
		)
		return
	}

	m.logger.Info("router set index comparison",
		"network", network.Name,
		"live_index", liveIndex,
		"contract_index", contractIndex,
	)

	if contractIndex == liveIndex {
		m.alerter.Resolve(
			alertKey,
			fmt.Sprintf("ROUTER SET IN SYNC — %s", network.Name),
			fmt.Sprintf(
				"Network: %s\n\n"+
					"pyth-vaa contract is in sync with the active Hermes router set.\n"+
					"Router Set Index: %d",
				network.Name, liveIndex,
			),
		)
		return
	}

	m.alerter.Send(types.Alert{
		Key:      alertKey,
		Severity: types.SeverityCritical,
		Title:    fmt.Sprintf("ROUTER SET OUT OF SYNC — %s", network.Name),
		Body: fmt.Sprintf(
			"Network: %s\n\n"+
				"pyth-vaa contract router set index does not match the active Hermes index.\n\n"+
				"Active Hermes Index: %d\n"+
				"Contract Index:      %d\n\n"+
				"Price feed VAA submissions will fail with InvalidRouterSetIndex.\n\n"+
				"Action required: Submit the router set upgrade VAA to the pyth-vaa contract.\n"+
				"Contact Pyth/Douro Labs for the upgrade VAA and submission procedure.\n\n"+
				"pyth-vaa contract: %s",
			network.Name,
			liveIndex,
			contractIndex,
			network.PythVAAContract,
		),
	})
}
