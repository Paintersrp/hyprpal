# hyprpal Benchmark Methodology (v0.5)

This document captures the raw numbers and workflow used to validate the engine
regressions for v0.5 relative to v0.4. Re-run the steps below to confirm the
results or to profile your own configuration replay.

## Environment

- **Host:** Framework 13 (Ryzen 7 7840U, 32 GB RAM)
- **OS:** Arch Linux (6.8.7-arch1-1)
- **Go:** 1.22.2
- **Hyprland:** 0.39.1 (used for the captured event stream)

## Command summary

```bash
# Ensure binaries are up to date
make build

# Run the synthetic Coding-mode benchmark 25x and emit pprof profiles
PROFILE=1 make bench

# Equivalent manual invocation if you want to change paths/flags
PROFILE=1 go run ./cmd/bench \
  --config configs/example.yaml \
  --fixture fixtures/coding.json \
  --iterations 25 \
  --output docs/flamegraphs/v0.5-bench.json \
  --cpu-profile docs/flamegraphs/v0.5-bench-cpu.pb.gz \
  --mem-profile docs/flamegraphs/v0.5-bench-heap.pb.gz
```

Set `PROFILE=1` in the environment to instruct the Makefile target to add the
`--cpu-profile`/`--mem-profile` flags. Replace the paths to generate additional
profiles; the README references the `docs/flamegraphs/v0.5-bench-{cpu,heap}.pb.gz`
artifacts by default. Add `--output` to persist the JSON payload (handy for
tracking regressions with version control). Fixtures may be JSON snapshots or
plain event logs (`kind>>payload` per line); when only a log is supplied the base
world from `fixtures/coding.json` seeds the replay so window counts and monitor
metadata remain consistent. Use `--respect-delays` to mirror pacing captured in
the fixture. Convert profiles to SVG flamegraphs with:

```bash
go tool pprof -http=:0 docs/flamegraphs/v0.5-bench-cpu.pb.gz
# Click "Flame Graph" in the UI and export to docs/flamegraphs/v0.5-bench-cpu.svg
```

Repeat for the heap profile. Keep both the `.pb.gz` (raw profile) and `.svg`
(rendered flamegraph) if you plan to check them into documentation.

## Raw benchmark output

The numbers below are averaged over 25 iterations of the synthetic Coding-mode
fixture. `cmd/bench` prints the summary in JSON; capture the block with
`jq '.summary'` to mirror the table shown in the README.

| Metric | v0.4 | v0.5 | Change |
| --- | --- | --- | --- |
| Min latency | 1.9 ms | 0.9 ms | −53% |
| Mean latency | 3.9 ms | 1.7 ms | −56% |
| Median latency | 3.4 ms | 1.5 ms | −56% |
| p95 latency | 9.7 ms | 4.3 ms | −56% |
| Max latency | 12.8 ms | 6.2 ms | −52% |
| Allocations / event | 312 | 118 | −62% |
| Bytes / event | 126 KB | 45 KB | −64% |
| Dispatches / iteration | 41 | 36 | −12% |

`cmd/bench` also captures the net heap change between the GC sweeps that wrap the replay via the `heapAllocDeltaBytes` and
`heapObjectsDelta` fields. In v0.4 the synthetic workload would leak roughly 70 KB of live heap after each iteration; v0.5 stays
within single-digit kilobytes thanks to pooling reconciled worlds.

## What changed in v0.5

- **Rect batching:** the engine now groups compatible resize/move pairs before
  dispatching, eliminating redundant `movewindowpixel` calls and reducing total
  dispatch volume per iteration by ~12%.
- **World snapshot pooling:** reconciled worlds are recycled, cutting per-event
  allocations by 62% and reducing GC pressure. This change is visible in the
  heap flamegraph where `state.Clone` shrinks from 27% of samples to ~9%.
- **Precomputed rule predicates:** static predicate trees are memoized once per
  config reload, lowering mean latency to 1.7 ms on the synthetic workload.

## Replaying your own capture

1. Collect a Hyprland event log via `socat - UNIX-CONNECT:"$XDG_RUNTIME_DIR/hypr/$HYPRLAND_INSTANCE_SIGNATURE/.socket2.sock" > my.log`.
2. Record a matching world snapshot with `hyprctl -j monitors clients workspaces > my-world.json`.
3. Combine the snapshot and log into a fixture (see `fixtures/coding.json` for the schema).
4. Invoke `go run ./cmd/bench --config ~/.config/hyprpal/config.yaml --fixture my-fixture.json --iterations 25`.
5. Re-run the profiling commands above and compare the summary table to track regressions.

Document any deviations (hardware, Go version, fixture changes) alongside your
metrics when sharing results so others can reproduce them.
