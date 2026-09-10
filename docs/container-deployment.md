# Container deployment

Production uses the `production` Go build tag: local OIDC/Paperless implementations,
synthetic JSON, banners and asset reload are absent from its binary. The separate
`development` target contains them. The image pins Go 1.27.1, Chromium
152.0.7977.82, OpenJDK 21.0.12, Alpine 3.24.1, multiarchitecture base-image
digests and the Dockerfile frontend. APK verifies repository signatures; every
security-sensitive direct runtime package is version-pinned. The generated SBOM
and provenance record the resolved closure for each immutable image digest.
Update the runtime versions and digests together after review; the CI freshness
and vulnerability gates reject stale or vulnerable runtime inputs.
The JDK is required for Java source launch of the embedded official CII schemas;
their manifest/license and Alpine package metadata are retained. Go runtime
and compiled module licenses/notices are under
`/usr/share/doc/invoice-generator/licenses`, alongside module inventories.

## Provision the Linux host

Use encrypted durable storage, current Docker Engine and Compose 2.24.4+.
Unprivileged user namespaces must be enabled. Pre-provision every root:

```sh
sudo install -d -o root -g root -m 0755 /srv/invoice
sudo install -d -o 65532 -g 65532 -m 0700 /srv/invoice/data /srv/invoice/data/database /srv/invoice/data/documents /srv/invoice/backup
sudo install -d -o root -g root -m 0755 /srv/invoice/config
sudo install -d -o root -g root -m 0700 /srv/invoice/secrets
sudo sync -f /srv/invoice
```

Install five secret files under `/srv/invoice/secrets`, owned by `65532:65532`
with mode `0400`: `oidc-client`, `session-key`, `transaction-key`,
`paperless-token`, `backup-key`. Session/transaction keys are independently random
32-byte values encoded as unpadded standard base64, without a newline. The backup
key is 32 **raw** random bytes. Export OIDC/Paperless values from a secret manager
to protected files; never pass their contents through flags/environment, shell
transcripts or this checkout. An empty Paperless file is permitted when disabled.
Sync the secret directory and retain an independently encrypted backup-key copy.

Compose file-backed secrets retain host ownership/modes: Compose `uid/gid/mode`
cannot repair them. Verify UID 65532 can read the actual mounted files. Bind
mounts use `create_host_path: false`; the entrypoint never creates or repairs
production roots. Data/documents/backups are writable; configuration and secrets
are separate read-only mounts. The root filesystem is read-only; all capabilities
are dropped, core dumps are disabled, and tmpfs/memory/CPU/PID/file limits apply.

Before starting production, check the actual mounts without printing contents:

```sh
docker compose run --rm --no-deps --entrypoint /bin/sh invoice -ec '
  test "$(id -u)" = 65532
  for secret in /run/secrets/oidc_client /run/secrets/session_key /run/secrets/transaction_key /run/secrets/paperless_token /run/secrets/backup_key; do
    test -r "$secret"
    test "$(stat -c %u:%g:%a "$secret")" = 65532:65532:400
    test ! -w "$secret"
  done
  for root in /data/database /data/documents /backup; do
    test "$(stat -c %u:%g:%a "$root")" = 65532:65532:700
    test -w "$root"
  done
  test ! -w /config
'
```

Fix ownership/modes on the host if this preflight fails; rebuilding the image
does not change bind-mounted permissions.

The seccomp policy derives from [Moby profiles commit 3c28324](https://github.com/moby/profiles/blob/3c28324314729dbade8287e868eef6338c42807a/seccomp/default.json),
with its Apache license in `docker/SECCOMP-LICENSE`. It additionally allows
`clone`, `unshare`, `setns` and `chroot` for Chrome's user/PID/network namespace
sandbox. Kernel namespace ownership and capability checks remain active. The
render test fails without `chroot`. Never add `SYS_ADMIN`, disable Chrome's
sandbox, remove `no-new-privileges` or use an unconfined seccomp profile.

## NetBird and Pocket ID topology

The example DNS names must be replaced with real private service names:

```sh
export INVOICE_ALLOWED_HOSTS=invoices.example.net
export INVOICE_POCKET_ID_ISSUER=https://id.example.net
export INVOICE_POCKET_ID_CLIENT_ID=invoice-generator
export INVOICE_CALLBACK_URL=https://invoices.example.net/auth/callback
export INVOICE_TRUSTED_PROXIES=172.30.80.1/32
docker compose config --quiet
docker compose up -d --build --wait
docker compose exec -T invoice /usr/local/bin/healthcheck
```

Browser → NetBird proxy uses verified TLS 1.3 at
`https://invoices.example.net`, restricted by the intended NetBird access policy.
The default same-host proxy → application hop uses `http://127.0.0.1:8080`;
Docker publishes only loopback. The exact trusted Docker gateway peer is
`172.30.80.1/32`. Confirm that address in the deployment, select a nonoverlapping
subnet, and use a fixed proxy `/32` instead if the proxy is containerized. The
proxy must **replace** client-supplied forwarded headers with the actual client
IP, `X-Forwarded-Proto: https`, `X-Forwarded-Host: invoices.example.net` and
`Host: invoices.example.net`. No wildcard proxy range or public app port is needed.

Application → Pocket ID uses verified HTTPS to the exact issuer origin. Allow
DNS and HTTPS to Pocket ID and optionally the exact Paperless service at the
host/NetBird firewall. The bridge permits outbound traffic for these dependencies;
it is not a firewall destination allowlist. Do not mark it internal without
providing the required routes.

Create a confidential OIDC client with Authorization Code flow, PKCE S256 and
the **single exact** callback `https://invoices.example.net/auth/callback`.
Request `openid profile email groups`; emit `groups` as an array in the signed
ID token. Create the exact group `invoice-admins` and assign authorized users.
Install the exported client secret in its protected file. Verify successful
member login and rejection of a nonmember before granting network access.

A proxy on another host requires application TLS: mount certificate/key in
`/config`, set `INVOICE_TLS_CERT` and `INVOICE_TLS_KEY` in an operator Compose
override, bind only the private NetBird interface, and make the proxy verify the
app certificate/DNS name. Override healthcheck to curl with `--cacert`,
`--resolve invoices.example.net:8080:127.0.0.1` and
`https://invoices.example.net:8080/health/ready`. Never use `--insecure`.
Verify each TLS hop independently with OpenSSL 3.5+:

```sh
openssl s_client -connect invoices.example.net:443 -servername invoices.example.net -verify_hostname invoices.example.net -verify_return_error -tls1_3 -groups X25519MLKEM768 -brief </dev/null
openssl s_client -connect PRIVATE_APP_IP:8080 -servername invoices.example.net -verify_hostname invoices.example.net -verify_return_error -CAfile app-ca.pem -tls1_3 -groups X25519MLKEM768 -brief </dev/null
openssl s_client -connect id.example.net:443 -servername id.example.net -verify_hostname id.example.net -verify_return_error -tls1_3 -groups X25519MLKEM768 -brief </dev/null
```

Confirm successful certificate verification and the negotiated hybrid group;
Go capability alone does not prove negotiation. The default same-host HTTP hop
has no TLS group and is explicitly a local trust boundary. No probe needs secrets.

## Health, logs, upgrade and recovery

The two exact unauthenticated GET paths return fixed payloads: `/health/live`
returns `ok`; `/health/ready` returns `ok` or `unavailable`. Readiness requires
startup callback validation/discovery, database access, matching complete
migrations, Java/JDK, real Chrome rendering and writable protected documents.
Chrome PDF and actual CII schema validation plus fresh OIDC discovery refresh
every 20 seconds; stale runtime status fails closed after 60 seconds. Initial
runtime failure prevents workers and listener startup. There are no versions, paths or dependency error details in health.

SIGTERM stops acceptance/job claims, drains writes for at most 10 seconds, stops
bounded workers, checkpoints SQLite and closes resources. Compose grants 35
seconds. JSON logs rotate across three 10-MiB files. Restrict log access, retain
only redacted operational logs for a defined period, and back up the SQLite audit
records independently of logs.

Before **every production upgrade**, create and verify an encrypted backup with
the current image (use a fresh filename):

```sh
docker compose exec -T invoice server backup -database /data/database/invoice.sqlite -document-root /data/documents -output /backup/pre-upgrade.enc -key-file /run/secrets/backup_key
docker compose exec -T invoice server backup verify -archive /backup/pre-upgrade.enc -key-file /run/secrets/backup_key
docker compose stop
docker compose up -d --build --wait
```

Migrations run before readiness. Retain the old image digest, verified encrypted
archive and matching key. Run the executable `./scripts/recovery-drill.sh UNIQUE_NAME` after stopping
production. It performs backup, verify, restore into `/backup/UNIQUE_NAME-restored`,
and integrity-check of `database.sqlite` plus `documents`, with the mounted
backup key. [Storage provisioning](server-storage.md) describes root durability. A restore drill must
target a **new absolute root** under a pre-provisioned volume with explicit
`-confirm`, then pass integrity checking and an authenticated document download
in an isolated instance. Rollback restores that archive into a new root and uses
the retained old image; never point old code at a newly migrated database.
Revoke sessions after recovery or key compromise:

```sh
docker compose exec -T invoice server sessions revoke-all -database /data/database/invoice.sqlite
```

Authenticated users can review and revoke their own active sessions at
`/settings/sessions`. The current session, a specific other session, or every
session can be terminated there. The application permits at most five active
sessions per Pocket ID identity and requires a fresh Pocket ID group check after
15 minutes; local session handling never extends that authorization window.
For targeted emergency administration, use the opaque identifier displayed in
the session page:

```sh
docker compose exec -T invoice server sessions revoke -database /data/database/invoice.sqlite -id SESSION_ID
```

## Development and verification

```sh
go run ./cmd/server serve -dev -dev-assets ./internal/web
docker compose -f compose.yaml -f compose.dev.yaml up -d --build --wait
docker compose -f compose.yaml -f compose.dev.yaml down
./scripts/ci-local.sh
# PowerShell: ./scripts/ci-local.ps1
```

Development binds **127.0.0.1:8080** in the host network namespace. This profile
targets Linux; Docker Desktop needs its explicit host-networking opt-in. If
unavailable, use native `go run`. Ordinary bridge publishing cannot reach a
container-loopback listener. The isolated named volume holds synthetic data.
Read-only template/static mounts reload per request; Go changes need a rebuild.
Production rejects reload and cannot execute `-dev`.

Both CI-local scripts run Task 11 Format/Vet/Unit/Integration/Browser stages,
production binary isolation, Compose validation, real restricted production
Chrome rendering and the OIDC/login/finalized-PDF browser suite in a separate
development image with Compose hardening. An additional browser workflow targets
the actual Compose listener, finalizes an invoice, downloads its PDF and confirms
Paperless delivery. It verifies persistence after restart and clean SIGTERM exit,
then runs production backup/verify/restore/integrity commands on an isolated copy
of the synthetic data. They tear down `invoice-ci` containers. No production
provider is used.

Create local amd64/arm64 release artifacts with retained SBOM and provenance:

```sh
docker buildx build --platform linux/amd64,linux/arm64 --target production --attest type=sbom,generator=docker/buildkit-syft-scanner:stable-1@sha256:ae4f3b554449e7e25548e7d8ccc029d17357348e30c6e3df01b92bc93654d6a9 --provenance=mode=max --output type=oci,dest=invoice-generator.oci.tar .
```

Never pass credentials as build arguments. Publishing is a separate release
operation. Compare repeated Go binary SHA-256 per architecture: CGO is disabled,
paths/symbols/build IDs/VCS stamping are stripped. OCI timestamps/provenance may
differ. Retain and scan the SBOM with its image digest before deployment.

The 512-PID limit is backed by the full concurrent browser/document workflow: a
256-PID limit rejected 18 process creations and broke PDF preview; 512 completed
with zero PID-limit or memory events at the same 2-CPU/2-GiB limits. Snapshot
rendering blocks HTTP/HTTPS/WebSocket/FTP asset requests through Chrome DevTools
before navigation; the existing CLI rendering policy is preserved.
