# Loose Ends — Plan

A personal, CLI-first task manager with a service backend. Second trial of Mission Control as a build vehicle.

> **Status:** Direction locked in after first review. See §4 for the settled decisions. One Tailscale-shaped sub-question remains in §16.

---

## 1. Overview

`loose-ends` is a single Go binary that operates in two modes:

- **Client mode** (`loose-ends <command>`): reads `~/.config/loose-ends/config.toml`, makes HTTP calls to the service.
- **Service mode** (`loose-ends serve`): long-running HTTP server backed by Postgres, exposed on the local loopback and over the user's Tailscale network.

## 2. Goals

- **CLI-first.** The primary interface is `loose-ends` on the command line. Everything possible from the CLI.
- **Service-backed.** A long-running HTTP service holds the data. The CLI is a thin client.
- **Local + tailnet.** Service is reachable locally (loopback) and from any device on the user's tailnet.
- **Single-user.** No multi-tenant concerns. Tailscale provides the auth boundary.
- **Hierarchical tasks.** Tasks can nest arbitrarily deep. "Sub-tasks" are just tasks with a parent.
- **Sibling ordering.** Tasks can be reordered within their parent.
- **Tags.** Free-form, flat (non-hierarchical) tags. Tasks can have many.
- **Soft lifecycle.** Completing a task changes its status; it is never deleted by default. Statuses: `open`, `done`, `archived`, `duplicate`, `partially_complete`.

## 3. Non-Goals

- No multi-user / no per-user data partitioning.
- No mobile/web clients (CLI only; web can come later if it earns its place).
- No reminders, notifications, or due-date scheduling in v1.
- No real-time push from server to client.
- No third-party integrations (Slack, GitHub, etc.).

## 4. Decisions (locked in)

| Concern             | Decision                                                                 |
|---------------------|--------------------------------------------------------------------------|
| App name            | **Loose Ends**                                                           |
| Binary name         | `loose-ends`                                                             |
| Language            | **Go**                                                                   |
| Storage             | **Postgres** (NOT SQLite)                                                |
| DB name             | `loose_ends` (namespaced; collision-safe)                                |
| DB role             | `loose_ends` (dedicated role; no shared `postgres` user)                 |
| Repo location       | `~/.mission-control/projects/loose-ends-trial-2/`                                |
| Service port        | `17890`                                                                  |
| Network exposure    | Loopback + Tailscale (via `tsnet`, see §13)                              |
| Config path         | `~/.config/loose-ends/config.toml` (override: `$LOOSE_ENDS_CONFIG`)      |
| Cascade on status   | **No cascade.** Closing a parent does not change children's status.      |
| First-run UX        | Service auto-starts via launchd (done in final phase, see §15.8)         |
| Build tool / vehicle| Mission Control agents (after this plan is approved)                     |

## 5. Architecture

```
   ┌─────────────────────┐
   │  loose-ends (CLI)   │ ── HTTP/JSON ──┐
   │  on machine A       │                │
   └─────────────────────┘                │
                                          ▼
                               ┌─────────────────────┐    SQL    ┌──────────────────┐
                               │  loose-ends serve   │ ────────▶ │ Postgres         │
                               │  (Go service)       │           │ db: loose_ends   │
                               └─────────────────────┘           └──────────────────┘
                                          ▲
                                          │
                                          │ HTTP/JSON over tailnet
                                          │
   ┌─────────────────────┐                │
   │  loose-ends (CLI)   │ ───────────────┘
   │  on machine B       │
   └─────────────────────┘
```

- Service is one process; CLI is many short-lived invocations.
- Service exposes two listeners:
  1. `127.0.0.1:17890` (loopback, no auth needed — local trust).
  2. A `tsnet` node on the tailnet (Tailscale handles identity/auth at the network layer).
- CLI config picks one URL: loopback for the host machine, tailnet DNS name for everywhere else.

## 6. Data Model

### Table: `tasks`

| Column            | Type            | Notes                                                                 |
|-------------------|-----------------|-----------------------------------------------------------------------|
| `id`              | UUID PK         |                                                                       |
| `parent_id`       | UUID FK NULL    | Self-reference; NULL = top-level                                      |
| `title`           | TEXT NOT NULL   |                                                                       |
| `body`            | TEXT NULL       | Optional longer description (markdown)                                |
| `status`          | TEXT NOT NULL   | Enum: open / done / archived / duplicate / partially_complete         |
| `sort_key`        | DOUBLE PRECISION NOT NULL | Ordering within siblings (see §8)                           |
| `duplicate_of_id` | UUID FK NULL    | When `status='duplicate'`, points at canonical task                   |
| `created_at`      | TIMESTAMPTZ     |                                                                       |
| `updated_at`      | TIMESTAMPTZ     |                                                                       |
| `completed_at`    | TIMESTAMPTZ NULL| Set when status transitions to `done` or `partially_complete`         |

### Table: `tags`

| Column       | Type            | Notes                |
|--------------|-----------------|----------------------|
| `id`         | UUID PK         |                      |
| `name`       | CITEXT UNIQUE   | Case-insensitive     |
| `created_at` | TIMESTAMPTZ     |                      |

### Table: `task_tags` (join)

| Column     | Type      | Notes |
|------------|-----------|-------|
| `task_id`  | UUID FK   |       |
| `tag_id`   | UUID FK   |       |
| PK: (task_id, tag_id) |

### Indices

- `tasks (parent_id, sort_key)` — primary lookup pattern
- `tasks (status)` — filter views
- `tasks (duplicate_of_id)` — back-pointers from canonical tasks
- `task_tags (tag_id)` — tag-filter queries

### Constraints / invariants

- A task may not be its own ancestor (enforced in service layer on moves).
- `duplicate_of_id` may only be non-NULL when `status = 'duplicate'`.
- `completed_at` is set on transition into `done` / `partially_complete`, cleared on transition back to `open`.
- Postgres extensions required: `pgcrypto` (for `gen_random_uuid()`), `citext` (for case-insensitive tag names).

## 7. Status Semantics

| Status               | Meaning                                              | Counts as "closed"? |
|----------------------|------------------------------------------------------|---------------------|
| `open`               | Actionable; default state                            | No                  |
| `done`               | Completed successfully                               | Yes                 |
| `partially_complete` | Some work done; remainder no longer pursued          | Yes                 |
| `archived`           | No longer relevant; not actionable, not "done"       | Yes                 |
| `duplicate`          | Same as another task (see `duplicate_of_id`)         | Yes                 |

**No cascade.** Status changes on a parent do not propagate to children. Children retain their state. Rationale: closing a parent shouldn't silently lie about whether children were addressed.

**Reopen** is supported: any closed status → `open` clears `completed_at` and `duplicate_of_id`.

## 8. Ordering

Use a **float `sort_key`** per sibling group.

- Insert at end: `max(sibling sort_key) + 1.0`
- Insert between two siblings A and B: `(A.sort_key + B.sort_key) / 2.0`
- Insert at front: `min - 1.0`
- Reparent: recompute relative to new siblings

This minimizes writes for reorders (only the moved task is updated). Long-term precision risk is mitigated by a `loose-ends rebalance [--parent ID]` admin command that re-spaces siblings by integer increments.

## 9. HTTP API

JSON in/out. Listeners: loopback + tailnet.

```
POST   /tasks                          Create task
GET    /tasks                          List. Query: parent_id, status, tag, q (search), include_closed
GET    /tasks/tree                     Full tree (or subtree if ?root=ID)
GET    /tasks/:id                      Get one
PATCH  /tasks/:id                      Update title/body/status
DELETE /tasks/:id                      Hard delete (rare; CLI confirms)
POST   /tasks/:id/move                 Body: { parent_id, before_id|after_id|position: first|last }

POST   /tasks/:id/tags                 Body: { name } — attach (creates tag if new)
DELETE /tasks/:id/tags/:tagName        Detach

GET    /tags                           List tags with counts
DELETE /tags/:id                       Delete tag (cascades through join table)

POST   /admin/rebalance                Body: { parent_id?: UUID|null }

GET    /healthz                        Liveness
GET    /version                        Build info
```

**Error format:**
```json
{ "error": { "code": "TASK_NOT_FOUND", "message": "..." } }
```

**Auth:** none at the HTTP layer in v1. Loopback is implicitly trusted; tailnet is gated by Tailscale ACLs. A bearer-token field is reserved in config (§11) for future tightening.

## 10. CLI

### Core verbs

```
loose-ends add "Buy milk" [--parent ID] [--tag work] [--tag urgent] [--body "..."]
loose-ends list [--parent ID|--root] [--status open|done|...] [--tag NAME] [--tree] [--all] [--json]
loose-ends show ID
loose-ends edit ID [--title T] [--body B]
loose-ends done ID
loose-ends partial ID
loose-ends archive ID
loose-ends duplicate ID --of OTHER_ID
loose-ends reopen ID
loose-ends rm ID [--yes]
loose-ends move ID [--parent ID|--root] [--before ID|--after ID|--first|--last]
loose-ends tag ID TAG [TAG ...]
loose-ends untag ID TAG [TAG ...]
loose-ends tags                              # list all tags with counts
loose-ends rebalance [--parent ID|--root]
```

### Service / admin

```
loose-ends serve [--port N] [--bind 127.0.0.1] [--tailnet on|off] [--db DSN]
loose-ends migrate                            # apply pending migrations
loose-ends config show
loose-ends config set KEY VALUE
loose-ends config path                        # prints config file location
```

### UX details

- **Short IDs:** display first 8 chars of UUID; accept any unique prefix as input.
- **Tree view:** indented with glyphs:
  - `☐ open` / `☑ done` / `▣ partial` / `⊗ archived` / `≈ duplicate`
- **Color:** auto-detect TTY; `--no-color` flag.
- **`--json`** on read commands for scripting.
- **List default:** hides closed tasks unless `--all` or `--status` specified.
- **Stdin body:** `loose-ends add "Title" --body -` reads body from stdin (markdown OK).

## 11. Configuration

Path: `~/.config/loose-ends/config.toml` (override with `$LOOSE_ENDS_CONFIG`).

```toml
[server]
url = "http://127.0.0.1:17890"
# url = "http://loose-ends.<tailnet>.ts.net:17890"  # for remote machines
# token = "..."  # reserved; not used in v1

[display]
color = "auto"   # auto | always | never
tree_indent = "  "
id_length = 8

[defaults]
# tags = ["personal"]

# Service-only block; CLI ignores it.
[service]
listen_loopback = "127.0.0.1:17890"
tailnet_enabled = true
tailnet_hostname = "loose-ends"            # becomes loose-ends.<tailnet>.ts.net
tailnet_state_dir = "~/.config/loose-ends/tsnet-state"

[service.database]
dsn = "postgres://loose_ends@127.0.0.1:5432/loose_ends?sslmode=disable"
```

The CLI loads only `[server]` and `[display]` / `[defaults]`. The service loads `[service]` and `[service.database]`. They share the same file by design — keeps everything in one place for a personal app.

If the file is missing on first run, `loose-ends` creates it with defaults and prints the path.

## 12. Service Mode

- Two listeners running concurrently:
  1. **Loopback HTTP** on `127.0.0.1:17890`. No auth. For the CLI on the same machine.
  2. **Tailnet HTTP** via `tsnet` (see §13). Auth is "you're on my tailnet."
- Runs DB migrations on startup (idempotent).
- Logs structured JSON to stdout/stderr; supervisor (launchd / systemd / terminal) captures logs.
- Graceful shutdown on SIGTERM/SIGINT.
- Single-process. No worker pool. Personal scale.

## 13. Tailscale Exposure

We use `tailscale.com/tsnet`. The service joins the tailnet as its own node — no need to bind to a Tailscale-assigned interface IP or trust the local firewall.

**Why `tsnet`:**
- The service gets its own MagicDNS name (e.g., `loose-ends.<tailnet>.ts.net`).
- Auth is handled by Tailscale itself; nothing on the public internet.
- Connections from non-tailnet hosts are impossible at the network layer.
- No interface-binding gymnastics; works the same on macOS and Linux.

**Setup flow (one-time):**
1. Generate a Tailscale auth key in the admin console (reusable, pre-approved, tagged `tag:loose-ends`).
2. Store at `~/.config/loose-ends/tsnet.authkey` (mode 0600). Service reads it on first startup.
3. After first registration, `tsnet` persists state in `tailnet_state_dir`. Auth key file can be deleted.

**Hostname:** configurable via `[service].tailnet_hostname`. Default `loose-ends`.

**Toggling off:** `tailnet_enabled = false` skips the tsnet listener (loopback-only).

## 14. Postgres Setup

To avoid collisions with any other apps using the user's local Postgres:

```sql
CREATE ROLE loose_ends LOGIN;
CREATE DATABASE loose_ends OWNER loose_ends;
\c loose_ends
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
```

- Role: `loose_ends` (no password for local socket auth, or set one if `pg_hba.conf` requires `md5`/`scram-sha-256`).
- Database: `loose_ends`.
- All tables in the default `public` schema of that DB. No prefix needed — the DB itself is the namespace.

Migrations live in `migrations/`, numbered `0001_init.up.sql` / `0001_init.down.sql` etc. Run via `golang-migrate` (embedded; `loose-ends migrate` or auto on `serve`).

## 15. Implementation Phases

Each phase is shippable on its own. The CLI is usable after Phase 3.

### 15.1 Scaffolding
- Go module init at `~/.mission-control/projects/loose-ends-trial-2/`
- Layout: `cmd/loose-ends/`, `internal/{cli,server,storage,config,model}/`, `migrations/`
- Dependencies: cobra, chi (or echo), pgx, golang-migrate, BurntSushi/toml, google/uuid, tsnet
- Config loader (reads/writes TOML, creates default on first run)
- CLI skeleton with `--help`
- `loose-ends config show|set|path`

### 15.2 Storage + Service Core
- Migration files for `tasks`, `tags`, `task_tags`
- Repository layer (storage package) with pgx pool
- HTTP server with `/healthz`, `/version`
- `loose-ends serve` (loopback only at this point; no tsnet yet)
- `loose-ends migrate`

### 15.3 Core CRUD
- `POST /tasks`, `GET /tasks`, `GET /tasks/:id`, `PATCH`, `DELETE`
- CLI: `add`, `list` (flat, no tree yet), `show`, `edit`, `rm`
- Short-ID prefix resolution (server-side)

### 15.4 Lifecycle
- Status transitions in API
- CLI: `done`, `partial`, `archive`, `duplicate`, `reopen`
- `--all` / `--status` filtering on `list`
- `duplicate_of_id` plumbing

### 15.5 Hierarchy + Ordering
- `parent_id` through API
- `POST /tasks/:id/move`
- Float `sort_key` insert/move logic, cycle prevention
- CLI: `--parent`, `move`, `--tree` view, `rebalance`

### 15.6 Tags
- Tag CRUD endpoints
- Join table operations
- CLI: `tag`, `untag`, `tags`
- `--tag` filter on `list`

### 15.7 Polish
- Color + glyphs in tree view
- `--json` output on all read commands
- Stdin body via `--body -`
- Helpful error messages (unknown ID, ambiguous prefix, etc.)

### 15.8 Tailnet + Auto-Start
- Add `tsnet` listener alongside loopback
- Document Tailscale auth-key flow in README
- Sample launchd plist (`com.scshafe.loose-ends.plist`) → `~/Library/LaunchAgents/`
- Sample systemd user unit for Linux
- `loose-ends serve --install-launchd` convenience command (optional)
- README: install, run as service, common commands

## 16. Remaining Questions

Mostly answered, but two small ones to confirm before we hand to Mission Control:

1. **Tailnet hostname** — default `loose-ends`. OK, or do you want something else (e.g., `tasks`, `ends`)?
2. **Single config file vs split** — §11 puts CLI and service config in the same `config.toml`. Alternative: separate `cli.toml` and `service.toml`. The single-file approach is simpler for a personal app; flagging in case you have a preference.

## 17. Future Work (out of scope for v1)

- Due dates and reminders
- Full-text search on title/body
- Web UI
- Bearer-token auth (for if/when tailnet ACLs aren't sufficient)
- Export/import (JSON, Markdown)
- Editor integration (open task body in `$EDITOR`)
- Recurring tasks
- Multi-machine service (one canonical Postgres, multiple service nodes)
