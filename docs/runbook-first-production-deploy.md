# Runbook — first production deployment (single VPS, Debian 12)

Concrete, ordered commands for taking market-ops from "nothing deployed" to a
running TLS stack you can sign into, on one Debian 12 VPS with Docker already
installed and a domain you control.

This is the executable companion to `DEPLOYMENT.md` §6.1 and §10. Where they
disagree, `DEPLOYMENT.md` is authoritative — it carries the reasoning and the
readiness gate; this file carries the keystrokes.

Substitute throughout:

| Placeholder | Meaning |
|---|---|
| `ops.example.com` | your domain |
| `deploy` | the non-root account on the VPS |
| `v0.1.0` | the release tag you push in Phase 1 |
| `you@example.com` | ACME registration address |

---

## What this gets you, and what it does not

**Reaches:** HTTPS on your domain with an auto-renewable certificate, the SPA
served same-origin, the Go gateway behind it, PostgreSQL 18 migrated forward,
one real owner account you can sign in as, and a rehearsed rollback.

**Does not reach:**

- **Account-scoped screens showing data.** The published Nginx image bakes
  `VITE_GATEWAY_BASE_URL=/api` but not `VITE_MARKETPLACE_ACCOUNT_ID`, so the SPA
  falls back to the dev seed UUID `00000000-0000-0000-0000-000000000003`
  (`apps/web/src/data/account.tsx`). You will sign in and the shell will load;
  screens that query by account will query an id that does not exist in your
  database. Fix options are in `DEPLOYMENT.md` §10 step 9b — the correct one is
  returning the id on `SessionInfo`.
- **WAL archiving and restore drills.** Not implemented. Until they are, this
  database has no tested recovery path. Phase 9 gives an interim `pg_dump`.
- **The production observability stack.** `compose.prod.yml` ships no collector;
  keep `OTEL_ENABLED=false`.
- **Chat.** The LLM tool runners are not wired; the plane runs on the mock
  provider and makes no paid calls.
- **Anything touching a real seller account or a marketplace write.** That is the
  separate S35 gate and needs its own explicit approval.

Treat the result as *infrastructure-verified*, not feature-complete.

---

## Phase 0 — merge and prove the bootstrap change (on your laptop)

The `bootstrap-owner` subcommand is new code. **It has not been compiled** — it
was written against the repository's exact type signatures but no Go toolchain
was available to build it. Prove it before it becomes an immutable image.

```sh
cd ~/workspace/market-ops
git switch -c feat/prod-owner-bootstrap

# 1. It must compile.
cd services/core && GOWORK=off go build ./... && cd ../..

# 2. It must pass its own DB-backed proofs. Needs a local Postgres.
task dev
export DATABASE_URL='postgres://market_ops:market_ops@localhost:5432/market_ops?sslmode=disable'
task db:reset
cd services/core && GOWORK=off go test ./cmd/core/... -run Bootstrap -v && cd ../..

# 3. The whole pre-merge gate.
task ci:local
task test:integration
```

If step 1 fails, fix the compile error before anything else — everything
downstream depends on this binary. Then commit, open a PR, let the `container
release` workflow run its eight `build and scan` jobs green (the `publish` job
will be skipped — that is correct for a PR), review, and merge to `main`.

---

## Phase 1 — publish the release (on your laptop)

Only a strict-semver tag on a commit reachable from `origin/main` publishes
images. The repository currently has **zero** tags, so this is the first one.

```sh
git switch main && git pull
git tag v0.1.0
git push origin v0.1.0
```

Watch the run. You need the **`publish immutable multi-platform images`** job
green — not just the eight build-and-scan jobs. When it finishes, four images
exist in GHCR:

```text
ghcr.io/mhosseinab/market-ops-core:v0.1.0
ghcr.io/mhosseinab/market-ops-llm:v0.1.0
ghcr.io/mhosseinab/market-ops-nginx:v0.1.0
ghcr.io/mhosseinab/market-ops-goose:v0.1.0
```

---

## Phase 2 — prepare the VPS

SSH in as root (or an existing sudoer).

```sh
apt-get update && apt-get -y upgrade
apt-get install -y git curl ufw ca-certificates unattended-upgrades
dpkg-reconfigure -plow unattended-upgrades   # enable automatic security updates
```

Create the non-root deployment account and give it Docker access:

```sh
adduser --disabled-password --gecos "" deploy
usermod -aG docker deploy
mkdir -p /home/deploy/.ssh
cp ~/.ssh/authorized_keys /home/deploy/.ssh/authorized_keys
chown -R deploy:deploy /home/deploy/.ssh && chmod 700 /home/deploy/.ssh
```

Firewall — 22 from your networks only, 80/443 public, everything else closed.
Postgres, core and the LLM plane publish no host ports at all, so they are
already unreachable; this is defence in depth.

```sh
ufw default deny incoming
ufw default allow outgoing
ufw allow from YOUR.OFFICE.IP.ADDR to any port 22 proto tcp   # or `ufw allow 22/tcp`
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
ufw status verbose
```

> Docker publishes ports by writing directly to nftables and **bypasses ufw**.
> The stack only publishes 80 and 443, which you are allowing anyway, so this is
> consistent here — but never assume a `ports:` entry is firewalled by ufw.

Harden SSH, then reconnect as `deploy` before closing the root session:

```sh
sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin no/' /etc/ssh/sshd_config
sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication no/' /etc/ssh/sshd_config
systemctl reload ssh
```

As `deploy`, confirm the toolchain:

```sh
docker version
docker compose version     # must be v2.x
docker buildx version      # required by the --resolve digest lookup
```

Install Task (the `prod:*` wrappers use it; raw `docker compose` equivalents are
listed beside every step if you would rather not):

```sh
sudo sh -c "$(curl -fsSL https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin
task --version
```

---

## Phase 3 — DNS

Point the domain at the VPS and **verify resolution before issuing a
certificate** — Let's Encrypt rate-limits failed issuance per hostname.

```text
A     ops.example.com  ->  <VPS IPv4>
AAAA  ops.example.com  ->  <VPS IPv6>   # only if the VPS actually has one
```

```sh
dig +short ops.example.com A
dig +short ops.example.com AAAA
```

Both must return your VPS addresses, and nothing else, before Phase 6.

---

## Phase 4 — get the release commit onto the VPS

The compose file, the Nginx production config and the task wrappers are read
from a checkout. Only the images come from GHCR.

```sh
cd ~
git clone https://github.com/mhosseinab/market-ops.git
cd market-ops
git fetch --tags
git checkout v0.1.0
git describe --tags        # must print v0.1.0
```

If the repository is private, use a deploy key or a PAT-authenticated HTTPS
clone. Nothing on this host needs write access.

---

## Phase 5 — environment file and secrets

The env file lives **outside** the checkout so a `git pull` can never touch it.

```sh
sudo install -d -m 0750 -o deploy -g deploy /etc/market-ops
install -m 0600 deploy/.env.prod.example /etc/market-ops/prod.env
export ENVFILE=/etc/market-ops/prod.env
echo 'export ENVFILE=/etc/market-ops/prod.env' >> ~/.bashrc
```

Generate the secrets. Run each and paste the value into `$ENVFILE`:

```sh
openssl rand -base64 24 | tr -d '\n'; echo    # POSTGRES_PASSWORD
openssl rand -base64 32                       # CONNECTOR_ENCRYPTION_KEY (must decode to exactly 32 bytes)
openssl rand -hex 32                          # LLM_GATEWAY_TOKEN
```

Edit `$ENVFILE` (`nano /etc/market-ops/prod.env`). Leave the four `*_IMAGE`
lines alone — Phase 6 owns them. Fill in:

```text
POSTGRES_USER=market_ops
POSTGRES_PASSWORD=<the base64-24 value>
POSTGRES_DB=market_ops
DATABASE_URL=postgres://market_ops:<same password, URL-encoded>@postgres:5432/market_ops?sslmode=disable

CONNECTOR_ENCRYPTION_KEY=<the base64-32 value>
DK_API_BASE_URL=https://seller.digikala.com
APP_BASE_URL=https://ops.example.com
LLM_GATEWAY_TOKEN=<the hex-32 value>

DK_DOMAIN=ops.example.com
DK_ACME_EMAIL=you@example.com

BOOTSTRAP_OWNER_EMAIL=you@example.com
BOOTSTRAP_ORG_NAME=Your Company
BOOTSTRAP_ACCOUNT_NATIVE_ID=<your DK seller id, or a stable placeholder>
BOOTSTRAP_ACCOUNT_DISPLAY_NAME=Your Company DK
```

Three things that bite:

- **`DATABASE_URL` host is `postgres`**, the compose service name — not
  `localhost`. Core and the migration runner both read this one value.
- **URL-encode the password** inside `DATABASE_URL`. A `/`, `@`, `:` or `+` in
  the password silently corrupts the URL. Regenerate until it is alphanumeric if
  you would rather not encode.
- **`DK_API_BASE_URL` must be set** for the compose parse to succeed, but no
  call is made to it until a seller authorizes — which is the separate S35 gate.
  Setting it now does not connect anything.

Re-check the permissions:

```sh
ls -l /etc/market-ops/prod.env    # -rw------- deploy deploy
```

---

## Phase 6 — pin the digests

Log in to GHCR (a classic PAT with `read:packages` is enough; needed only if the
packages are private):

```sh
echo '<YOUR_GITHUB_PAT>' | docker login ghcr.io -u mhosseinab --password-stdin
```

Resolve the published tag to immutable digests and write them into `$ENVFILE`:

```sh
task release:images -- --resolve v0.1.0 --env-file "$ENVFILE"
task release:images:check
```

Raw equivalent: `bash tools/deploy/release_images.sh --resolve v0.1.0 --env-file "$ENVFILE"`

`release:images` refuses anything that is not
`ghcr.io/mhosseinab/market-ops-<component>@sha256:<64 hex>` — a tag can be moved
after the Trivy scan that approved it. `release:images:check` proves all four
digests are actually fetchable before you start anything.

**Record the four printed digests now.** They are this release's identity and the
rollback target for the next one.

> `--resolve` is the one path in this tooling that was never exercised against a
> live registry. If it errors, fall back: download the `image-digests-v0.1.0`
> artifact from the Actions run, `scp` it up, and run
> `task release:images -- --from ~/images.env --env-file "$ENVFILE"`.

---

## Phase 7 — validate the configuration

```sh
task prod:config
```

Raw equivalent: `docker compose --env-file "$ENVFILE" -f deploy/compose.prod.yml config --quiet`

Exits non-zero and names the first missing variable rather than booting a
half-wired stack. It prints pass/fail only — the rendered config contains every
substituted secret. If you need to read it, do so privately and never paste it
into a log, an issue, or a chat.

---

## Phase 8 — issue the TLS certificate

Nginx will not start without a certificate on disk, so this comes **before** the
first `up`, with port 80 free.

```sh
task prod:certs:issue
```

Raw equivalent:

```sh
docker compose --env-file "$ENVFILE" -f deploy/compose.prod.yml \
  run --rm --service-ports certbot certonly --standalone \
  --cert-name market-ops -d "ops.example.com" \
  --email "you@example.com" --agree-tos --no-eff-email
```

`--cert-name market-ops` is load-bearing: `nginx.prod.conf` names the live
directory as a literal, and the image's own `ENTRYPOINT` bypasses the base
image's envsubst step, so a templated path would never expand.

The `letsencrypt` volume now holds the certificate and the ACME account key.
Back it up with the database; deleting it burns the issuance rate limit for the
hostname.

---

## Phase 9 — migrate, then create the owner

Read the migration plan before applying it:

```sh
task prod:migrate:status
task prod:migrate
```

Raw: `docker compose --env-file "$ENVFILE" -f deploy/compose.prod.yml run --rm migrate status|up`

Forward-only by construction — the image's entrypoint is `goose` and its command
is `up`. Re-running is a no-op that preserves data. **Never** run `task db:reset`
here: that is the integration image and it drops the database.

A migrated database has schema and no rows. Create the first owner:

```sh
task prod:bootstrap
```

It prompts for the password with echo disabled, so the credential never reaches
shell history, `$ENVFILE`, or `docker compose config` output. Use something long
— at least 12 characters is enforced, but a generated 24+ is better:

```sh
openssl rand -base64 24    # generate, store in your password manager, paste at the prompt
```

Output looks like:

```text
bootstrap-owner: organization=1f0c…  user=8ab2…  email=you@example.com  role=owner
bootstrap-owner: marketplaceAccount=93de…  native=…
```

**Record all three ids.** The `marketplaceAccount` id is what the SPA gap in
"What this does not reach" is about.

Re-running is safe: it reports the existing ids and changes nothing. To rotate
that password later, set `BOOTSTRAP_ROTATE_PASSWORD=true` in `$ENVFILE` and
re-run — the rotation also revokes every live session for that user.

Interim backup, since WAL archiving does not exist yet:

```sh
docker compose --env-file "$ENVFILE" -f deploy/compose.prod.yml \
  exec -T postgres pg_dump -U market_ops market_ops | gzip > ~/market-ops-$(date +%F).sql.gz
```

Copy it **off the VPS**. A backup that only exists on the machine it protects is
not a backup, and one you have never restored is not evidence.

---

## Phase 10 — start the stack

```sh
task prod:up
```

Raw: `docker compose --env-file "$ENVFILE" -f deploy/compose.prod.yml up -d --wait` then `ps`

Ordering is postgres → migrate → core/llm → nginx. `--wait` blocks on health
checks. `core` has no container health check — it is distroless with no shell —
but the Nginx probe calls `/api/healthz`, which proxies through to core, so a
healthy Nginx proves the whole edge→gateway path.

If `--wait` times out:

```sh
task prod:ps
task prod:logs                    # core llm nginx migrate
task prod:logs -- core            # one service
```

---

## Phase 11 — verify

From your laptop, not the VPS:

```sh
curl -fsS  https://ops.example.com/healthz          # nginx itself
curl -fsS  https://ops.example.com/api/healthz      # proxied through to core
curl -fsSI https://ops.example.com/                 # SPA, headers
curl -fsSI http://ops.example.com/                  # must 301 to https
```

Confirm no internal port is exposed:

```sh
nmap -Pn -p 5432,8080,8100 ops.example.com    # all closed/filtered
```

Then open `https://ops.example.com/` in a browser, sign in with the owner email
and the password from Phase 9, and check in devtools that the session cookie is
`Secure`, `HttpOnly`, `SameSite`. Deep-link to a nested route and reload — the
SPA history fallback must serve the app, not a 404.

Expect account-scoped screens to be empty or error. That is the known SPA gap,
not a deployment failure.

---

## Phase 12 — rehearse the rollback before you need it

An untested rollback is a hope. Do this while nothing is wrong.

```sh
cat "$ENVFILE.prev-images"    # empty on a first deploy — nothing to roll back to yet
```

On the first release there is no previous image, so rehearse the mechanism on
the next one: after `task release:images -- --resolve v0.1.1`, run
`task prod:rollback` and confirm `task prod:ps` comes back healthy on the v0.1.0
digests, then roll forward again. Record that it worked — it is a §9 gate item.

Note the snapshot is one step deep, not a history. The digests you recorded in
Phase 6 are the durable record.

---

## Phase 13 — certificate renewal

Certbot certificates last 90 days. Renew through the webroot the running Nginx
already serves on port 80:

```sh
crontab -e
```

```cron
17 3 * * 1 cd /home/deploy/market-ops && ENVFILE=/etc/market-ops/prod.env /usr/local/bin/task prod:certs:renew >> /home/deploy/certbot.log 2>&1
```

Weekly is right: renewal is a no-op until the certificate is inside its 30-day
window. Test it once by hand first:

```sh
task prod:certs:renew
```

---

## Phase 14 — record the handoff

`DEPLOYMENT.md` §12 lists what to retain. At minimum, for this release:

- release commit and the four image digests from Phase 6
- migration status output from Phase 9
- the organization / user / marketplaceAccount ids from the bootstrap
- TLS and smoke-test output from Phase 11
- the backup object you copied off the VPS, and whether you restored it
- the explicit human go/no-go for S34, with your name and the date

---

## Quick reference

```sh
export ENVFILE=/etc/market-ops/prod.env
cd ~/market-ops

task prod:ps                       # what is running, and is it healthy
task prod:logs -- core             # one service's logs
task prod:config                   # does the env file still parse
task release:images:check          # are the pinned digests still fetchable
task prod:certs:renew              # renew + reload nginx
task prod:rollback                 # previous digests, restart (images only)
```

Upgrading to a later release:

```sh
git fetch --tags && git checkout v0.1.1
task release:images -- --resolve v0.1.1 --env-file "$ENVFILE"
task release:images:check
task prod:config
task prod:migrate:status && task prod:migrate
task prod:up
```

The checkout and the images must be the same version: the compose file and
`nginx.prod.conf` come from the checkout, the services come from the digests.
