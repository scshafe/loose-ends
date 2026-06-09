# Loose Ends

A CLI-first personal task manager: a single Go binary that is both a
command-line client and a loopback (and optionally tailnet) HTTP service, backed
by Postgres. Built through Mission Control's agent runtime; see
[`LOOSE_ENDS_PLAN.md`](LOOSE_ENDS_PLAN.md) for the full spec and
[`HARDENING.md`](HARDENING.md) for the post-trial hardening.

## Build

```bash
go build -o loose-ends ./cmd/loose-ends   # Go 1.23+
```

## Quick start

```bash
# 1. A Postgres role + database named loose_ends (extensions are bootstrapped by
#    the migrations; the loose_ends role can create the trusted pgcrypto/citext
#    extensions on a database it owns).
createdb loose_ends 2>/dev/null || true

# 2. Apply migrations (embedded in the binary).
loose-ends migrate

# 3. Run the service (auto-applies migrations; loopback by default).
loose-ends serve

# 4. Use the CLI (talks to the running service).
loose-ends add "Ship the report" --tag work
loose-ends list --tree
loose-ends done <id>
```

Configuration lives at `~/Library/Application Support/loose-ends/config.toml`
(macOS) or `~/.config/loose-ends/config.toml` (Linux); override with
`LOOSE_ENDS_CONFIG`. See `loose-ends config show`.

## Tailnet exposure (Tailscale)

The service can join your Tailscale network as its own node (`tsnet`), reachable
at `loose-ends.<your-tailnet>.ts.net` and nowhere else — Tailscale handles
identity and auth at the network layer (spec §13).

**First-run setup:**

1. In the Tailscale admin console, generate an auth key (reusable, pre-approved,
   tagged e.g. `tag:loose-ends`).
2. Write it to `~/.config/loose-ends/tsnet.authkey` with mode `0600`. Paste it
   into `cat`'s stdin so the key never lands in your shell history:
   ```bash
   mkdir -p ~/.config/loose-ends
   ( umask 077; cat > ~/.config/loose-ends/tsnet.authkey )   # paste, then Ctrl-D
   ```
3. Ensure `tailnet_enabled = true` (the default) in config. Start the service:
   ```bash
   loose-ends serve
   ```
   The node registers, then `tsnet` persists state in `tailnet_state_dir`
   (`~/.config/loose-ends/tsnet-state` by default). After first registration the
   auth-key file can be deleted.

**Toggle off:** `tailnet_enabled = false` (or `serve --tailnet off`) runs
loopback-only. The hostname is configurable via `service.tailnet_hostname`.

> The auth key is a secret — keep it in the `0600` file; never pass it on the
> command line or commit it.

### Optional HTTP auth

For defense-in-depth on top of the tailnet boundary, set `service.auth_token`;
the service then requires `Authorization: Bearer <token>` on every route except
`/healthz` and `/version`.

The **loopback bind address** is guarded: `serve` refuses a non-loopback
`--bind` (e.g. `0.0.0.0`) unless `service.auth_token` is set or you pass
`--allow-public`. The **tailnet listener** is separate — its access is gated by
your Tailscale ACLs ("auth is you're on my tailnet", §13), so it is *not* subject
to that loopback check; `serve` prints a warning if tailnet exposure is on
without an `auth_token`.

## Auto-start at login

**macOS (launchd):**
```bash
loose-ends serve --install-launchd        # writes ~/Library/LaunchAgents/com.scshafe.loose-ends.plist
launchctl load ~/Library/LaunchAgents/com.scshafe.loose-ends.plist
```
A hand-editable sample is in [`deploy/com.scshafe.loose-ends.plist`](deploy/com.scshafe.loose-ends.plist).

**Linux (systemd user unit):** see [`deploy/loose-ends.service`](deploy/loose-ends.service).

## Docker (tailnet)

Run the whole thing — app + Postgres — as a self-contained `docker compose`
stack that joins your tailnet directly via `tsnet`. Because `tsnet` is userspace,
the container needs **no `/dev/net/tun`, no `CAP_NET_ADMIN`, no `--privileged`**,
and there is no `tailscaled` sidecar. Reached at `loose-ends.<tailnet>.ts.net:17890`.

```bash
# put your Tailscale auth key where both native + Docker read it (paste into
# cat's stdin so it stays out of shell history; Ctrl-D when done):
mkdir -p ~/.config/loose-ends
( umask 077; cat > ~/.config/loose-ends/tsnet.authkey )

cd deploy/docker && docker compose up -d --build
```

Full guide — including **where/how to store the auth key**, what kind of key to
mint, importing existing data, and hardening — in
[`deploy/docker/README.md`](deploy/docker/README.md).

## Tests

```bash
go test ./...                                   # unit tests
LOOSE_ENDS_TEST_DSN='postgres://loose_ends@127.0.0.1:5432/le_test?sslmode=disable' \
  go test -tags integration ./internal/storage/ # integration tests (throwaway DB)
```
