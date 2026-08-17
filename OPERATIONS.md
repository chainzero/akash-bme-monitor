# Akash BME Price Feed Monitor — Operations

## Table of Contents
1. [Current Deployment](#current-deployment)
2. [Configuration Updates](#configuration-updates)
3. [What Is Monitored](#what-is-monitored)

---

## Current Deployment

The monitor runs as a single-replica Kubernetes Deployment in the `akash-services` namespace.

### Cluster
The service is hosted on the **provider-monitoring** cluster, reachable via Tailscale at `provider-monitoring`. This is the same cluster that hosts the Akash Provider Monitor — the BME monitor shares the infrastructure but runs as an independent workload.

The pod is pinned to the control-plane node (`provider-monitor-k3cluster`), which has the internet egress required to reach the Akash API, Ethereum RPC, Wormholescan, and Slack.

### Manifests
Kubernetes manifests (`deployment.yaml`, `configmap.yaml`) are maintained in the [`akash.bme.monitor`](https://github.com/ovrclk/server-mgmt/tree/main/mainnet/deployments/akash.bme.monitor) directory of the server-mgmt repo.

### Key design notes
- **Single replica by design.** Alert cooldown state is held in memory. Running multiple instances would produce duplicate Slack alerts.
- **Stateless.** No persistent volumes. The pod can be safely force-deleted and rescheduled without data loss.
- **Config via ConfigMap.** All tunable parameters (poll intervals, thresholds, networks, relayers) live in the ConfigMap — no code change or image rebuild required for config updates.
- **Secret injection.** The Slack webhook URL is injected at runtime from a K8s Secret (`price-feed-monitor-secrets`) and never stored in the ConfigMap or image.

### Current image
```
scarruthers/price-feed-monitor:v9
```

---

## Configuration Updates

### Change config only (no new image needed)
Applies to: adding/removing networks or relayers, adjusting poll intervals or alert thresholds, updating wallet addresses or health endpoints, changing report schedule.

```bash
kubectl apply -f deploy/configmap.yaml
kubectl rollout restart deployment/price-feed-monitor -n akash-services
```

### Deploy a new image version
Applies to: code changes to alert logic, new monitoring components, bug fixes.

```bash
# Build and push
docker buildx build --platform linux/amd64,linux/arm64 \
  --tag scarruthers/price-feed-monitor:vN --push .

# Update image tag in deploy/deployment.yaml, then:
kubectl apply -f deploy/configmap.yaml
kubectl apply -f deploy/deployment.yaml
kubectl rollout status deployment/price-feed-monitor -n akash-services
```

---

## What Is Monitored

### Scheduled Health Reports
Full status summary posted to `#akash-bme-monitor` at **08:00 and 20:00 CST** daily, and on every pod restart.

---

### 1. Oracle Price Health
**Poll:** every 60s

Fetches the latest AKT/USD price from the Akash oracle module and checks how old it is.

| Age | Severity |
|-----|----------|
| > 5 min | Warning |
| > 15 min | Critical |
| > 30 min | Emergency |

---

### 2. Hermes Relayer Health
**Poll:** every 30s — alerts after **3 consecutive failures**

For each relayer (`hermes-relayer-04/05/06`):
- `/health` endpoint reachable and `isRunning = true`
- Wallet balance vs. thresholds (Info: < 1000 AKT, Warning: < 500 AKT, Critical: < 100 AKT)
- `contractAddress` and `priceFeedId` match expected values (misconfiguration detection)

---

### 3. Guardian Set — Ethereum RPC
**Disabled on mainnet as of 2026-08-12** — Pyth dropped support for the legacy wormhole/pyth-wasm contract path; Component 7 (Router Set) is now the sole mandatory currency check. Still enabled on `deploy-testnet/` until the transition window there closes; disabled on `deploy-sandbox/` already.

**Poll:** every 10 min (when enabled)

Reads the current Wormhole guardian set directly from the Ethereum contract (`0x98f3c9e6...`) and compares the guardian addresses against what the Akash Wormhole CosmWasm contract holds on-chain. Alerts if the sets are out of sync.

---

### 4. Guardian Set — Wormholescan
**Disabled on mainnet as of 2026-08-12** — same rationale as Component 3 above.

**Poll:** every 30 min (when enabled)

Secondary detection path via the Wormholescan REST API. Monitors for guardian set index changes and retrieves the governance VAA needed for on-chain submission if an upgrade occurs. Complements Component 3 — does not require an Ethereum RPC.

---

### 7. Router Set — Pyth Hermes
**Poll:** every 10 min

Replaces Components 3 & 5 post-Pyth-core-upgrade. Fetches a live price update VAA from the authenticated Hermes endpoint (`pyth.dourolabs.app/hermes`) and decodes the active `router_set_index`, then compares it against each network's `pyth-vaa` contract's stored index. Alerts if they differ — the contract is behind the active router set and price submissions will start failing with `InvalidRouterSetIndex`. Also alerts (Warning, single-shot) if the Hermes endpoint itself is unreachable, since a router set rotation could go undetected while it's down.

---

### 5. BME (Burn Mint Equilibrium) Status
**Poll:** every 60s

Monitors the Akash token burn/mint mechanism. Three independent checks:

**Collateral ratio vs. chain-defined thresholds** (thresholds read dynamically from chain):

| Consecutive polls breaching | Severity |
|-----------------------------|----------|
| 1 | Warning |
| 2 | Critical |
| 3 | Emergency (final) |

**Collateral ratio advance warning** (fixed thresholds, independent of the chain's own warn/halt): the chain-defined thresholds above sit just below 1.0, so they only trip once a halt is imminent. These give earlier notice:

| Ratio | Severity |
|-------|----------|
| <= `collateral_ratio_warn_at` (default 1.25) | Warning |
| <= `collateral_ratio_critical_at` (default 1.0) | Critical |

Each level fires **once** per breach (not on every poll) and resets once the ratio recovers back above `collateral_ratio_warn_at`, so a later dip alerts again.

**Minting or refunds halted** (`mints_allowed` / `refunds_allowed`):

| Consecutive polls halted | Severity |
|--------------------------|----------|
| 1 | Warning |
| 2 | Critical |
| 3 | Emergency (final) |

Alert includes halt reason decoded from the chain status field (e.g. oracle staleness vs. collateral breach).

**Inconsistent API response** (ratio = 0 with healthy status): Warning with explanation — threshold alerts suppressed for that poll cycle.

---

### 6. Guardian Update Announcements
**Poll:** every 30 min

Monitors the [Pyth Network governance forum](https://forum.pyth.network/c/proposals/7.rss) RSS feed for posts matching keywords: `guardian`, `guardian set`, `wormhole core`, `guardian rotation`, `guardian key`. Alerts on first match per post.
