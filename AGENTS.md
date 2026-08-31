# Working on twt

Context for coding agents (and humans) who change this repository. Read
[CONTEXT.md](CONTEXT.md) for the domain language. Read [README.md](README.md)
to learn how a person runs twt with agents. The docs under [docs/](docs/)
are the reference. [docs/agent-dx.md](docs/agent-dx.md) is the design bar
for the CLI surface.

## Build and test

| Recipe | What it does |
|---|---|
| `make build` | Compile every package. |
| `make check` | The full local gate: gofmt check, `go vet`, `go test ./...`. Run it before you call work done. |
| `make test` | Go tests only. The full run takes ~10 minutes; `internal/cli` is the slow package (it drives real git and tmux). |
| `make test/nvim` | The Neovim plugin suite (`nvim/twt.nvim/tests/test.sh`). Needs `nvim` and `tmux`. |
| `make test/all` | Go and Neovim suites. |
| `make install` | Install `twt` to GOBIN. |

Iterate on one package or one test, not the full suite:

```sh
go test ./internal/ticket/ -count=1
go test ./internal/cli/ -run 'TestTicketsApprove' -count=1
```

Read test output for `--- FAIL` lines. Do not trust the exit code of a
pipeline that ends in `head` or `grep`.

**Sandboxed environments:** if a test that runs a nested `go build` (for
example `TestAgentsListDiscoversAndPreviewsLiveProviderPanesWithoutWritingState`)
times out after 30s, the sandbox is blocking the default temp directory.
Re-run with `TMPDIR` pointing at a writable scratch directory. The Neovim
`tests/integration.lua` suite can fail the same way. Neither failure mode
indicates a code problem when the rest of the suite is green.

## The contract tests will catch you

The CLI surface is policed by tests in `internal/cli`. When you add, remove,
or rename a command or `apply` operation, expect these to fail until you
finish the job:

- **Help coverage** — every command needs an entry in
  `internal/cli/help.go` (`commandHelp`), and every entry must match a real
  command.
- **Arguments contract** — a `Use` string with a placeholder needs
  `setArguments(...)` declaring each positional. Do not put flags in `Use`.
- **Completion coverage** — every resource-shaped argument needs a
  completion function (`internal/cli/completion.go`).
- **Apply-op pin** — `internal/cli/agentdx_test.go` pins the exact count of
  `apply` operations. Adding or removing one means updating the literal.
- **Conformance suite** — `internal/ticket/conformance` is the executable
  contract of the `ticket.Store` interface. A new Store method usually needs
  a conformance case.

## Conventions

- Every mutation goes through `runMutation` (validate-then-apply) and
  honors `--dry-run`. Every ticket write goes through `Service.mutate` so
  git sync and claim safety hold on every client.
- Required flags are declared with `MarkFlagRequired` so `twt schema` stays
  truthful. `--output json` works on every non-interactive command.
- No new error codes, no new ticket statuses. Derive display states in
  views instead.
- `gofmt` is mandatory (`make fmt`).
- `skills/twt/SKILL.md` is the agent operating contract and is embedded in
  the binary (`go:embed`). When the command surface or a workflow changes,
  update it in the same change; `twt skills install` distributes it.
- User-facing text (help, errors, docs) uses short, direct sentences in
  the style of the existing help entries.

## Layout

| Path | Owns |
|---|---|
| `internal/cli` | Every command, `apply`, `schema`, help, completion. |
| `internal/ticket` | The ticket store (markdown + git sync, claims, queue, plans). |
| `internal/ticket/conformance` | The executable Store contract. |
| `internal/localdispatch` | Dispatching implementation/planning agents into workspaces. |
| `internal/agentprovider` | The generated agent prompts and provider argv. |
| `internal/workspace` | Worktrees, tmux sessions, prepared environments. |
| `internal/prstate` | Live pull request state (gh, origin) with a TTL cache. |
| `skills/twt/SKILL.md` | The embedded agent skill. |
| `nvim/twt.nvim` | The Neovim client (consumes the JSON contract). |
