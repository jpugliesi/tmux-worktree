# twt2 Agent DX score

The first twt2 implementation scored 4/21 on the Agent DX CLI Scale. The
agent-facing pass raises the score to 12/21, which is **Agent-ready**.

| Axis | Before | After | Current support |
|---|---:|---:|---|
| Machine-readable output | 1 | 2 | `--output json` and structured JSON errors |
| Raw payload input | 0 | 1 | Strict JSON for common mutations through `apply --stdin` |
| Schema introspection | 0 | 2 | Runtime command, argument, flag, and apply-operation schema |
| Context window discipline | 0 | 1 | `--limit` on all list commands |
| Input hardening | 1 | 2 | Strict resource IDs, YAML fields, JSON fields, and relative init paths |
| Safety rails | 1 | 2 | Global `--dry-run`; Project removal is a plan by default |
| Agent knowledge packaging | 1 | 2 | A discoverable `twt2` skill with agent guardrails |

The next useful work is full raw JSON input for every mutation and field masks
for every read command. NDJSON, automatic JSON for non-TTY output, and response
sanitization are later Agent-first features.

Multi-surface status:

- MCP: not implemented
- Neovim: supported through the versioned JSON CLI contract
- Headless auth: not applicable; twt2 uses local Git and tmux
