# twt

twt is a personal Markdown backlog and a local agent runner. You write
Tickets. You dispatch coding agents into tmux Workspaces on this machine.
The CLI owns every write. There is no server.

A person at a terminal uses text. An agent uses `--output json`. Both use
the same commands.

## You and the agent

You decide the work. The agent does the work. twt is the ledger.

| You | The agent |
|---|---|
| Create a Project and write the plan | Read the Ticket and the Project plan |
| Split the plan into Tickets | Claim the Ticket |
| Approve a Ticket plan | Write the Ticket plan and ask you |
| Answer questions on the board | Implement, attach each pull request, complete |
| Close a Ticket when every pull request merges | Stop and wait when a decision is missing |

Do not edit Ticket files by hand. Do not ask an agent to do that either.
Every mutation goes through `twt tickets` or `twt apply`.

## Language

| Noun | Meaning |
|---|---|
| Ticket | One Markdown file. Status, blockers, labels, claim, pull requests. |
| Project | A directory of Tickets plus an optional `plan.md`. |
| Label | A loose theme on a Ticket. Not a Project. No `plan.md`. |
| Claimant | Who holds a Ticket (`--as NAME`). One worker per Ticket. |
| Workspace Template | Reusable YAML. Repositories, tmux layout, dispatch defaults. |
| Workspace | One change. Git worktrees plus one tmux session. |
| Agent Session | One coding-agent run inside a Workspace. |

A Ticket is pickable when it is `ready-for-agent`, unclaimed, and every
blocker is `done` or `wontfix`. A Ticket with a `## Plan` section does not
dispatch for implementation until you run `twt tickets approve`.

## Install

You need Go, git, tmux, and one agent CLI (`codex`, `claude`, `cursor`, or
`grok`).

```sh
go install github.com/jpugliesi/tmux-worktree/cmd/twt@latest
```

`go install` writes `twt` to GOBIN (`$(go env GOPATH)/bin`, often
`~/go/bin`). Put that directory on PATH. Do not put a checkout `bin/`
ahead of it. If you do, `go install` updates a binary your shell never
runs.

```sh
which twt
twt skills install
twt tickets init
```

`twt skills install` writes the agent operating contract into Cursor,
Claude Code, and Codex. Run it again after every `twt` upgrade.
`twt doctor` warns when an installed skill does not match the binary.

Set the Tickets home in `~/.config/twt/config.yaml`. An Obsidian vault
folder works.

```yaml
home: /path/to/your/twt-home
ticketAgent:
  provider: codex
  effort: large
```

`home` holds `tickets/` and shared Workspace Templates. A tickets-only
client can set `ticketsHome` instead. `twt config` shows every resolved
setting.

## Text and JSON

Text is the default. A pipe stays text.

```sh
twt tickets ls
twt tickets ls | grep factory
twt projects get myfeature
```

An agent, a script, and Neovim pass `--output json` on every command.
`--output ndjson` streams one object per line on a list command.

```sh
twt tickets list --ready --output json --limit 20
twt tickets list --label change-monitor -A --output json
twt labels list --output json
twt schema
```

`--fields` and `--limit` keep a JSON read small. You do not need those
flags at a terminal.

## First project

1. Create a Workspace Template for the repository the agent will edit.

   ```sh
   twt templates create myproject
   twt templates repos add myproject app git@github.com:you/app.git
   ```

2. Create a Project. At a terminal you can omit the name. twt asks for
   it, opens an editor for the plan, and can start a Workspace.

   ```sh
   twt projects create myfeature --template myproject
   twt projects plan myfeature
   ```

3. Split the plan into Tickets. Create leaf Tickets first. A blocked Ticket stays
   `ready-for-agent`. The queue hides it until the blocker closes.

   ```sh
   twt tickets create "Add the API" --project myfeature --status ready-for-agent
   twt tickets create "Add the UI" --project myfeature --status ready-for-agent \
     --blocked-by add-the-api
   twt tickets create "Spike the monitor" --label change-monitor
   twt labels add change-monitor --ticket add-the-ui
   twt tickets list --label change-monitor -A
   twt labels list
   twt labels rename change-monitor monitor-theme
   twt labels remove monitor-theme --ticket spike-the-monitor
   twt tickets tree --project myfeature
   ```

   A Label is a loose theme. It does not need a Project. `twt labels add`,
   `remove`, and `rename` rewrite Ticket frontmatter and do not move files.
   `--label` never creates a Project. An empty `--project` removes the
   Ticket from its Project:

   ```sh
   twt tickets set spike-the-monitor --project ""
   ```

4. Dispatch. `--plan` starts a planning agent. Plain dispatch starts
   implementation. Dispatch claims the Ticket, creates a Workspace, and
   starts the agent in tmux.

   ```sh
   twt tickets dispatch add-the-api --plan
   twt tickets approve add-the-api
   twt tickets dispatch add-the-api
   ```

5. Watch the board. Everything that needs you is under WAITING ON YOU.

   ```sh
   twt projects get myfeature
   twt tickets tree --project myfeature
   ```

6. Attach when you want to steer. The Workspace tmux session is already
   running.

   ```sh
   twt workspaces list --project myfeature
   twt switch
   twt agents list
   ```

## Your loop

Read the board. Act on WAITING ON YOU first. Then close merged work.
Then dispatch the next ready Ticket.

```sh
twt projects get myfeature
printf '%s' "Use OAuth." | twt tickets answer some-ticket -
twt tickets approve some-ticket
twt tickets close some-ticket
twt tickets dispatch NEXT_READY_TICKET
```

Close a Ticket when every pull request is merged. The close unblocks
dependents. Review feedback stays on the same Ticket. Re-claim it, fix
the feedback, and complete again.

Close the Project when the work ends.

```sh
twt projects close myfeature
```

## The agent loop

Install the skill on every machine that runs an agent. The skill tells
the agent to use JSON, dry-run mutations, and `twt schema`.

A planning dispatch writes a `## Plan` section, asks you questions, and
waits for `twt tickets approve`. An implementation dispatch refuses a
planned Ticket you have not approved.

The worker contract is one write at the end:

```sh
twt tickets complete TICKET --as CLAIMANT --pr URL --output json
```

That records the pull requests, releases the claim, and sets
`ready-for-human`. A worker that cannot finish comments the blocker and
runs `twt tickets unclaim`.

A coordinator, a cron, or you can run one wave and stop:

```sh
twt tickets sync --project myfeature --output json
twt projects get myfeature --output json
```

Act on `waitingOnYou`, then `inReview` (close when every pull request is
merged), then `ready` up to `capacity.available`. Do not poll in a tight
loop.

## Workspaces without tickets

The Workspace layer also works on its own.

```sh
twt create fix-auth --template myproject
twt next
twt switch
twt done
```

`twt templates prepare` keeps a warm pool of initialized worktrees.
`twt done` returns those worktrees to the pool and keeps the Workspace
record. Providers are `codex`, `claude`, `cursor`, and `grok`.

## More than one machine

Git is the wire. Set `ticketsSync.mode: git` so every Ticket write
commits and pushes. A claim is a push. Two agents cannot both win the
same Ticket.

```yaml
home: /home/you/twt-home
ticketsSync:
  mode: git
  remote: origin
```

Use non-interactive git credentials. Claims run inside twt commands, so
the push must not prompt.

Two client shapes work.

**Concierge.** A bot syncs, reads the board, and surfaces what needs you.
It can run `answer`, `approve`, `close`, and `projects plan`. Dispatch
stays on the workstation that has tmux.

**Bring your own executor.** A bot claims ready work, runs its own
agents, and reports with `twt tickets pr add` and `twt tickets complete`.
twt is the ledger. The executor is whatever the client has.

See [docs/twt.md](docs/twt.md) for the full git-sync rules.

## Reference

| Document | What it is |
|---|---|
| [docs/twt.md](docs/twt.md) | Command reference. Templates, Workspaces, tickets, dispatch, JSON. |
| [docs/tickets.md](docs/tickets.md) | Ticket store contract. Files, frontmatter, claims. |
| [docs/agent-dx.md](docs/agent-dx.md) | Why the CLI looks this way to agents. |
| [docs/security.md](docs/security.md) | Threat model. The agent is not a trusted operator. |
| `twt schema` | Live machine contract. Commands, flags, apply operations, errors. |
| `twt skills get` | The skill this build embeds. |

## License

MIT
