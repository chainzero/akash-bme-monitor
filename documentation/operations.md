# Operations

Commands use mainnet names; for other environments see the table in [deployment.md](deployment.md#kubernetes).

## Change config only

For networks, relayers, wallets, thresholds, poll intervals, and report schedule. No new image is needed.

```bash
# Edit deploy/configmap.yaml, then:
kubectl apply -f deploy/configmap.yaml
kubectl rollout restart deployment/price-feed-monitor -n akash-services
kubectl rollout status deployment/price-feed-monitor -n akash-services
```

Apply only the ConfigMap. Re-applying `deployment.yaml` is unnecessary and can roll the image back if the file's tag is behind the cluster.

A "🔄 BME Price Feed Monitor Restarted" report arrives in Slack within about 30 seconds. Check it shows the change.

## Deploy a new version

For code changes: alert logic, new monitors, fixes.

1. Merge to `main` and tag a release (see [deployment.md](deployment.md#images)).
2. Update the image tag in `deploy/deployment.yaml` (and `ovrclk/server-mgmt`), then:

```bash
kubectl apply -f deploy/deployment.yaml
kubectl rollout status deployment/price-feed-monitor -n akash-services
```

## Roll back

```bash
kubectl rollout undo deployment/price-feed-monitor -n akash-services
# or pin a specific version:
kubectl set image deployment/price-feed-monitor \
  price-feed-monitor=ghcr.io/ovrclk/akash-bme-monitor:vN -n akash-services
```

## Status and logs

```bash
kubectl get pods -n akash-services -l app=price-feed-monitor
kubectl logs -n akash-services deployment/price-feed-monitor --tail=100 -f
```

Logs are structured (`slog`). Useful filters: `component=router_set_monitor`, `"slack alert sent"`, `"alert suppressed by cooldown"`.

## Rotate a secret

```bash
kubectl patch secret price-feed-monitor-secrets -n akash-services \
  -p '{"stringData":{"pyth-api-key":"NEW_VALUE"}}'
kubectl rollout restart deployment/price-feed-monitor -n akash-services
```

Secrets are read at startup, so a restart is required.

## Fund a relayer wallet

Wallet alerts include the exact command. General form:

```bash
akash tx bank send default <relayer-wallet> 10000000000uakt --from default -y
```

## Things to know

- **Restarts re-alert.** Cooldown state is in memory, so every active condition posts again right after a restart.
- **The pod is safe to delete.** It has no volumes and no state; it can be force-deleted and rescheduled freely.
- **Test a guardian VAA fetch** (legacy path): `go run . test-vaa --target-index N` with `ETHERSCAN_API_KEY` set.
