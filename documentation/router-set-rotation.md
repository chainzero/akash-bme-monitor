# Router set rotation response

Pyth price updates are signed by a router set identified by a `router_set_index`. Each network's `pyth-vaa` contract only accepts updates signed by the index it has stored. When Pyth rotates to a new router set, the contract must be updated. Until it is, relayer submissions fail with `InvalidRouterSetIndex` and the on-chain oracle price goes stale.

## Alerts

| Alert | Meaning |
|---|---|
| **ROUTER SET ROTATION: INDEX N → M** | Hermes is now serving updates signed by a newer router set. |
| **ROUTER SET OUT OF SYNC — `<network>`** | That network's `pyth-vaa` contract index differs from the live Hermes index. Submissions on that network are failing or about to. |
| **HERMES API UNREACHABLE** | The monitor can't see the live index, so a rotation could go undetected. Check that `PYTH_API_KEY` is valid. |

Expect oracle price staleness alerts (and eventually BME halt alerts) to follow an out-of-sync alert if nothing is done.

## Response

1. **Confirm** the indexes in the alert body (Active Hermes Index vs. Contract Index), or in the latest health report's `Router Set:` line.
2. **Contact Pyth / Douro Labs** for the router set upgrade VAA and the exact submission procedure.
3. **Submit** the upgrade to each out-of-sync network's `pyth-vaa` contract (a `tx wasm execute` against the address in the alert).
4. **Verify**: within one poll (10 minutes) the monitor posts ✅ **ROUTER SET IN SYNC — `<network>`**. Oracle price alerts should clear once relayers resume submitting.

Contract addresses per network are the `pyth_vaa_contract` values in each `deploy*/configmap.yaml`.
