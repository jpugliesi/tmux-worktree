# tmux-worktree

`twt` manages coding-agent work with tmux. One **Project** is one unit of work.
Each Project has its own git worktrees, its own tmux session with your pane
layout, and its own Agent Sessions. Prepared Environments make Project
creation take seconds. A Markdown ticket tracker (`twt tickets`) holds the
backlog that you and your agents pick from. Every command speaks JSON,
dry-runs, and stable error codes, so agents drive `twt` as well as you do.

A **Project Template** is the YAML recipe for that kind of Project. A
**Prepared Environment** is a warm, initialized worktree set that a Project
claims. An **Agent Session** is one coding-agent run that belongs to a
Project. A **Ticket** is one Markdown note in **Tickets home**. A **Board**
is a directory that groups Tickets.

## The daily loop

This is the workflow the tool exists for. Every step is one short command.

### 1. Pick work

Your backlog lives as Markdown tickets in your Obsidian vault. List what is
ready. A ready Ticket is `ready-for-agent`, unclaimed, and has no open
blockers:

```sh
twt tickets ls --ready
twt tickets claim fix-auth-tokens
```

File new work at any time, from anywhere:

```sh
twt tickets create "Fix auth token refresh" --board core --status ready-for-agent
```

A claim is compare-and-set. When two agents race for one Ticket, the second
gets `locked` and the name of the holder. Agents claim with `--as NAME`.

### 2. Start a Project

Claim a Ticket and start its Project in one step:

```sh
twt tickets start fix-auth-tokens
```

This claims the Ticket, creates the Project, links the two, and appends a
start comment. `twt done` then offers to close that Ticket.

Or claim first, then start by name:

```sh
twt start fix-auth-tokens
```

Both paths claim a Prepared Environment and create a branch. They build the
tmux session with your pane layout and start any Agent Sessions the
template declares. Then they switch your tmux client to the new session.
A warm start to a working session takes about six seconds. The replacement
environment prepares itself in the background.

Run `twt start` with no name to get a prompt. Run it from anywhere. Inside
another Project it archives that Project after the switch. Outside one it
uses your last template.

### 3. Work

Move between live Projects with the picker. `twt` uses `fzf` when `fzf` is
installed. Without `fzf`, `twt` shows a numbered list:

```sh
twt switch          # name, template, status, age
twt switch fix-api  # skip the picker
```

Pick an Agent Session the same way. The list is newest first. Each text line
is provider, ID, and age:

```sh
twt agents ls
twt agents open            # fzf preview is the transcript. Enter resumes.
twt agents open AGENT_ID   # skip the picker
```

The preview shows the same markdown as `twt agents transcript show`. Preview
of a discovered session does not register it. A selection registers it,
then resumes.

Attach coding agents that already ran in the Project directories.
Registration infers the provider and session ID from the resume command.
`twt agents ls` also shows discovered Codex, Claude, and Grok sessions.
The first action on a discovered session registers it:

```sh
twt agents register -- codex resume SESSION_ID
twt agents discover --adopt
twt agents resume AGENT_ID
```

In Neovim, [twt.nvim](nvim/twt.nvim/README.md) picks an Agent Session with
`<leader>arp` and opens its transcript. Collect review notes with
`<leader>an`. Send the batch to the agent's pane with `<leader>arr`. Log
progress on the Ticket as you go:

```sh
echo "Root cause found in token refresh path." | twt tickets comment fix-auth-tokens --stdin
```

Already have a tmux session that you made by hand? Adopt it. Removal of an
adopted Project deletes only the twt state. It never deletes the
directories:

```sh
twt projects adopt my-session --name fix-auth
```

### 4. Finish

Close the Ticket, then remove the Project:

```sh
twt tickets close fix-auth-tokens
twt done
```

`close` sets the status to `done` and drops the claim in one write. `done`
archives the Project, verifies that nothing unpushed gets lost, and removes
the worktrees, branch, and records. When you run it from inside the
Project's own session, it moves your tmux client to another Project first.
A branch with unpushed commits blocks removal and prints the escape
commands. A branch with no new commits removes instantly, even offline.

When you started with `twt tickets start`, `twt done` asks whether to close
that Ticket. Use `twt tickets set --status` and `twt tickets unclaim` when
you need only one of those two changes.

Not done yet, just pausing? `twt archive` stops the session and keeps
everything. `twt projects open NAME` brings it back, layout and all.

### Housekeeping

```sh
twt config                            # resolved settings and their sources
twt context                           # Project and repository for this directory
twt storage show                      # active vs archived bytes
twt environments list                 # the warm pool, with sizes and ages
twt projects remove --all-archived --older-than 14d --apply
twt storage clean --apply             # failed environments, orphan records
twt doctor                            # end-to-end health check
```

## Command map

`twt --help` groups the same commands. `twt schema` is the live contract.

**Workflows**

| Command | Job |
| --- | --- |
| `twt tickets` | Create, list, claim, start, comment, and close Markdown Tickets |
| `twt start` | Create a Project and switch to it |
| `twt tickets start` | Claim a Ticket and start its Project |
| `twt switch` | Move the tmux client to a Project |
| `twt agents` | List, open, resume, send, and read Agent Sessions |
| `twt archive` | Stop a Project session and keep its data |
| `twt done` | Archive a Project and remove its data |
| `twt projects` | Create, open, adopt, archive, and remove Projects |
| `twt templates` | Create and edit Project Templates. Prepare Environments |

**Inspect and maintain**

| Command | Job |
| --- | --- |
| `twt config` | Show every resolved setting and its source |
| `twt context` | Show the Project and repository for a directory or pane |
| `twt environments` | Inspect the Prepared Environment pool |
| `twt storage` | Show disk use. Clean twt-owned leftovers |
| `twt doctor` | Check tools, templates, state, and skill copies |

**Automation**

| Command | Job |
| --- | --- |
| `twt schema` | Print the versioned command, apply, and error contract |
| `twt apply --stdin` | Run one typed JSON mutation |
| `twt skills install` | Write the version-stamped agent skill |
| `twt completion` | Generate shell completion |

`list` commands accept `ls`. Common reads are `twt tickets ls`,
`twt agents ls`, and `twt projects ls`.

These commands are interactive. They have no apply operation. They refuse
`--output json`:

- `twt start`
- `twt tickets start`
- `twt tickets home`
- `twt switch`
- `twt done`
- the tmux client move of an archive
- `twt templates edit`
- `twt agents focus`
- `twt agents open`
- `twt agents register --pane current`

## One-time setup

### Install

The install needs Go 1.23+, git, and tmux. `fzf` is optional and improves
the pickers:

```sh
git clone https://github.com/jpugliesi/tmux-worktree
cd tmux-worktree
go install ./cmd/twt
```

`go install` writes `twt` to `$(go env GOPATH)/bin`, or to `$GOBIN` when that
is set. Put that directory on `PATH`:

```sh
echo 'export PATH="$(go env GOPATH)/bin:$PATH"' >> ~/.zshrc
```

To keep the binary next to this checkout instead:

```sh
GOBIN="$PWD/bin" go install ./cmd/twt
echo "export PATH=\"$PWD/bin:\$PATH\"" >> ~/.zshrc
```

Shell completion covers commands, template names, Project names, ticket
slugs, and Agent Session IDs:

```sh
twt completion zsh > "${fpath[1]}/_twt"
```

Use `twt completion bash`, `twt completion fish`, or
`twt completion powershell` for the other shells.

### Define a Project Template

A template declares what every Project of its kind gets: repositories,
initialization, your tmux pane layout, and default agents.

```sh
twt templates create product
twt templates repos add product api git@github.com:acme/api.git --depth 1
twt templates repos init set product api -- sh -c './init.sh && direnv allow .'
```

Or write `~/.config/twt/templates/product.yaml` directly. twt validates the
file on every load. A `session` command runs each time twt creates the
Project's session. This example builds a three-pane layout (editor, shell
below, agent column):

```yaml
session:
  command:
    - sh
    - -c
    - |
      w="$TWT_TMUX_WINDOW_API"
      tmux split-window -h -l 34% -t "$w" -c "$TWT_REPOSITORY_API"
      tmux split-window -v -l 25% -t "$w".1 -c "$TWT_REPOSITORY_API"
      tmux select-pane -t "$w".1
agents:
  - label: coder
    provider: claude
    start: [claude]
pool_depth: 1
```

Warm the pool once. twt keeps it warm after every claim:

```sh
twt templates prepare product
```

### Configure tickets

Point twt at the directory in your Obsidian vault that holds tickets:

```sh
echo 'ticketsHome: ~/Vaults/yourvault/tickets' > ~/.config/twt/config.yaml
twt tickets init
twt tickets home
```

`init` scaffolds the vault hub with Bases views (Recent, Ready, Blocked,
Claimed) and a ticket template. It never overwrites existing notes. Existing
ticket files with extra frontmatter keep working. Mutations preserve unknown
fields byte-for-byte.

### Give your agents the skill

```sh
twt skills install
```

This writes the version-stamped `twt` skill into the Cursor, Claude Code, and
Codex skill trees. Agents then use JSON output, dry-runs, `--fields`, the
ready queue, and claims. Run it again after upgrades. `twt doctor` warns when
an installed copy is stale.

## For agents and scripts

Every command takes `--output json`. That format is the default when output
is piped. Every mutation accepts `--dry-run`. Reads accept `--fields` and
`--limit` / `--offset`. List commands also accept `ndjson` for streaming.

`twt schema` describes the whole surface at runtime: commands, flags, apply
operations, and the stable error and exit codes. Transcript text is
sanitized and marked untrusted. `twt` treats its caller as an untrusted
operator. See [Security posture](docs/security.md) and the
[Agent DX score](docs/agent-dx.md) (20/21).

The full reference is in the [twt guide](docs/twt.md). That guide covers
YAML shapes, the JSON contract, and retry and safety semantics.

## Legacy bash CLI

The original bash CLI is retired and installed as `twt-legacy`. Its
configuration stays at `~/.config/tmux-worktree/config.sh` and its data at
`$TMUX_WORKTREE_DIR`. `tests/start-reset.sh` still exercises it. Run its old
commands (`create`, `start`, `rename`, `reset`, `shared`) as `twt-legacy`.
Use Projects and Project Templates for new work.

## License

MIT
