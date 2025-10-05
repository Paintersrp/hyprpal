# Coding preset

This preset assumes a dual-monitor workstation with the primary development monitor exposed as `DP-1` and a secondary communications monitor exposed as `HDMI-A-1`.

## Grid layout structure
- Workspace `1` uses `layout.grid` to keep the IDE focused:
  - `colWeights: [2, 1]` doubles the editor column compared to the utility column.
  - `rowWeights: [3, 2]` keeps the top row dominant while reserving space for documentation panes.
  - Slot `editor` spans two rows (rows `0-1`) so the IDE keeps full height.
  - Slot `terminal` lives on row `0`, column `1` for quick command access.
  - Slot `docs` occupies row `1`, column `1` for browsers, notes, or API references.
  - Adjust slot `match` clauses to map the layout to your application classes.

## Application targets
- IDE window with class `code` (Visual Studio Code) on workspace `1`
- Terminal windows with class `Alacritty` to dock alongside the IDE
- Browser or documentation windows such as `firefox` or `brave-browser` for the `docs` slot

## Workspace layout assumptions
- Workspace `1` is your main development surface on `DP-1`
- Workspace `2` is left unmanaged for ad-hoc windows
- Workspace `3` can be repurposed for persistent communications or other layouts as needed

Adjust monitor names, classes, or workspace IDs to match your Hyprland environment.
