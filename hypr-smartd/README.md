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
- Add `hsctl` CLI helper for mode inspection and manual actions.
- Guardrails for loop protection and managed-workspace scoping.
