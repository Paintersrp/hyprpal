# hyprpal

`hyprpal` is a small companion daemon for [Hyprland](https://hyprland.org/) that listens to compositor events, maintains a live world snapshot of monitors/workspaces/clients, and applies declarative layout rules. v0.1 ships with sidecar docking and fullscreen enforcement actions so you can keep communication apps docked while focusing on your main workspace or go distraction-free in Gaming mode.

## Quick Start

1. Ensure Hyprland is running and `hyprctl` is available on your `$PATH`.
2. Build the daemon:
   ```bash
   make build
   ```
3. Copy `configs/example.yaml` to `~/.config/hyprpal/config.yaml` and edit it for your workspace/app names.
4. Run the daemon from your session:
   ```bash
   make run
   ```
   Use `--dry-run` to preview dispatches without affecting windows.
5. Follow logs while iterating:
   ```bash
   journalctl --user -fu hyprpal
   ```

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

## Configuration

Configuration is YAML with modes, rules, and actions. A condensed example:

```yaml
modes:
  - name: Coding
    rules:
      - name: Dock comms on workspace 3
        when:
          all:
            - mode: Coding
            - workspace.id: 3
            - apps.present: [Slack, discord]
        actions:
          - type: layout.sidecarDock
            params:
              workspace: 3
              side: right
              widthPercent: 25 # must be between 10 and 50
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

Place the configuration at `~/.config/hyprpal/config.yaml` to align with the provided systemd unit. `layout.sidecarDock` enforces a width between 10–50% of the monitor; values below 10% are rejected during config loading. The daemon automatically reloads when this file changes and still honors `SIGHUP` (e.g. `systemctl --user reload hyprpal`) for manual reloads.

## Makefile targets

- `make build` – compile to `bin/hyprpal`.
- `make run` – run the daemon against `configs/example.yaml`.
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
