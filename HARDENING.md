# Loose Ends — Post-Trial Hardening

This records the changes made after the trial-2 build to close the five HIGH
findings from the gate code review (see the trial handoff §4). Each fix is
verified by a live smoke test and/or automated tests.

## What changed

### 1. Self-bootstrapping migrations (HIGH #1 + the single-binary MEDIUMs)
- **`migrations/0000_extensions.up.sql`** — `CREATE EXTENSION IF NOT EXISTS pgcrypto; citext`.
  Both are *trusted* extensions (PostgreSQL 13+), so the database **owner** can
  install them without superuser. `IF NOT EXISTS` makes it a no-op where they
  already exist.
- **`migrations/embed.go`** — the SQL files are now compiled into the binary
  (`//go:embed *.sql`). `storage.RunMigrations` defaults to the embedded source
  (`iofs`); pass `--path <dir>` to read from disk instead. The single binary now
  bootstraps a fresh database with no `migrations/` directory present.
- **`serve` auto-applies migrations** on startup (idempotent); opt out with
  `--skip-migrate`.

Verified: `migrate` on a brand-new database owned by a non-superuser role creates
`pgcrypto`, `citext`, and all tables.

### 2. Authentication seam (HIGH #2)
- New config key **`service.auth_token`**. When set, every route except
  `/healthz` and `/version` requires `Authorization: Bearer <token>` (constant-time
  compare); missing/incorrect → `401 UNAUTHORIZED`. When empty (default), the
  service is unauthenticated — intended for loopback-only use.
- The CLI already sends `server.token` as a bearer token, so a local authenticated
  setup is: set `service.auth_token` and `server.token` to the same value.

### 3. Loopback enforcement (HIGH #3)
- `serve` **refuses to bind a non-loopback `--bind` address** (e.g. `0.0.0.0`)
  unless an `service.auth_token` is configured or `--allow-public` is passed.
  This closes the footgun where one flag exposed the unauthenticated API to the
  network.
- Scope note: this guard governs the **loopback bind address only**. The phase
  15.8 **tailnet** listener serves the same handler but is gated by Tailscale
  ACLs by design (spec §13, "auth is you're on my tailnet"), so it is not subject
  to the loopback check; `serve` prints a warning when tailnet exposure is on
  without an `auth_token`. Set `service.auth_token` to require bearer auth on the
  tailnet listener too.

### 4. Duplicate-delete fix (HIGH #4)
- `DeleteTask` now runs in a transaction that first reopens any tasks pointing at
  the target as their duplicate (`status='open', duplicate_of_id=NULL`) before
  deleting, so the lifecycle CHECK is never violated. You can now delete a task
  that has duplicates pointing at it; the dependents return to `open`.

### 5. Tests (HIGH #5)
- A **store interface** (`server.TaskStore`, satisfied by `*storage.Repository`)
  lets the HTTP handlers be tested without a database.
- **Unit tests** (run in `go test ./...`): status validation, config set/save/load,
  `serviceListenAddr` + loopback classification, short-ID formatting, the JSON
  request decoders, the auth middleware, and the create/list/get/delete/validation
  handler paths against a fake store.
- **Integration tests** (build-tagged `integration`, run host-side against a real
  Postgres) cover the duplicate-delete regression, fractional sort-key moves,
  cycle detection, and `completed_at` lifecycle:
  ```bash
  LOOSE_ENDS_TEST_DSN='postgres://loose_ends@127.0.0.1:5432/le_clean?sslmode=disable' \
    go test -tags integration ./internal/storage/
  ```

## Verification

```text
go build ./...   # clean
go vet ./...     # clean
go test ./...    # ok (model, config, cli, server)
go test -tags integration ./internal/storage/   # ok (against a throwaway DB)
```

## Still open (lower priority — from the gate review)

- `completed_at` is cleared when a *done* task is archived/marked-duplicate (loses
  the original completion time). Localized to the `UpdateTask` CASE expression.
- MEDIUM/LOW polish: body-size limit + panic recovery + server timeouts; DSN
  defaults to `sslmode=disable`; `config show` prints the token/DSN unredacted;
  hardcoded valid-status set duplicated in several places; `q` search and
  `defaults.tags` unimplemented.
- Phase **15.8 (Tailnet + auto-start)** — see `cmd`/service wiring and the
  operator-gated bring-up.
