# Akash BME Monitor — Documentation

| Doc | Covers |
|---|---|
| [monitors.md](monitors.md) | Every monitor: what it checks, poll interval, alert severities, repeat intervals |
| [configuration.md](configuration.md) | `config.yaml` structure, environment variables, adding networks and relayers |
| [deployment.md](deployment.md) | Building images to GHCR, Kubernetes deployment for mainnet / sandbox / testnet |
| [operations.md](operations.md) | Day-to-day runbook: config changes, rollouts, rollback, logs, known quirks |
| [router-set-rotation.md](router-set-rotation.md) | Responding to a Pyth router set rotation / out-of-sync alert |

## Environments

| Environment | Network name | Manifests | Slack channel |
|---|---|---|---|
| Mainnet | `mainnet` | `deploy/` | `#akash-bme-monitor` |
| Sandbox | `sandbox-2` | `deploy-sandbox/` | `#akash-bme-monitor-sandbox` |
| Testnet | `testnet-reclamation` | `deploy-testnet/` | `#akash-bme-monitor-testnet` |

All three run as separate Deployments in the `akash-services` namespace on the `provider-monitoring` cluster.
