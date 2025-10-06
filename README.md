# hyprpal

`hyprpal` is a small companion daemon for [Hyprland](https://hyprland.org/) that listens to compositor events, maintains a live world snapshot of monitors/workspaces/clients, and applies declarative layout rules. v0.1 ships with sidecar docking and fullscreen enforcement actions so you can keep communication apps docked while focusing on your main workspace or go distraction-free in Gaming mode.

## Quick Start

1. Ensure Hyprland is running and `hyprctl` is available on your `$PATH`.
2. Build the daemon:
   ```bash
   make build
   ```
3. Copy one of the presets from [`examples/`](./examples/) to `~/.config/hyprpal/config.yaml` (for example, `examples/dual-monitor/config.yaml`) and edit it for your workspace/app names.
4. Run the daemon from your session:
   ```bash
   make run
   ```
   Use `--dry-run` to preview dispatches without affecting windows. Pass `--dispatch=hyprctl` to force shelling out to `hyprctl` when the socket strategy is undesirable or unavailable.
5. Need a quick snapshot without touching Hyprland? Use the smoke CLI to load the config, capture the current world, and preview the plan without dispatching anything:
   ```bash
   make smoke
   ```
   Override the mode or config path via `--mode`/`--config` when auditioning alternative setups.
5. Follow logs while iterating (see [Troubleshooting & Logging](#troubleshooting--logging) for trace mode tips):
    ```bash
    journalctl --user -fu hyprpal
    ```

## Comprehensive How-To Guide

Need a production-ready walkthrough that covers installation, configuration, service management, and troubleshooting? Read the [Hyprpal How-To Guide](./docs/how-to-use.md) for step-by-step procedures, validation checklists, and operational tips.

## `hsctl` control CLI

`hsctl` is the companion CLI for interacting with a running `hyprpal` daemon. It talks to the daemon over the Unix control socket (default: `${XDG_RUNTIME_DIR}/hyprpal/control.sock`, override with `HYPRPAL_CONTROL_SOCKET` or `--socket`). `make build` compiles `hsctl` to `bin/hsctl`, while `make install` places it alongside `hyprpal` in `~/.local/bin` by default.

### Inspect and change modes

Retrieve the active mode and the available options:

```bash
hsctl mode get
# Active mode: Coding
# Available modes: Coding, Gaming
```

Switch modes instantly; the daemon updates its world model and reapplies rules for the new mode:

```bash
hsctl mode set Gaming
# Switched to mode Gaming
```

### Reload configuration

Trigger a configuration reload (equivalent to sending `SIGHUP`). The daemon re-reads the YAML config and reconciles rules without restarting:

```bash
hsctl reload
# Reload requested
```

### Preview plans with explanations

Ask the daemon to compute the pending Hyprland dispatches. The optional `--explain` flag includes the rule reason associated with each command:

```bash
hsctl plan --explain
# dispatch: dispatch focusworkspace 3
#   reason: Coding › Dock comms on workspace 3
# dispatch: dispatch movewindowpixel exact 0 0
#   reason: Sidecar dock Slack on workspace 3
```

When no actions are queued, `hsctl` prints `No pending actions`.

## `smoke` world snapshot CLI

`cmd/smoke` is a standalone helper that bootstraps the rule engine with a no-op dispatcher. It loads your configuration, captures a one-off world snapshot with the regular Hyprland queries, prints both structures, and evaluates the active mode to show the pending dispatches alongside the rule that generated them. Use it when iterating on YAML changes outside of Hyprland or when you want to verify predicate logic without letting the daemon mutate windows.

Typical usage while editing a config:

```bash
go run ./cmd/smoke --config ~/.config/hyprpal/config.yaml --mode Coding --explain
```

The `--explain` flag (enabled by default) annotates each dispatch with its `mode:rule` source, mirroring `hsctl plan --explain`. Predicate traces are also printed so you can understand why a rule matched or skipped.

## `bench` engine replay harness

`cmd/bench` replays a captured or synthetic Hyprland event stream through the engine so you can measure incremental-apply latency, dispatch volume, and allocation behaviour without relying on a live compositor. It uses the regular configuration loader plus a fake Hyprland data source derived from the engine unit tests.

Run the harness against the default synthetic fixture (a Coding workspace with dockable comms windows) or point it at your own capture:

```bash
# Replay the default synthetic stream 25 times with three warm-up passes
go run ./cmd/bench --iterations 25 --warmup 3 --config configs/example.yaml

# Replay a custom JSON fixture with CPU/heap profiles
go run ./cmd/bench --config ~/.config/hyprpal/config.yaml \
  --fixture fixtures/coding.json --iterations 25 --warmup 3 \
  --cpu-profile bench.cpu --mem-profile bench.mem
```

Prefer a canned workflow? `make bench` replays the default fixture 25 times (with
three warm-up passes) and honors `PROFILE=1 make bench` to emit CPU/heap profiles under
`docs/flamegraphs/`. The [performance note](./docs/perf.md) shows the resulting
benchmarks versus v0.4 and explains how to turn those profiles into flamegraphs.

The fixture supports two formats:

- **JSON** – provide world snapshots plus an ordered list of events. Keys mirror the internal types (`clients`, `workspaces`, `monitors`, `activeWorkspace`, `activeClient`). Each event object accepts `kind`, `payload`, and an optional `delay` duration string.
- **Event log** – plain text lines matching Hyprland's `kind>>payload` stream. When this format is used, the harness falls back to the built-in synthetic world snapshot and replaces only the event list.

Useful flags:

- `--iterations` – number of times to replay the stream (default `10`).
- `--warmup` – warm-up iterations to discard before timing begins (default `0`).
- `--mode` – override the mode selected before the first reconcile.
- `--respect-delays` – sleep for any `delay` values encoded in the fixture events.
- `--cpu-profile` / `--mem-profile` – emit pprof-compatible profile files for deeper analysis.
- `--log-level` – adjust engine logging verbosity during the run (defaults to `warn`).
- `--event-trace` – write a JSON array describing every replayed event (iteration, event index, latency, dispatch delta).

The harness prints summary statistics after replaying the stream: total dispatches (with per-iteration and per-event breakdowns), latency percentiles (min/avg/p50/p95/max in milliseconds), and allocation counts/bytes per event. Use the Makefile target (`make bench`) for a quick replay of the synthetic fixture with the repository's example configuration.

Pair the JSON summary with the optional event trace (`--event-trace bench-events.json`) to isolate the slowest events before diving into flamegraphs. Each entry captures iteration, event kind, payload, latency, and dispatch delta so you can map spikes back to concrete Hyprland activity.

## Configuration

Configuration is YAML with modes, rules, and actions. Start by copying a preset from [`examples/`](./examples/)—each directory includes a `config.yaml` you can place at `~/.config/hyprpal/config.yaml` and a README describing its assumptions. A condensed example:

```yaml
managedWorkspaces:
  - 3
  - 4
gaps:
  inner: 8
  outer: 20
tolerancePx: 2
modes:
  - name: Coding
    rules:
      - name: Dock comms on workspace 3
        when:
          all:
            - mode: Coding
            - workspace.id: 3
            - apps.present: [Slack, discord]
        priority: 10 # higher values run first when multiple rules match
        actions:
          - type: layout.sidecarDock
            params:
              workspace: 3
              side: right
              widthPercent: 25 # must be between 10 and 50
              focusAfter: host # host|sidecar|none; host restores focus to the previously active window
              match:
                anyClass: [Slack, discord]
  - name: Gaming
    rules:
      - name: Fullscreen active game
        when:
          mode: Gaming
        actions:
          - type: layout.fullscreen
            params:
              target: active
```

Place the configuration at `~/.config/hyprpal/config.yaml` to align with the provided systemd unit. `managedWorkspaces` scopes hyprpal’s actions to the listed workspace IDs by default—rules will be skipped elsewhere unless they set `mutateUnmanaged: true`. Leaving the list empty allows every workspace to be managed. Use the root-level `gaps` block to mirror your Hyprland gap settings (outer gap trims the monitor rectangle, inner gap is placed between floated windows). `tolerancePx` controls how much wiggle room hyprpal allows when comparing rectangles for idempotence; keep it aligned with Hyprland’s rounding behavior (default `2px`, or `0` for exact matches). `priority` is optional and defaults to `0`; higher values run earlier when multiple rules in a mode match the same cycle. `layout.sidecarDock` enforces a width between 10–50% of the monitor; values below 10% are rejected during config loading. Use the optional `focusAfter` field to steer which client should hold focus once the dock is positioned: `sidecar` (default) keeps focus on the dock, `host` restores the previously active window when possible, and `none` leaves Hyprland to follow the last command executed. The daemon automatically reloads when this file changes and still honors `SIGHUP` (e.g. `systemctl --user reload hyprpal`) for manual reloads. Runtime flags such as `--dispatch=socket|hyprctl`, `--dry-run`, `--log-level`, and `--mode` let you adjust behavior without editing the config file.


Hyprland can advertise `reserved` monitor insets (top/right/bottom/left) for panels or bars. hyprpal merges those values with an optional `manualReserved` map so rules operate on the actual usable area. Declare overrides with monitor names (`manualReserved: { "DP-1": { top: 32 } }`) or use `"*"`/`""` as wildcards when you want a default applied everywhere. Manual entries replace the compositor-provided numbers—set an edge to `0` to clear it or raise it to keep windows clear of your own elements. The merged insets are applied before gaps, ensuring actions such as `layout.sidecarDock` respect both Hyprland’s reserved areas and your explicit adjustments.

### Adjust presets for your setup

When reusing a preset, update monitor names, workspace IDs, and application classes to match your Hyprland environment. The accompanying READMEs in each [`examples/`](./examples/) preset (for instance, [`examples/dual-monitor/README.md`](./examples/dual-monitor/README.md)) call out the expected monitor names and client classes so you know what to tweak before copying the config. If you adopt multiple presets, consult each README to understand the assumptions around docking widths, preferred workspaces, and optional dependencies.

## Guardrails: loop protection & workspace scoping

hyprpal keeps a few safety rails in place so layout automation can be tested without accidentally spamming Hyprland:

- **Rule loop protection.** Each unique rule/plan signature may execute at most three times within a five-second sliding window. When the threshold is exceeded, hyprpal applies a five-second cooldown and logs a warning similar to `rule Dock comms temporarily disabled after 3 executions in 5s [mode Coding]`. Use `--log-level=debug` (or watch `journalctl` for warnings) while iterating to confirm the guard trips when you induce a loop; the log entry lets you verify the plan signature and timing.
- **Managed workspace allow-list.** The new root-level `managedWorkspaces` array defines the workspace IDs hyprpal is allowed to manipulate. Actions automatically skip unmanaged workspaces and emit an info-level log when they do so. Individual rules can opt out by adding `mutateUnmanaged: true`, making it easy to run a focused rule (e.g. forcing fullscreen for the active game) without broadening the global allow-list.

During dry runs (`--dry-run`), these guardrails still apply. Combine `--dry-run` with `--log-level=debug` to observe skipped rules, cooldown activation, and managed-workspace checks before enabling live dispatches.

## Troubleshooting & Logging

`hyprpal` produces structured trace output that mirrors the engine lifecycle. Run the daemon with `--log-level=trace` (for example, `hyprpal --log-level=trace --mode Coding`) to surface JSON payloads describing events, world diffs, matched rules, and dispatch outcomes. Trace mode is invaluable when wiring new rules or chasing layout loops because every step is annotated with the data the engine used to decide.

Representative trace excerpt (with `redactTitles: true` so window titles are replaced by `[redacted]`):

```
[TRACE] event.received {"kind":"openwindow","payload":"[redacted]"}
[TRACE] world.reconciled {"counts":{"clients":5,"workspaces":3,"monitors":1},"activeWorkspace":3,"activeClient":"0xabc","delta":{"changed":true,"clientsAdded":["0xdef"],"activeWorkspace":{"from":2,"to":3}}}
[TRACE] rule.matched {"mode":"Coding","rule":"Dock comms on workspace 3","reason":"apps.present matched Slack"}
[TRACE] plan.aggregated {"commandCount":2,"commands":[["dispatch","movewindowpixel","exact","0","0"],["dispatch","resizewindowpixel","exact","480","1440"]]}
[TRACE] dispatch.result {"status":"applied","command":["dispatch","movewindowpixel","exact","0","0"]}
```

Common debugging workflows:

- **Verify event ingestion.** Ensure `event.received` lines appear when you open or focus windows; missing events often indicate `HYPRLAND_INSTANCE_SIGNATURE` is unset in the environment running `hyprpal`.
- **Inspect world reconciliation.** Compare successive `world.reconciled` payloads to confirm clients/workspaces are counted correctly. The `delta` object highlights additions/removals and workspace focus changes—useful for spotting stale state when Hyprland restarts.
- **Understand rule matches.** `rule.matched` entries list the mode, rule name, and reason string generated by predicates. If a rule is skipped unexpectedly, bump to trace level and confirm whether the rule ever matches.
- **Diagnose dispatch issues.** `dispatch.result` lines include `status` values (`dry-run`, `applied`, or `error`). When status is `error`, the payload also contains the Hyprland error string so you can replay the failing command with `hyprctl` manually.
- **Validate title redaction.** Enable `redactTitles: true` in your config (or toggle it at runtime via `hsctl reload`) to redact client titles inside stored world snapshots, trace logs, and info-level dispatch logs. This is helpful when sharing traces or debugging on a streamed session.

Pair `--log-level=trace` with `--dry-run` to audition rules without moving windows. For persistent logging, rely on `journalctl --user -fu hyprpal` or redirect stdout to a file while running under `make run`.

## Performance & Benchmarks

`make bench` wraps `go run ./cmd/bench --config configs/example.yaml --fixture fixtures/coding.json --iterations 25` to
replay the synthetic Coding-mode fixture and capture latency/allocation stats. The command prints a JSON payload by default;
pipe to `jq '.summary'` for the aggregated metrics that feed the table below or pass `--output bench.json` to save the full
report for later comparison. Add `--human` to mirror the key numbers in a tabular summary directly in the terminal. The JSON
payload also now captures per-iteration duration and dispatch counts under `iterations[]`, while the summary exposes
`iterationDuration` stats so you can line up pprof flamegraphs with the slowest replays.
Fixtures can
be expressed as JSON snapshots (see `fixtures/coding.json`) **or** plain event logs containing `kind>>payload` lines; in the
latter case the base world snapshot from the JSON fixture is reused. Use `--respect-delays` to honor any `delay` strings
captured in the fixture, and `--mode` to force a particular rule mode when the recorded stream did not specify one. Pass `PROFILE=1` to emit CPU/heap profiles in `docs/flamegraphs/` (see
[docs/perf.md](docs/perf.md) for the exact workflow plus instructions for replaying your own capture).

Need to drill into the worst-case spikes before opening pprof? Supply `--event-trace docs/flamegraphs/v0.5-events.json` alongside the usual flags. Pipe the resulting JSON array through tools like `jq 'sort_by(-.durationMs)[:5]'` to surface the five slowest events so you know which flamegraph sample to inspect first.

The summary payload now includes `heapAllocDeltaBytes`/`heapObjectsDelta` alongside the existing allocation counters so you can
track the net heap growth per iteration. This makes it straightforward to spot regressions relative to v0.4 (which routinely
left >64 KB of heap behind after each iteration) and validate that the pooled worlds introduced in v0.5 release almost all
temporary objects before the next event arrives.

**Key gains over v0.4 (synthetic Coding-mode stream, 25 iterations, Go 1.22.2 on Ryzen 7 7840U):**

| Metric | v0.4 | v0.5 | Δ |
| --- | --- | --- | --- |
| Mean event latency | 3.9 ms | 1.7 ms | 2.2 ms faster (−56%) |
| p95 event latency | 9.7 ms | 4.3 ms | 5.4 ms faster (−56%) |
| Allocations / event | 312 | 118 | −62% |
| Bytes / event | 126 KB | 45 KB | −64% |
| Total dispatches / iteration | 41 | 36 | −12% |

The CPU flamegraph (`docs/flamegraphs/v0.5-bench-cpu.svg`) shows `engine.applyPlan` shrinking to ~18% of samples (down from
41% in v0.4) after batching rectangle diffs, while the heap flamegraph
(`docs/flamegraphs/v0.5-bench-heap.svg`) highlights allocator savings from the pooled world snapshots. Inspect the diffs with
`go tool pprof -http=:0 docs/flamegraphs/v0.5-bench-{cpu,heap}.pb.gz` to explore hot paths interactively.

## Makefile targets

- `make build` – compile to `bin/hyprpal`.
- `make run` – run the daemon against `configs/example.yaml`.
- `make smoke` – run the smoke CLI to inspect the current world snapshot against `configs/example.yaml`.
- `make bench` – replay the synthetic fixture through the engine using `configs/example.yaml`.
- `make install` – install the binary to `~/.local/bin` (override with `INSTALL_DIR=...`).
- `make service` – reload and start the user service.
- `make lint` – run `go vet` plus a `gofmt` check.
- `make test` – execute unit tests.

## Systemd (user) service

Install the binary with `make install` (which places it at `~/.local/bin/hyprpal` by default), copy `system/hyprpal.service` to `~/.config/systemd/user/`, then enable it:

```bash
mkdir -p ~/.config/systemd/user/
cp system/hyprpal.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now hyprpal.service
journalctl --user -fu hyprpal
```

## Acceptance smoke test

1. Start Hyprland and open Slack/Discord plus your editor on workspace 3.
2. Run `hyprpal --mode Coding`.
3. Observe a log similar to:
   ```
   [INFO] DRY-RUN dispatch: [setfloatingaddress address:0xabc 1]
   [INFO] DRY-RUN dispatch: [movewindowpixel exact 0 0]
   [INFO] DRY-RUN dispatch: [resizewindowpixel exact 480 1440]
   ```
4. Re-run without `--dry-run` to apply the sidecar.
5. Switch to Gaming mode (`hyprpal --mode Gaming`) and launch a game window; it will be forced fullscreen.

## Roadmap (v0.1 → v0.2)

- Grid layout primitive and Coding mode demo.
- Hot reload config watcher plus richer error reporting.
- Replace `hyprctl` shell-outs with direct socket IPC for lower latency.
- Extend `hsctl` with scripted workflows (macros, templated plans).
- Guardrails for loop protection and managed-workspace scoping.
