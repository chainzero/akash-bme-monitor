# Mainnet Pyth Core Upgrade — Deployment Runbook

This runbook covers deploying the updated monitoring app (v21) when the mainnet
Pyth core upgrade occurs. All code and manifests are already prepared — you only
need to fill in addresses that aren't known until the contracts are deployed.

## Prerequisites — gather before starting

Obtain the following from Pyth/Douro Labs before running any commands:

| Value | Where to use |
|---|---|
| `pyth-vaa` contract address on mainnet | Step 2 |
| New Hermes relayer 1 — health URL, wallet, contract (pyth-pro) | Step 3 |
| New Hermes relayer 2 — health URL, wallet, contract (pyth-pro) | Step 3 |
| New Hermes relayer 3 — health URL, wallet, contract (pyth-pro) | Step 3 |
| Pyth API key (same key as sandbox) | Step 4 |

---

## Step 1 — Pull latest code on the host

```bash
cd ~/akash-bme-monitor   # or wherever the repo lives on this machine
git pull
```

The `deploy/deployment.yaml` already references v21. No changes to that file are needed.

---

## Step 2 — Fill in the pyth-vaa contract address

Replace `FILL_IN_PYTH_VAA_CONTRACT_MAINNET` with the actual contract address:

```bash
# macOS:
sed -i '' 's/FILL_IN_PYTH_VAA_CONTRACT_MAINNET/akash1REPLACE_WITH_ACTUAL_ADDRESS/g' deploy/configmap.yaml

# Linux:
sed -i 's/FILL_IN_PYTH_VAA_CONTRACT_MAINNET/akash1REPLACE_WITH_ACTUAL_ADDRESS/g' deploy/configmap.yaml
```

Verify the replacement landed correctly:

```bash
grep pyth_vaa_contract deploy/configmap.yaml
```

---

## Step 3 — Add the three new Hermes relayer entries

Open `deploy/configmap.yaml` in an editor. Near the bottom of the `hermes_relayers`
list you will see three commented-out template blocks labeled `hermes-relayer-new-01`
through `hermes-relayer-new-03`. For each one:

1. Remove the leading `#` from every line of the block
2. Replace each `FILL_IN_*` placeholder with the real value

The fields to fill in per relayer:
- `name` — any descriptive name (e.g. `hermes-relayer-new-01`)
- `health_endpoint` — the `/health` URL for this relayer
- `wallet` — the relayer's Akash wallet address
- `expected_contract_address` — the new pyth-pro contract address

The `expected_price_feed_id` is already set to the AKT/USD feed — leave it unchanged.

---

## Step 4 — Add the Pyth API key to the cluster secret

This only adds the new key; existing slack and etherscan keys are untouched.

```bash
kubectl patch secret price-feed-monitor-secrets -n akash-services \
  -p '{"stringData":{"pyth-api-key":"REPLACE_WITH_ACTUAL_PYTH_API_KEY"}}'
```

---

## Step 5 — Apply and verify

```bash
# Apply updated config and deployment
kubectl apply -f deploy/configmap.yaml
kubectl apply -f deploy/deployment.yaml

# Watch the rollout
kubectl rollout status deployment/price-feed-monitor -n akash-services

# Check startup logs — look for:
#   "router set monitor enabled"
#   "oracle price monitor enabled"
#   "hermes health monitor enabled"
kubectl logs -n akash-services deployment/price-feed-monitor --tail=60
```

A Slack message "BME Price Feed Monitor Restarted" will arrive within ~30 seconds.
The report should show:
- Oracle Price: ✅
- All 6 Hermes relayers (3 old + 3 new): ✅ running
- Guardian Set: ✅ in sync (old contracts still active)
- Router Set: ✅ index N | pyth-vaa in sync
- Pyth Hermes API: ✅ operational

---

## Rollback

If anything goes wrong, revert to the previous image:

```bash
kubectl set image deployment/price-feed-monitor \
  price-feed-monitor=scarruthers/price-feed-monitor:v18 \
  -n akash-services
```

---

## After the transition period (~2 weeks post-upgrade)

Once Pyth/Douro Labs confirms the old wormhole contracts are fully retired,
contact Scott to disable Components 3 and 5 in the configmap and remove the
three old relayer entries.
