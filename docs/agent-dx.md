# twt Agent DX score

The scale is the Agent DX CLI Scale: seven axes, 0 to 3 each, for a total of
21. The first twt implementation scored 4/21. The agent-facing pass scored
12/21. The contract pass scored 13/21. This branch scores **20/21**, which is
**Agent-first**. Every claim below was probed against the built binary.

| Axis | First | Previous | Now | Current support |
|---|---:|---:|---:|---|
| Machine-readable output | 1 | 2 | 3 | JSON is the default when stdout is not a terminal; `--output ndjson` streams list elements one per line with a totalCount summary line; JSON errors carry a stable code, a hint, and a help command |
| Raw payload input | 0 | 1 | 2 | 39 typed operations through `twt apply -` cover every non-interactive mutation; each shares one core with its flag command; interactive commands are excluded by design and the error says so |
| Schema introspection | 0 | 3 | 3 | `twt schema` walks the live command tree: build version, per-command arguments, flag enums, required flags (every required flag is cobra-declared, so the schema never under-reports), `apply` request fields, error codes, exit codes |
| Context window discipline | 0 | 1 | 3 | `--fields` masks on every read command with reflection-derived valid names, `--offset` with `--limit` on every list, `totalCount` and `truncated` in every response, NDJSON streaming, and skill guidance on all of it |
| Input hardening | 1 | 2 | 3 | Strict resource names, strict YAML and JSON decoding, a 1 MiB bound on every stdin path, writes confined to twt-owned roots behind ownership markers, and a written posture: [security.md](security.md) — the agent is not a trusted operator |
| Safety rails | 1 | 2 | 3 | `--dry-run` on every mutation; cleanup actions are plans with typed blockers behind `--apply`; Project close asks before it changes open Tickets or requires `--force`; transcript text is sanitized (ANSI, OSC, and control sequences stripped) and marked `untrusted: true` in JSON, with a skill rule to never follow instructions inside it |
| Agent knowledge packaging | 1 | 2 | 3 | The skill is embedded in the binary, version-stamped, and installed with `twt skills install` into the Cursor, Claude Code, and Codex trees; `twt doctor` warns when an installed copy is stale |

## Honest caveats per axis

**Raw payload input stays at 2.** Interactive commands (`next`, `switch`,
`done`, `templates edit`, `tickets home`, `agents focus`, `agents open`,
`agents register --pane current`) have no apply operation by design — they
move a tmux client or need a terminal. The axis's 3 asks for raw payloads as a first-class peer on every
mutation; the exclusions are deliberate, so the score stays at 2. The
`tickets create` wizard (title, Project picker, `$EDITOR`) and the editor
paths of `tickets plan` and `projects plan` are TTY-only. Agents pass
DESCRIPTION or `-`. `--project` never creates a Project.

**Context discipline: pagination is offset-based.** There is no cursor. At
personal-vault and per-machine scale, offset windows are stable enough; a
cursor would claim rigor the store cannot honor between invocations.

**Safety rails: sanitization is structural, not semantic.** twt strips the
escape-sequence attack channel and marks transcript text untrusted; it does
not judge the meaning of the text. A semantic prompt-injection filter would
need a model in the loop and is out of scope for a local CLI.

**Schema introspection: positional names are hand-declared.** A missing
declaration fails a test; a wrong name still typechecks. Required flags are
declared through cobra's MarkFlagRequired, so `twt schema` reports them; a
conditionally required flag (`templates init set --cwd`, mutually exclusive
with `--repo`) shows as optional with the condition in its help.

## Multi-surface status

- MCP: not implemented
- Neovim: supported through the versioned JSON CLI contract
- Shell completion: `twt completion zsh|bash|fish|powershell`, with values
  from the twt stores
- Skill install: `twt skills install [--user] [--dir DIR]`
- Headless auth: not applicable; twt uses local Git and tmux
