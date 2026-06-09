# Deploy loose-ends to your tailnet with Docker

This brings up loose-ends as a self-contained `docker compose` stack:

- **`loose-ends`** — the app. It joins your Tailscale network *in userspace*
  (`tsnet`) and serves the HTTP API at `loose-ends.<your-tailnet>.ts.net:17890`.
- **`db`** — its Postgres, on a private compose network (no host port).

Because `tsnet` runs the userspace network stack, the container needs **no
`/dev/net/tun`, no `CAP_NET_ADMIN`, no `--privileged`** — just outbound HTTPS
(443) to reach the Tailscale control plane and DERP relays. There is **no
tailscaled sidecar**; the binary is its own tailnet node.

No host port is published: the API is unauthenticated by default, so it is
reachable *only* over your tailnet (your Mac, being on the tailnet, reaches it
by MagicDNS like any other device).

---

## Where to store the key

**Short answer:** put your Tailscale auth key on the host at

```
~/.config/loose-ends/tsnet.authkey      # mode 0600
```

and `docker compose` bind-mounts that file **read-only** into the container at
the exact path the binary reads. The key is therefore:

- the **same** location the native (non-Docker) install uses — one key, both modes;
- **never** on the command line, **never** in the image, **never** in `docker inspect`;
- **read-only** in the container, and **consumed only once** (see below).

Create it by pasting the key into `cat`'s stdin — the key is typed into the
running program *after* the command line is already recorded, so (unlike a
`<<<'key'` here-string, which the shell saves to history verbatim) it never lands
in `~/.zsh_history`:

```bash
mkdir -p ~/.config/loose-ends
( umask 077; cat > ~/.config/loose-ends/tsnet.authkey )   # paste the key, Enter, then Ctrl-D
chmod 600 ~/.config/loose-ends/tsnet.authkey              # (umask 077 already set this)
```

> **Do this before `docker compose up`.** If the file does not exist, Docker
> creates an empty *directory* at that path and the service fails to start with
> a "read tailnet auth key … is a directory" error. If you hit that, remove the
> stray directory and create the file.

### The key is one-shot — the volume is what persists

`tsnet` uses the auth key **only on first registration**. Once the node
registers, its identity lives in the `tsnet-state` Docker volume
(`tailscaled.state`), and every later restart reconnects with **no key**. So:

- After the first successful `up`, the key is no longer used. If you want the
  secret off disk, **empty the file** (`: > ~/.config/loose-ends/tsnet.authkey`) —
  do **not** delete it. Compose bind-mounts that exact path, so a *missing* file
  makes Docker recreate it as a directory on the next container recreate, which
  crashes `serve`. An empty file reads as "no key" and the service starts from
  the persisted `tsnet-state`. (To drop the key path entirely, also remove the
  `tsnet.authkey` volume line from `docker-compose.yml`.)
- Do **not** delete the `tsnet-state` volume — losing it forces a re-registration
  (and re-consumes a key).

### What kind of key to mint

In the Tailscale admin console (**Settings → Keys → Generate auth key**), for a
long-lived server node, prefer:

| Setting | Choose | Why |
|---|---|---|
| **Reusable** | ✅ yes (recommended) | A one-off key works too — it's used exactly once with a healthy volume — but reusable means the same key still works if you ever recreate the `tsnet-state` volume or redeploy fresh. |
| **Ephemeral** | ❌ no | Ephemeral nodes are auto-removed after short inactivity and lose their IP — wrong for a persistent service. |
| **Pre-approved** | ✅ yes | Lets the container join unattended even if device approval is on. |
| **Tags** | `tag:loose-ends` | Gives the node a stable service identity for ACLs, and **disables node-key expiry** by default (no surprise re-login at 90/180 days). |

If you tag the key, the tag must already exist in your tailnet policy
(`tagOwners`) before the key is minted, e.g.:

```jsonc
// tailnet policy file (admin console → Access controls)
"tagOwners": {
  "tag:loose-ends": ["autogroup:admin"]
}
```

(You also need ACL grants that let your other devices reach `tag:loose-ends` on
the service port — by default a tagged node is reachable by nothing.) A plain
untagged personal key also works for getting started; you just don't get the
no-expiry / ACL benefits.

> Already have a key? Whatever type it is, it will register the node on first
> boot. The table above is what to mint *next time* if yours is, say, a one-off
> or ephemeral key.

---

## Run it

```bash
cd deploy/docker

# 1. (once) put your key at ~/.config/loose-ends/tsnet.authkey — see above.

# 2. build the image + start the stack
docker compose up -d --build

# 3. watch it register on the tailnet
docker compose logs -f loose-ends
#   warning: tailnet exposure is on with no service.auth_token — every device on
#     your tailnet can reach the API unauthenticated (gated only by Tailscale
#     ACLs). Set service.auth_token for defense-in-depth.
#   tailnet node "loose-ends" serving on port 17890
#   serving on http://127.0.0.1:17890
```

The `warning:` line is **expected** with the shipped config (no `auth_token` — see
*Optional hardening*); migrations run silently before it. The node then appears
in your admin console's **Machines** list as `loose-ends`.

### Verify

From any device on your tailnet:

```bash
curl http://loose-ends.<your-tailnet>.ts.net:17890/healthz     # {"ok":true}
curl http://loose-ends.<your-tailnet>.ts.net:17890/version
```

(Substitute your tailnet's name, e.g. `loose-ends.tail1234.ts.net`. Find it in
the admin console or via `tailscale status` on any device.)

Point the CLI at the deployed service from any tailnet device:

```bash
loose-ends config set server.url http://loose-ends.<your-tailnet>.ts.net:17890
loose-ends list --tree
```

---

## Operate

```bash
docker compose logs -f loose-ends      # follow logs
docker compose restart loose-ends      # restart the app
docker compose down                    # stop (keeps volumes/data + node identity)
docker compose up -d --build           # rebuild + redeploy after a code change
docker compose exec loose-ends bash    # shell in for debugging
docker compose exec db psql -U loose_ends loose_ends   # psql into the database
```

- **Auto-start:** `restart: unless-stopped` restarts the app on crash and on
  host reboot (as long as Docker itself is set to start at boot). This is the
  Docker equivalent of the launchd/systemd units in `deploy/`.
- **State lives in two named volumes:** `loose-ends_tsnet-state` (node identity)
  and `loose-ends_db-data` (your tasks). `docker compose down` keeps them;
  `docker compose down -v` **deletes** them (fresh node + empty DB next time).

---

## Data: this stack starts with an empty database

The bundled Postgres is a fresh database — `serve` runs the embedded migrations
on first boot, but there are **no tasks** in it. Two ways to get real data:

**A. Import an existing dump** (e.g. from the host Postgres the trial used):

```bash
# on the host, dump the existing loose_ends DB (data only, into the new schema)
pg_dump --data-only --no-owner postgres://loose_ends@127.0.0.1:5432/loose_ends \
  | docker compose exec -T db psql -U loose_ends loose_ends
```

**B. Point the app at your existing Postgres instead of the bundled one.**
Remove the `db` service + `depends_on`, delete the `POSTGRES_*`/`db-data` bits,
and set the DSN in `config.toml` to your host database. From a Linux container,
the host is `host.docker.internal` with:

```yaml
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

and `dsn = "postgres://loose_ends@host.docker.internal:5432/loose_ends?sslmode=disable"`
(your host Postgres must accept connections from the Docker bridge in
`pg_hba.conf` / `listen_addresses`).

---

## Optional hardening

- **HTTP bearer auth (defense-in-depth):** the tailnet boundary already gates
  access, but if your tailnet has other people/devices, set `service.auth_token`
  in `config.toml`. Every route except `/healthz` and `/version` then requires
  `Authorization: Bearer <token>`; set `server.token` to the same value on each
  CLI client.
- **Postgres password:** set `POSTGRES_PASSWORD` on the `db` service and add it
  to the DSN in `config.toml`. (The db port isn't published, so the default
  `trust` is acceptable on the private network.)

---

## Alternative: auth key via env instead of the file mount

The file mount is the default and the most secure (the key never enters the
container's environment). On **native Linux**, a `0600` key file owned by your
host user may not be readable by the container's uid (10001); on macOS Docker
Desktop / Colima the file-sharing layer handles this and the mount just works.

If the file mount is blocked, deliver the key as an env var instead:

```bash
cd deploy/docker
cp .env.example .env          # .env is gitignored
$EDITOR .env                  # set TS_AUTHKEY=tskey-auth-…
```

then in `docker-compose.yml` comment out the `tsnet.authkey` volume and
uncomment `env_file: .env`. `tsnet` reads `$TS_AUTHKEY` when no key file is
present. Trade-off: an env var is visible via `docker inspect` and in the
container's `/proc/<pid>/environ`. The state-volume behavior is identical — the
key is still consumed only on first registration.

---

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `read tailnet auth key … is a directory` | The key file didn't exist at `up` time, so Docker made a directory. `rm -rf` it, create the `0600` file, `docker compose up -d` again. |
| `permission denied` reading the key (Linux) | Container uid can't read your `0600` file. Use the env-var method above, or add `user: "${UID}:${GID}"` and export `UID`/`GID`. |
| Node never appears / `bring up tailnet node` errors | The container needs outbound 443. Check egress and DNS (it must resolve `controlplane.tailscale.com`). Key already used + no key/expired → mint a fresh reusable key. |
| App restarts waiting on DB | Normal on first boot while Postgres initializes; the healthcheck `start_period` covers it. Persisting → check `docker compose logs db`. |
| Duplicate `loose-ends` nodes in the console | The `tsnet-state` volume was lost between runs, forcing re-registration. Keep the volume; delete stale nodes in the admin console. |
