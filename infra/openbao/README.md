# Reelrelay OpenBao access

Terraform owns only `reelrelay-read` and the `reelrelay` role under `auth/kubernetes`. The Helm chart owns the ESO identity, TokenReview-only RBAC, SecretStore, and ExternalSecret. ESO creates the Secret named by `existingSecret` (currently `reelrelay-secrets`). The bot keeps its separate, unprivileged pod identity.

The policy reads **one KV v2 entry**, `kv/apps/reelrelay`. It cannot list, write, or read child paths. Terraform does not read or store application values. The entry must contain `TELEGRAM_BOT_TOKEN`; `INSTAGRAM_SESSION_ID` is optional.

## 1. Prepare the shared platform

Use Terraform, the OpenBao CLI (`bao`), Python 3, and `kubectl` with your cluster context. On `fium`, you can use `sudo k3s kubectl` in place of `kubectl`. Run the repository commands from the Reelrelay root in the same shell.

Set the endpoint and private local state directory. Replace `YOUR_ADMIN_USER` with your existing administrator username. Enter the password at the CLI prompt; do not use a root token:

```bash
export BAO_ADDR=https://bao.kevinpita.com
umask 077
state_dir="$HOME/.local/state/reelrelay/openbao"
mkdir -p "$state_dir"
chmod 700 "$state_dir"
bao login -method=userpass -no-print username=YOUR_ADMIN_USER
```

The platform owner must provide the permanent `kubernetes/` auth mount. Check it with `bao auth list`. **Only if it is absent**, the platform owner runs this one-time setup:

```bash
kubectl -n default get configmap kube-root-ca.crt \
  -o jsonpath='{.data.ca\.crt}' > "$state_dir/k3s-ca.crt"
bao auth enable -path=kubernetes kubernetes
bao write auth/kubernetes/config \
  kubernetes_host=https://fium.tail235c8.ts.net:6443 \
  kubernetes_ca_cert=@"$state_dir/k3s-ca.crt" \
  disable_local_ca_jwt=true
```

Do not overwrite an existing mount configuration without platform-owner approval. It must use the login JWT for TokenReview, with no stored `token_reviewer_jwt`. The chart grants `create` on `tokenreviews` to `reelrelay:openbao-reader`. ESO must have permission to request tokens for that account; the platform ESO chart supplies this permission.

The shared mount, OpenBao service, KV engine, and ESO installation are not application resources. Keep them in platform ownership. Do not delete them when removing Reelrelay. No demo resources are needed or changed.

Platform reference: `~/nixos-config` at `b088ade`, in `modules/selfhosted/secrets.nix`, `modules/selfhosted/web.nix`, `modules/hosts/fium.nix`, and `flake.lock`. The pins are OpenBao **2.6.2** and ESO **2.10.0**. This configuration uses `hashicorp/vault` **5.10.0** for OpenBao's compatible policy and Kubernetes-role APIs. Provider schema checks do not replace a live integration check.

## 2. Set the application values

Get a Kubernetes API audience. This command requests a short-lived token but prints only its public audience, not the token:

```bash
kubectl -n default create token default --duration=10m | python3 -c \
  'import base64,json,sys; p=sys.stdin.read().strip().split(".")[1]; print(json.loads(base64.urlsafe_b64decode(p + "=" * (-len(p) % 4)))["aud"][0])'
```

In [`../chart/values.yaml`](../chart/values.yaml), set `openbao.audience` to that value and set `openbao.enabled: true`. Terraform reads the same file. Do not override these fields only in Argo CD. Do not use an arbitrary audience such as `vault`: the login JWT also authenticates to the Kubernetes API.

Set `image.tag` or `image.digest` to a published image. The current `<pending>` tag is not deployable. Do not commit or sync the values yet if Argo CD has automatic sync enabled.

## 3. Apply application access

Use your existing administrator login. `bao print token` below passes the token to Terraform without printing it. The provisioning account needs policy/role administration, token self-lookup, and child-token creation. The runtime account cannot provision its own access.

```bash
export VAULT_TOKEN="$(bao print token)"
terraform -chdir=infra/openbao init \
  -backend-config="path=$state_dir/terraform.tfstate"
terraform -chdir=infra/openbao fmt -check
terraform -chdir=infra/openbao validate
```

If the policy or role already exists, inspect its ownership. Import it before planning only if it belongs to this application:

```bash
terraform -chdir=infra/openbao import vault_policy.reelrelay reelrelay-read
terraform -chdir=infra/openbao import vault_kubernetes_auth_backend_role.reelrelay auth/kubernetes/role/reelrelay
```

Create the plan:

```bash
terraform -chdir=infra/openbao plan -out="$state_dir/access.tfplan"
```

Review the plan. A new setup must add only the policy and the role. Then apply it:

```bash
terraform -chdir=infra/openbao apply "$state_dir/access.tfplan"
unset VAULT_TOKEN
rm "$state_dir/access.tfplan"
```

Keep state and plans private. Back up the state directory securely. Use this same backend path for all later runs, and do not run concurrent applies. Git ignores state, plans, and variable files; commit `.terraform.lock.hcl`.

## 4. Sync with Argo CD

Use the existing Application with source path `infra/chart` and destination namespace `reelrelay`. Let Argo CD create the namespace with `CreateNamespace=true`; the chart does not declare a second Namespace object. Argo CD must be allowed to manage the chart's ClusterRole and ClusterRoleBinding.

Keep only one installation of this chart in `reelrelay`. Before the first sync, check for existing `openbao-reader`, `openbao`, `reelrelay-secrets`, and the target Secret. Resolve any other owner's resources before ESO adopts the target. Do not apply the chart with Helm as well as Argo CD.

Commit the reviewed values and sync the Application. Then check readiness without printing Secret data:

```bash
kubectl -n reelrelay wait secretstore/openbao --for=condition=Ready --timeout=120s
kubectl -n reelrelay wait externalsecret/reelrelay-secrets --for=condition=Ready --timeout=120s
kubectl -n reelrelay get deployment
```

Check access denial with the runtime identity, not the administrator. The read below must return **403 permission denied**, not 404 or a connection error. It discards any response body:

```bash
(
  unset VAULT_TOKEN
  audience="$(kubectl -n reelrelay get secretstore openbao -o jsonpath='{.spec.provider.vault.auth.kubernetes.serviceAccountRef.audiences[0]}')"
  export BAO_TOKEN="$(kubectl -n reelrelay create token openbao-reader --duration=10m --audience="$audience" |
    bao write -field=token auth/kubernetes/login role=reelrelay jwt=-)"
  test -n "$BAO_TOKEN" || exit 1
  bao read kv/data/apps/reelrelay-access-check >/dev/null || true
  bao token revoke -self >/dev/null
)
```

The OpenBao role caps runtime tokens at ten minutes. ESO requests ten-minute Kubernetes tokens. Secret updates sync every minute, but environment variables change only when the bot pod restarts. Restart the Deployment after rotating credentials.

Removing the ExternalSecret can delete its owned Kubernetes Secret. `deletionPolicy: Retain` prevents deletion on a missing OpenBao entry, not on ExternalSecret removal. Removing the Terraform role/policy does not erase the KV entry or the Kubernetes Secret. Neither action revokes credentials already loaded by the bot.
