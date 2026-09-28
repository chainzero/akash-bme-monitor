# Akash BME Monitor

Akash BME Monitor watches the Akash Network BME (Burn Mint Equilibrium) price feed pipeline end to end: the on-chain AKT/USD oracle price, the Hermes relayers that submit it, their wallet balances, BME collateral health, and the Pyth router set the `pyth-vaa` contract verifies against. Problems are posted to Slack with escalating severity, and a full health report is posted twice daily.

It is a single Go binary with no state, running as a one-replica Kubernetes Deployment.

**Slack**: `#akash-bme-monitor` (mainnet), `#akash-bme-monitor-sandbox`, `#akash-bme-monitor-testnet`
**Image**: `ghcr.io/ovrclk/akash-bme-monitor`
**Cluster**: `provider-monitoring`, namespace `akash-services`

---

## Repository layout

```
main.go                 # Wires and starts all monitors
config.yaml             # Local development config
Dockerfile              # Static build → distroless nonroot image

deploy/                 # Mainnet manifests (namespace, configmap, secret template, deployment)
deploy-sandbox/         # Sandbox manifests
deploy-testnet/         # Testnet manifests

internal/
  oracle/               # Oracle price freshness + relayer wallet balances
  hermes/               # Hermes relayer /health checks
  router/               # Pyth router set index: Hermes vs pyth-vaa contract
  bme/                  # BME collateral ratio and mint/refund status
  announcements/        # Pyth forum RSS + Wormhole GitHub early warnings
  guardian/             # Legacy Wormhole guardian set monitors (disabled on mainnet)
  report/               # Startup and scheduled health reports
  alerting/             # Slack webhook client with per-key cooldowns
  akashclient/          # Akash REST fetch with multi-node fallback
  config/  types/       # Config loading/validation, shared types
```

---

## Engineering docs

Full documentation lives in [`documentation/`](documentation/README.md): what each monitor alerts on, configuration, build and deployment, day-to-day operations, and the router set rotation response.
