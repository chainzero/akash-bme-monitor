# Monitors

Each monitor runs in its own goroutine on its own poll interval and sends alerts independently. Poll intervals below are the mainnet values from `deploy/configmap.yaml`.

| # | Monitor | Config key | Poll | Mainnet |
|---|---|---|---|---|
| 1 | Oracle price + wallet balances | `oracle_price_monitor` | 60s | Enabled |
| 2 | Hermes relayer health | `hermes_health_monitor` | 30s | Enabled |
| 3 | Guardian set — Ethereum RPC | `guardian_set_monitor` | 10m | **Disabled** |
| 4 | Guardian announcements (Pyth forum, Wormhole GitHub) | `announcement_monitor` | 30m / 1h | Enabled |
| 5 | Guardian set — Wormholescan | `wormholescan_monitor` | 30m | **Disabled** |
| 6 | BME status | `bme_monitor` | 60s | Enabled |
| 7 | Pyth router set | `router_set_monitor` | 10m | Enabled |

Monitors 3 and 5 were disabled on mainnet on 2026-08-12 after the Pyth core upgrade retired the legacy Wormhole/pyth-wasm contract path. Monitor 7 replaces them.

---

## How alerting works

- **Severities**: Info → Warning → Critical → Emergency.
- **One alert per condition**: each condition has a stable key (e.g. one per relayer wallet). When the condition clears, a ✅ resolved message is posted.
- **Repeat interval (cooldown)**: while a condition persists at the same severity, the alert is re-posted at most once per cooldown — 10 minutes by default. Some alerts override this (see wallet balances).
- **Escalation bypasses the cooldown**: moving to a higher severity posts immediately.
- **Restarts reset state**: cooldowns live in memory, so a pod restart re-posts any active condition on the first check.

## Health reports

A full status report is posted on every pod start and at **08:00 and 20:00 America/Chicago** (`report.schedule_times`). It shows oracle price and age, each relayer's status and wallet balance, BME status, router set sync, and whether the Pyth Hermes API is reachable.

---

## 1. Oracle price + wallet balances

**Oracle price freshness** — fetches the latest AKT/USD price from the Akash oracle module (`/akash/oracle/v2/prices` when `oracle_api_version: v2`) and checks its age.

| Price age | Severity |
|---|---|
| > 5 min | Warning |
| > 15 min | Critical |
| > 30 min | Emergency |

**Relayer wallet balances** — checked on the same 60s cycle for every relayer with a `wallet` set.

| Balance | Severity | Repeats every |
|---|---|---|
| < 1,000 AKT (`info_wallet_balance`) | Info | 12 hours |
| < 500 AKT (`warn_wallet_balance`) | Warning | 6 hours |
| < 100 AKT (`min_wallet_balance`) | Critical | 1 hour |

Each alert includes a ready-to-run funding command (10,000 AKT). Recovering above 1,000 AKT posts a resolved message. A partial top-up that moves the wallet to a lower tier does not post immediately — the lower tier waits for its own repeat interval.

## 2. Hermes relayer health

Polls each relayer's `/health` endpoint every 30s.

| Condition | Severity |
|---|---|
| Endpoint unreachable or non-200 | Critical |
| `isRunning: false` | Critical |
| `lastPriceUpdateAt` older than `last_price_update_max_age` (2m) — price submissions stalled | Critical |
| `priceFeedId` / `contractAddress` differ from `expected_*` in config | Warning |

Unreachable and stopped alerts fire on the **first** failed check and repeat every 10 minutes. `consecutive_failures_threshold` is logged at startup but not currently enforced.

## 4. Guardian announcements

Early-warning signals for Wormhole guardian set changes. Both alert at Warning, once per matching item.

- **Pyth forum** — the governance RSS feed, matched against `keywords` in config.
- **Wormhole GitHub** — a new `vN.prototxt` in `guardianset/mainnetv2/canonical_sets/`, or an open PR whose title matches rotation keywords.

## 6. BME status

Polls `/akash/bme/v1/status`. The chain's own warn/halt thresholds are read from the chain on every poll, so governance changes are picked up automatically.

**Collateral ratio below chain thresholds, or mints/refunds halted** — escalates per consecutive poll:

| Consecutive polls | Severity |
|---|---|
| 1 | Warning |
| 2 | Critical |
| 3 | Emergency (final alert) |

Halt alerts include the decoded halt reason (e.g. oracle staleness vs. collateral breach).

**Advance collateral warning** — the chain thresholds sit just below 1.0 and only trip when a halt is imminent, so these give earlier notice:

| Ratio | Severity |
|---|---|
| ≤ `collateral_ratio_warn_at` (1.25) | Warning |
| ≤ `collateral_ratio_critical_at` (1.0) | Critical |

Each level fires once per breach and re-arms after the ratio recovers above `collateral_ratio_warn_at`.

**Inconsistent response** (ratio 0 with a healthy status) — Warning; threshold alerts are skipped for that cycle.

## 7. Pyth router set

Every 10 minutes:

1. Fetches a live price update from the authenticated Hermes endpoint (`pyth.dourolabs.app/hermes`, using `PYTH_API_KEY`) and decodes the active `router_set_index`.
2. Queries each network's `pyth-vaa` contract for the index it currently accepts.

| Condition | Severity |
|---|---|
| Contract index ≠ live Hermes index — submissions will fail with `InvalidRouterSetIndex` | Critical |
| Live Hermes index increased since the last poll (rotation) | Critical |
| Hermes unreachable — 1st / 2nd / 3rd consecutive failure | Warning / Critical / Emergency, then silent until recovery |

If the `pyth-vaa` contract itself can't be queried, the monitor only logs — node reachability is covered by monitors 1 and 6. See [router-set-rotation.md](router-set-rotation.md) for the response.

## 3 & 5. Guardian set (legacy, disabled on mainnet)

- **3 — Ethereum RPC**: compares the guardian set in the Ethereum Wormhole contract (`0x98f3c9e6…`) with the Akash Wormhole CosmWasm contract.
- **5 — Wormholescan**: watches the global guardian set index and fetches the governance VAA when it changes.

Both are still enabled on testnet. They can be removed once the legacy path is retired everywhere.
