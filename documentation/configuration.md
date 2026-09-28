# Configuration

All behaviour is driven by a single YAML file. In Kubernetes it comes from the environment's ConfigMap (`deploy*/configmap.yaml`) mounted at `/etc/price-feed-monitor/config.yaml`. The repo-root `config.yaml` is for local development.

Config changes never need a new image — see [operations.md](operations.md#change-config-only).

## Config file location

In order of precedence:

1. `PRICE_FEED_MONITOR_CONFIG` environment variable (set to `/etc/price-feed-monitor/config.yaml` in the image)
2. `-config <path>` flag
3. `./config.yaml`

## Secrets (environment variables)

Secrets are never put in the config file. They are injected from the environment's Kubernetes Secret and override the matching config field.

| Variable | Secret key | Used by | Required |
|---|---|---|---|
| `SLACK_WEBHOOK_URL` | `slack-webhook-url` | All alerts and reports | Yes |
| `PYTH_API_KEY` | `pyth-api-key` | Router set monitor (Hermes) | When `router_set_monitor` is enabled |
| `ETHERSCAN_API_KEY` | `etherscan-api-key` | Guardian set monitor | When `guardian_set_monitor` is enabled |

## File structure

```yaml
slack:              # webhook_url (placeholder — overridden by env), channel
report:             # timezone, schedule_times for the health report
oracle_price_monitor:
hermes_health_monitor:
guardian_set_monitor:
wormholescan_monitor:
router_set_monitor:
bme_monitor:
announcement_monitor:
networks:           # one entry per Akash network to monitor
```

Each monitor section has `enabled` and `poll_interval`, plus its own thresholds. See [monitors.md](monitors.md) for what each setting controls; the commented `config.yaml` at the repo root documents every field.

## Networks

```yaml
networks:
  - name: mainnet
    akash_api:                         # tried in order; falls back on network errors / 5xx
      - "https://rpc.akt.dev/rest"
      - "https://api.akashnet.net:443"
    wormhole_contract: "akash1..."     # legacy guardian monitors only
    pyth_vaa_contract: "akash1..."     # router set monitor; omit to skip that network
    hermes_relayers: [...]
```

`akash_api` must be REST (LCD) endpoints, not RPC.

## Adding or changing a relayer

Add an entry under `networks[].hermes_relayers`. The monitors loop over whatever is configured, so no code change is needed.

```yaml
hermes_relayers:
  - name: hermes-relayer-07
    health_endpoint: "https://hermesrelayerprod7.akash.pub/health"
    wallet: "akash1..."                 # omit to skip balance checks
    info_wallet_balance: 1000000000     # uakt — 1000 AKT → Info
    warn_wallet_balance: 500000000      # uakt — 500 AKT  → Warning
    min_wallet_balance: 100000000       # uakt — 100 AKT  → Critical (0 disables balance checks)
    expected_price_feed_id: "0x4ea5bb4d2f5900cc2e97ba534240950740b4d3b89fe712a94a7304fd2fd92702"
    expected_contract_address: "akash1..."
```

Balance thresholds are in **uakt** (1 AKT = 1,000,000 uakt).

The relayer's `/health` endpoint is behind an NGINX allowlist on the relayer host. Add the monitoring cluster's egress IP to that allowlist for any new relayer.
