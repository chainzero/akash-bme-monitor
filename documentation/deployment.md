# Build and deployment

## Images

Images are published to **`ghcr.io/ovrclk/akash-bme-monitor`** by GitHub Actions. Nobody builds or pushes images by hand.

| Trigger | Result |
|---|---|
| Pull request | `go vet`, `go test -race`, secret scan — must pass to merge |
| Push a `vN` tag on `main` | Builds `linux/amd64` and pushes `ghcr.io/ovrclk/akash-bme-monitor:vN` |

To release:

```bash
git checkout main && git pull
git tag v23
git push origin v23
```

Then watch the release workflow under the repo's **Actions** tab. The image is ready when it goes green.

The package is private, so every cluster that pulls it needs the `ghcr-pull` image pull secret in `akash-services` (see [first-time setup](#first-time-setup-per-environment)).

### Local build (testing only)

```bash
docker build --platform linux/amd64 -t akash-bme-monitor:local .
docker run --rm \
  -e SLACK_WEBHOOK_URL="https://hooks.slack.com/services/..." \
  -v "$(pwd)/config.yaml:/etc/price-feed-monitor/config.yaml:ro" \
  akash-bme-monitor:local
```

Always pass `--platform linux/amd64` on Apple Silicon — the cluster is amd64.

## Kubernetes

Each environment is one single-replica Deployment in `akash-services` on the `provider-monitoring` cluster.

| Environment | Deployment | ConfigMap | Secret |
|---|---|---|---|
| Mainnet | `price-feed-monitor` | `price-feed-monitor-config` | `price-feed-monitor-secrets` |
| Sandbox | `price-feed-monitor-sandbox` | `price-feed-monitor-sandbox-config` | `price-feed-monitor-sandbox-secrets` |
| Testnet | `price-feed-monitor-testnet` | `price-feed-monitor-testnet-config` | `price-feed-monitor-testnet-secrets` |

Copies of the mainnet manifests are also kept in [`ovrclk/server-mgmt`](https://github.com/ovrclk/server-mgmt/tree/main/mainnet/deployments/akash.bme.monitor). Keep both in step when changing them.

**Single replica by design.** Alert cooldown state is in memory; a second replica would double-post every alert.

### First-time setup (per environment)

Examples use mainnet; substitute the sandbox/testnet names from the table above.

```bash
# 1. Namespace
kubectl apply -f deploy/namespace.yaml

# 2. GHCR pull secret — use a token owned by an ovrclk service/bot account with
#    read:packages, not a personal token (it stops working when that person leaves)
kubectl create secret docker-registry ghcr-pull \
  --namespace akash-services \
  --docker-server=ghcr.io \
  --docker-username=<service-account> \
  --docker-password=<token>

# 3. App secrets — create from the command line so values never touch disk
kubectl create secret generic price-feed-monitor-secrets \
  --namespace akash-services \
  --from-literal=slack-webhook-url="https://hooks.slack.com/services/..." \
  --from-literal=pyth-api-key="..." \
  --from-literal=etherscan-api-key="..."

# 4. Config and Deployment
kubectl apply -f deploy/configmap.yaml
kubectl apply -f deploy/deployment.yaml
kubectl rollout status deployment/price-feed-monitor -n akash-services
```

`deploy*/secret.yaml` are templates with placeholder values only. Never commit real values to them.

### Security context

- Distroless `static:nonroot` image, runs as uid/gid 65532.
- Read-only root filesystem, no privilege escalation, no service account token.
- `capabilities: drop: [ALL]` is intentionally **not** set — on this k3s/Flannel cluster it breaks UDP return traffic and therefore DNS.
