# ADR-008: File-based CRI log parsing

**Status:** Accepted
**Date:** 2026-04-01
**Context:** CRI migration (M5)

## Context

CRI logs are file-based. The runtime writes container stdout/stderr to
a log file in a standardised format:

```
2024-01-15T10:30:00.000000000Z stdout F hello world
2024-01-15T10:30:00.001000000Z stderr F error: something broke
```

Alternatives considered:
- Streaming logs via `ContainerStats` gRPC stream — complex, varies by
  runtime implementation
- Direct file access — consistent format, supports seek/grep, historical
  queries

## Decision

Parse CRI log files directly from the filesystem. The log path is
obtained from `ContainerStatus` and follows the convention
`{LogDirectory}/{container-name}/0.log`.

Implementation (`pkg/logs/`):
- **Parser:** splits CRI log format lines, handles partial-line (P tag)
  merging, supports `--since` and `--tail` filtering
- **Multiplexer:** reads from all service log files, merges entries in
  timestamp order, outputs with coloured service prefixes (6-colour
  ANSI cycle)
- **Follow mode:** file-offset polling at 200ms intervals, clean exit
  on SIGINT/SIGTERM
- **Dump mode:** historical display with RFC3339 or duration `--since`
  and number or "all" `--tail`

## Consequences

- Consistent log format across all CRI runtimes (format is spec'd)
- Direct file access — no subprocess, no stdout pipe
- Can seek/grep log files for historical queries
- Follow mode uses polling (not fsnotify) for simplicity; 200ms
  latency is acceptable
- Log rotation by the runtime (containerd rotates by default) means
  file path may change; currently handled by re-reading status
