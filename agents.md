# Agent Charter: hypr-smartd

## Mission
Build **hypr-smartd**, a Hyprland companion daemon in Go. It listens to Hyprland IPC events, maintains a live world model of monitors/workspaces/clients, and applies **smart layouts** via declarative rules and modes—without modifying the compositor.

## Target v0.1 scope (must ship)
- Event subscription (Hyprland event socket) and initial world reconcile (`hyprctl -j clients/workspaces/monitors`).
- YAML config with modes → rules → actions.
- Two actions implemented end-to-end:
  1) `layout.sidecarDock` (dock Slack/Discord at 25% left/right on a given workspace).
  2) `layout.fullscreen` (force fullscreen for active client).
- Guardrails: dry-run, simple debounce, idempotent apply.
- CLI flags: `--config`, `--dry-run`, `--log-level`.
- User systemd unit + Makefile targets.

## Environment assumptions
- Host: Arch/Wayland (Hyprland already running).
- `hyprctl` in PATH. `HYPRLAND_INSTANCE_SIGNATURE` present.
- Go 1.22+.

## Tech choices
- Language: Go.
- Config: YAML (gopkg.in/yaml.v3).
- Logging: standard library (fmt/log) with simple levels.
- No external deps unless clearly justified.

## Repository layout (create this)
hypr-smartd/
├── cmd/
│   └── hypr-smartd/
│       └── main.go
├── internal/
│   ├── config/      # YAML models + loader
│   │   └── config.go
│   ├── engine/      # event loop, reconcile, guards
│   │   └── engine.go
│   ├── ipc/         # hyprctl JSON queries + event socket
│   │   ├── events.go
│   │   └── hyprctl.go
│   ├── layout/      # rect math + dispatch batching
│   │   ├── geom.go
│   │   └── ops.go
│   ├── rules/       # predicates + action builders
│   │   ├── actions.go
│   │   ├── match.go
│   │   └── rules.go
│   ├── state/       # world model types
│   │   └── world.go
│   └── util/
│       └── log.go
├── configs/
│   └── example.yaml
├── system/
│   └── hypr-smartd.service
├── .github/
│   └── workflows/
│       └── ci.yml
├── .gitignore
├── Makefile
├── go.mod
├── README.md
└── LICENSE

## Initial files to generate (content-level requirements)
- **README.md**: quick start, systemd usage, config example, roadmap v0.1→v0.2.
- **Makefile**: `build`, `run`, `install`, `service`, `lint`, `test`.
- **configs/example.yaml**: two modes (`Coding`, `Gaming`), includes a `layout.sidecarDock` and `layout.fullscreen` example.
- **system/hypr-smartd.service**: user service (Restart=on-failure).
- **.github/workflows/ci.yml**: Go build + `go vet` + `go test`.
- **.gitignore**: `bin/`, `.DS_Store`, `*.log`.

## Coding standards
- Go 1.22, `go fmt`/`go vet` clean. Add minimal unit tests for predicate matching and sidecar rect math.
- No panics in long-running code paths; return errors and log.
- Keep IPC package self-contained and replace hyprctl shell-outs with direct sockets in a later PR.

## Event flow
1. Subscribe to Hyprland event socket (`.socket2.sock`) → parse `kind>>payload`.
2. On interesting events (`openwindow`, `closewindow`, `activewindow`, `workspace`, `movewindow`, monitor add/remove), **reconcile** world state using `hyprctl -j`.
3. Evaluate rules for current mode; build a **plan** (list of hyprctl dispatches).
4. Apply with debounce + idempotence checks; support `--dry-run`.

## Predicates (v0.1)
- `any` / `all` / `not`, plus:
  - `mode` (string equals)
  - `app.class` (string equals, case-insensitive)
  - `app.titleRegex` (RE2)
  - `apps.present` (list of classes present anywhere)
  - `workspace.id` (active WS id)
  - `monitor.name` (string equals)

## Actions (v0.1)
- `layout.sidecarDock { workspace, side: left|right, widthPercent: 10..50, match: {class|anyClass|titleRegex} }`
- `layout.fullscreen { target: "active" | "match" }`
- Stubs (no-op for now, wire later): `layout.ensureWorkspace`, `client.pinToWorkspace`.

## Debounce / safety
- Per-rule debounce window: default 500ms (configurable later).
- Idempotence: if target rect ~equals current, skip.
- Batch `hyprctl dispatch` calls when possible; on error, abort plan.

## Acceptance criteria (for v0.1 PR)
- `make build` produces `bin/hypr-smartd`.
- `make run` with Hyprland running subscribes to events and logs them.
- Placing Slack/Discord and any host app on a target workspace triggers **sidecarDock** and visibly tiles 25/75 on that monitor.
- `--dry-run` shows planned dispatches without changing windows.
- `systemd --user` unit enables + starts successfully on login session.

## Project management rules for the Agent
- Always open a branch (`feat/…`), commit in small steps, open a PR with:
  - Checklists for acceptance criteria,
  - “How to test” section,
  - Screenshots or `hyprctl -j` snippets when relevant.
- Keep PRs under ~500 lines of diff when possible.
- If a detail is missing, pick a sane default; **do not block**—leave TODOs and proceed.
- Prefer simple code that works over speculative abstractions. Ship v0.1, then iterate.

## License
- MIT by default unless overridden.
