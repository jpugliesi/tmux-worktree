# twt

**twt** turns a folder of Markdown tickets into an orchestration layer for
coding agents. You describe a project as a plan, break the plan into a
dependency graph of tickets, and dispatch autonomous agents to implement
them — in tmux workspaces on your own machine. The tickets live in a plain
git repository, so every machine (and every agent, and every bot) that can
run `git push` is a full read/write client. There is no server.

What that gives you:

- **A backlog you own.** Tickets are Markdown files with YAML frontmatter in
  a directory you choose (an Obsidian vault works well). Every mutation goes
  through the CLI, so agents and humans share one safe write path.
- **Safe concurrency across machines.** A ticket claim is a git push: the
  push is the compare-and-swap, so two agents on two machines cannot both
  win the same ticket.
- **A planning loop with a human gate.** Planning agents write a plan into
  the ticket and ask for your approval; implementation dispatch refuses a
  planned ticket you have not approved.
- **Visibility.** A dependency tree and a project board with live pull
  request state show exactly what is in progress, in review, blocked, or
  waiting on you.
- **tmux workspaces.** Each dispatched agent runs in its own tmux session
  with prepared git worktrees. Attach at any time and steer.

## How it works

Six nouns cover the model:

| Noun | Meaning |
|---|---|
| **Ticket** | One Markdown file: status, priority, `blocked_by` edges, pull requests, claim. |
| **Project** | A directory of tickets plus an optional `plan.md` design document. |
| **Claimant** | Who holds a ticket (`--as NAME`); one worker per ticket, enforced by git push. |
| **Template** | A reusable YAML spec: repositories, tmux layout, dispatch defaults. |
| **Workspace** | One change-focused environment: git worktrees + a tmux session. |
| **Agent Session** | One coding-agent run inside a workspace. |

A ticket moves through a small state machine: `ready` (unclaimed, unblocked)
→ claimed (`in-progress`) → `in-review` (pull requests attached) → `done`
(closing a ticket unblocks its dependents). An agent that needs a decision
parks its ticket on `needs-input` and waits for your answer. A ticket with a
`## Plan` section also carries an approval gate: it does not dispatch for
implementation until you run `twt tickets approve`.

## Quick start

Requires Go, git, and tmux.

```sh
go install github.com/jpugliesi/tmux-worktree/cmd/twt@latest
```

1. Define a template for the repository you work on:

   ```sh
   twt templates create myproject
   twt templates repos add myproject app git@github.com:you/app.git
   ```

2. Create the tickets home and a project:

   ```sh
   # ~/.config/twt/config.yaml
   # home: /path/to/your/twt-home   (tickets/ and templates/ live inside;
   #                                 an Obsidian vault folder works)
   twt tickets init
   twt projects create myfeature --template myproject
   twt projects plan init myfeature
   ```

3. Write the plan, then break it into tickets with dependency edges:

   ```sh
   printf '%s' "$PLAN" | twt projects plan edit myfeature --stdin
   twt tickets create "Add the API" --project myfeature --status ready-for-agent
   twt tickets create "Add the UI" --project myfeature --status ready-for-agent \
     --blocked-by add-the-api
   ```

4. Dispatch and watch:

   ```sh
   twt tickets dispatch add-the-api        # claims the ticket, starts an agent in tmux
   twt tickets tree --project myfeature    # the dependency DAG with PR badges
twt projects show myfeature             # the board
twt projects close myfeature            # close the Project when work ends
   ```

## The workflow

**Your loop.** You define projects, iterate the plan, and make decisions.
Everything that needs you appears on the board under WAITING ON YOU:

```sh
twt projects show myfeature
printf '%s' "Use OAuth." | twt tickets answer some-ticket --stdin   # answer an agent's question
twt tickets approve some-ticket                                     # approve a plan for implementation
twt tickets close some-ticket                                       # merged means done; unblocks dependents
```

**The agent loop.** `twt tickets dispatch TICKET --plan` starts a planning
agent: it explores the code, writes a plan into the ticket
(`twt tickets plan`), asks you questions (`twt tickets ask`), and requests
approval. Plain `twt tickets dispatch TICKET` starts an implementation
agent: it implements the ticket, attaches each pull request as soon as it
exists (`twt tickets pr add`), and finishes with `twt tickets complete`,
which records the pull requests and hands the ticket to you for review.

**The coordinator wave.** A resident agent session (or a cron, or you) runs
one wave and stops:

```sh
twt tickets sync --project myfeature --output json   # store + session reconcile
twt projects show myfeature --output json            # the single coordinator read
# close tickets whose PRs are all merged, surface questions, then:
twt tickets dispatch NEXT_READY_TICKET
```

**Stacked pull requests.** With `stacking: true` under the template's
`local_dispatch`, a dependent ticket whose blocker is in review dispatches
from the blocker's branch and opens its pull request as a stack member.

## Workspaces and tmux

The workspace layer also works on its own, without tickets:

```sh
twt create fix-auth --template myproject   # worktrees + tmux session
twt next                                   # next workspace, archive the current one
twt switch                                 # jump between workspace sessions
twt done                                   # finish and return worktrees to the pool
```

Templates can declare tmux window layouts, initialization commands, and
agent sessions to start. `twt templates prepare` keeps a warm pool of
initialized worktrees. `twt done` keeps Workspace state and branches. It
returns the physical worktrees for fast reuse. `twt agents
list/send/resume` control the coding agents inside a workspace; dispatch
uses the same machinery. Providers: `codex`, `claude`, `cursor`, `grok`.

## Drive it from any client (chat bots included)

Any machine with a terminal is a full twt client: install the binary, clone
the tickets repository, point `ticketsHome` at the clone, and set
`ticketsSync.mode: git`. Git is the wire protocol; the push arbitrates
claims. This is how a hosted bot (for example Grok Bot on its own VM) drives
the workflow. Two patterns work well:

**Concierge.** The bot syncs, reads the boards, and surfaces exactly what
needs you — plans awaiting approval, open questions, tickets whose pull
requests are all merged — together with the command to run. On your word it
executes the store-side writes itself (`answer`, `approve`, `close`,
`plan edit`). Dispatch and tmux actions happen on your workstation, where a
resident coordinator session reacts to the store changes; the bot never
needs access to that machine.

**Bring your own executor.** The bot pulls ready work
(`twt tickets queue --project X --output json`), claims it
(`twt tickets claim TICKET --as bot-id`), runs the work with agents it can
create natively — for example cloud coding agents, with the worker contract
in their prompt — and reports back with `twt tickets pr add` and
`twt tickets complete`. twt is the shared ledger; the executor is whatever
the client has.

## For agents and scripts

twt is built agent-first:

- `twt skills install` writes the agent skill (the full operating contract)
  into `~/.claude/skills`, `~/.cursor/skills`, and `~/.agents/skills`.
- `twt schema` prints the machine-readable contract: every command,
  argument, flag, `apply` operation, error code, and exit code.
- Output is JSON whenever stdout is not a terminal; `--output ndjson`
  streams long lists; `--fields` and `--limit` keep reads small.
- Every mutation supports `--dry-run` (validate, change nothing), and
  `twt apply --stdin` accepts typed JSON payloads for every non-interactive
  mutation — no flag translation.
- Structured errors with stable codes and hints; exit codes 0/1/2/3.

See [docs/agent-dx.md](docs/agent-dx.md) for the design scorecard,
[docs/security.md](docs/security.md) for the threat model, and
[docs/twt.md](docs/twt.md) for the full reference.

## Git setup for the tickets home

The multi-machine workflow needs one git remote that every client can push
to:

1. **Create a remote.** Any git host works: a private GitHub repository, a
   repo on your own forge, or a bare repository on a server you can SSH to
   (`git init --bare tickets.git`).
2. **Clone it on each machine** and point twt at the clone. The full layout
   is a twt home with `tickets/` and shared `templates/` inside, all synced
   over the one remote:

   ```yaml
   # ~/.config/twt/config.yaml
   home: /home/you/twt-home   # or TWT_HOME; tickets/ and templates/ inside
   ticketsSync:
     mode: git        # or TWT_TICKETS_SYNC=git
     remote: origin   # default
   ```

   With `home` set, workspace templates you create sync to every executor
   machine, while templates in `~/.config/twt/templates/` stay machine-local
   and override shared ones by name. A tickets-only client can instead set
   just `ticketsHome: /path/to/tickets`.

3. **Use non-interactive credentials** (an SSH key or a git credential
   helper). Claims happen inside twt commands, so the push must not prompt.

The semantics are simple: every ticket write commits and pushes. Claim-class
writes (claim, complete, close) require the push — the push is the
compare-and-swap, and a lost race returns a `locked` error, so the caller
just picks other work. Other writes push best-effort and self-heal on the
next successful sync. Reads never touch the network; run
`twt tickets sync` (or pass `--fresh` to list/tree/show) to pull first when
freshness matters.

## License

MIT
