# twt2 Agent DX score

The scale is the Agent DX CLI Scale: seven axes, 0 to 3 each, for a total of
21. The first twt2 implementation
scored 4/21. The agent-facing pass scored 12/21. This branch scores **13/21**,
which stays in the **Agent-ready** range.

| Axis | First | Previous | Now | Current support |
|---|---:|---:|---:|---|
| Machine-readable output | 1 | 2 | 2 | `--output json` for every command, named read envelopes, and JSON errors with a stable code, a hint, and a help command |
| Raw payload input | 0 | 1 | 1 | Strict JSON for six mutations through `apply --stdin`, plus strict YAML through `templates create --from-file` and `--from-stdin` |
| Schema introspection | 0 | 2 | 3 | `twt2 schema` walks the live command tree and gives the build version, per-command arguments, flags with enums, `apply` request fields, error codes, and exit codes |
| Context window discipline | 0 | 1 | 1 | `--limit` on every list command, with `totalCount` and `truncated` in the response |
| Input hardening | 1 | 2 | 2 | Strict resource IDs, strict YAML and JSON fields, relative init paths, and twt2 ownership markers before each destructive action |
| Safety rails | 1 | 2 | 2 | `--dry-run` for every mutation; Project removal is a plan with typed Removal Blockers |
| Agent knowledge packaging | 1 | 2 | 2 | One discoverable `twt2` skill with agent guardrails, error-code handling, and the current sentinel |

## Why each axis has this score

**Machine-readable output: 2.** Every command group takes `--output json`, and
`--output` is the only format flag. Errors carry `code`, `message`, `hint`, and
`helpCommand`. The axis needs NDJSON for paginated results and structured
output as the default in a non-TTY context for a 3. twt2 has neither: a piped
call still needs `--output json`.

**Raw payload input: 1.** `apply --stdin` now covers `templates.create`,
`templates.repos.add`, `projects.create`, `projects.archive`,
`projects.remove`, and `agents.register`, and one shared table drives both the
supported list and the schema, so the two cannot drift. The axis needs a raw
payload for *all* mutating commands for a 2. Many mutations, such as
`projects setup retry`, `templates init set`, `templates remove`,
`agents send`, and `storage clean`, still need flags.

**Schema introspection: 3.** The schema comes from the live command tree of
the installed binary. It carries the build version, so it always describes the
running version. It includes flag enums, nested `apply` request field paths,
the error-code list, and the exit-code map, and it excludes the generated
`help` and `completion` commands. Each command declares its positional
arguments next to its `Use` value, and a test refuses a command that shows an
uppercase placeholder without that declaration. The remaining weakness is
honest: the positional declaration is written by hand next to the parser, so a
wrong argument *name* is still possible even though a missing declaration is
not.

**Context window discipline: 1.** `--limit` exists on `templates list`,
`projects list`, `agents list`, `agents discover`, and `environments list`.
Each response reports `totalCount` and `truncated`, so an agent knows what the
limit removed. The axis needs field masks on all read commands and pagination
for a 2. twt2 has no field mask and no cursor, and a large `agents transcript
show` result can still fill a context window.

**Input hardening: 2.** Resource names use a strict pattern that rejects path
separators, `..`, percent signs, query characters, and control characters. YAML
decoding rejects unknown fields and more than one document. JSON decoding
rejects unknown fields, more than one value, and input over 1 MiB. Project
initialization paths must stay inside the Project root. Removal changes only a
root that carries the matching twt2 ownership marker. The axis needs an
explicit written security posture and output-path sandboxing for a 3. The
posture line now exists in the preview guide, but the guarantees are not
listed per command, so the score stays at 2.

**Safety rails: 2.** Every mutation accepts `--dry-run`, including `apply`,
`done`, `templates edit`, and `templates remove`. Project removal is a plan
by default, and each refusal is a typed Removal Blocker with a stable code.
The axis needs response sanitization for a 3. twt2 returns provider transcript
text without any sanitization, so a prompt-injection string in an Agent
Transcript reaches the caller unchanged. This is the largest open risk.

**Agent knowledge packaging: 2.** The `twt2` skill has YAML frontmatter and
covers the schema-first, limit, dry-run, current-sentinel, error-code, and
finish workflows. The axis needs a versioned, standard-conformant skill
library for a 3. twt2 ships one skill file with no version field.

## Next useful work

1. Response sanitization for transcript output (axis 6, and the only security
   gap that an agent cannot work around).
2. Field masks for read commands and NDJSON for lists (axes 1 and 4).
3. A raw JSON request for every mutation (axis 2).

Multi-surface status:

- MCP: not implemented
- Neovim: supported through the versioned JSON CLI contract
- Shell completion: `twt2 completion zsh|bash|fish|powershell`, with values
  from the twt2 stores
- Headless auth: not applicable; twt2 uses local Git and tmux
