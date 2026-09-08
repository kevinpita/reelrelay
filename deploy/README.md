# Deploy with Helm

The chart is in [`chart/`](chart/). It creates one Deployment. The bot uses outbound Telegram long polling, so it needs no Service, Ingress, public URL, or persistent volume.

## 1. Select the image and chart values

After CI publishes an image, copy its digest from the workflow summary. Set `image.repository` and `image.digest` in [`chart/values.yaml`](chart/values.yaml). Keep the other entries:

```yaml
image:
  repository: ghcr.io/your-owner/your-repo
  digest: sha256:replace-with-the-published-digest
```

Use lowercase owner and repository names. A non-empty `image.digest` takes priority over `image.tag`. To use a commit tag instead, clear the digest and set `image.tag` to `sha-<full-commit>`.

Other chart settings:

| Value | Default | Purpose |
| --- | --- | --- |
| `existingSecret` | `igbot-secrets` | Secret with the bot token and optional session ID |
| `cookiesSecret` | Empty | Optional Secret containing `cookies.txt` |
| `imagePullSecrets` | `[]` | Existing registry pull Secrets |
| `image.pullPolicy` | `IfNotPresent` | Container image pull policy |
| `maxConcurrentDownloads` | `2` | Concurrent downloads; the app accepts 1 to 16 |
| `tmpSizeLimit` | `1Gi` | Temporary download volume limit |
| `resources` | See `values.yaml` | CPU, memory, and ephemeral-storage requests and limits |

Keep credentials out of chart values. These settings contain Secret names, not Secret contents.

Validate from the repo root without accessing a cluster:

```bash
just k8s-render
just k8s-check
```

These commands run `helm template`, `helm lint --strict`, and Kubernetes schema checks. The placeholder image is intentional. Select a published image and create the runtime Secret before deployment.

## 2. Create the runtime Secret

Use a secret manager, External Secrets, or SOPS for production. The chart references an existing Secret; it does not create one.

For a manual test, use a private `.env` file with `KEY=value` entries. Do not use quotes or an `export` prefix.

```bash
kubectl create namespace igbot --dry-run=client -o yaml | kubectl apply -f -
kubectl -n igbot create secret generic igbot-secrets \
  --from-env-file=.env --dry-run=client -o yaml | kubectl apply -f -
```

`TELEGRAM_BOT_TOKEN` is required. `INSTAGRAM_SESSION_ID` is optional. If you choose a different Secret name, set `existingSecret` to that name.

Do not save rendered Secret YAML in Git. Base64 encoding does not protect secrets. After changing a Secret used through environment variables, restart the Deployment or configure a secret-reload controller:

```bash
kubectl -n igbot rollout restart deployment/igbot
```

### Cookie-file option

To use Netscape-format cookies instead of a session ID:

```bash
kubectl -n igbot create secret generic igbot-cookies \
  --from-file=cookies.txt=/absolute/path/to/cookies.txt \
  --dry-run=client -o yaml | kubectl apply -f -
```

Set this value in `chart/values.yaml`:

```yaml
cookiesSecret: igbot-cookies
```

The chart mounts the Secret read-only at `/var/run/instagram`. Each download gets a private, writable cookie copy in `/tmp`. The original Secret stays unchanged.

## 3. Set registry access

A public GHCR package needs no pull Secret. GitHub may create a new package as private. Set its visibility explicitly if public access is intended.

For a private package, provision a registry Secret through your secret manager with `read:packages` access. Reference it in `chart/values.yaml`:

```yaml
imagePullSecrets:
  - name: ghcr-pull
```

Keep registry credentials separate from the bot token.

## 4. Install or update the release

Run this command from the repo root after the image, values, and runtime Secret are ready:

```bash
helm upgrade --install igbot deploy/chart \
  --namespace igbot --create-namespace --wait --timeout 5m
```

To deploy a new image, update `image.digest` in the chart values and run this command again. CI has no Kubernetes credentials. Publishing an image or changing Git does not deploy the bot.

## Runtime requirements

- The template fixes the replica count at **one** and uses `Recreate`. Do not add an HPA or run another bot with the same token. Updates cause a short service interruption.
- The chart selects Linux x64 nodes. It keeps non-root execution, a read-only root filesystem, dropped capabilities, and no Kubernetes service-account token.
- `/tmp` is writable and disk-backed. Adjust `tmpSizeLimit` and ephemeral-storage resource limits together.
- Allow DNS and outbound HTTPS to Telegram, Instagram, and media CDNs. Allow node health probes if required by your network policy.
- Readiness confirms startup, not continued upstream availability. The process has 30 seconds to exit after termination and does not retain cancelled jobs.
- Deployment names follow the Helm release name. The examples use `igbot`.

## Inspect and roll back

```bash
kubectl -n igbot rollout status deployment/igbot
kubectl -n igbot logs deployment/igbot --tail=100
```

To roll back an image, restore the previous `image.digest` in `chart/values.yaml` and run the Helm command again.
