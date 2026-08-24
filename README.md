# tmux-worktree

`twt` manages coding-agent work with tmux. One **Workspace** is one unit of work.
Each Workspace has its own git worktrees, its own tmux session with your pane
layout, and its own Agent Sessions. Prepared Environments make Workspace
creation take seconds. A Markdown ticket tracker (`twt tickets`) holds the
backlog that you and your agents pick from. Every command speaks JSON,
dry-runs, and stable error codes, so agents drive `twt` as well as you do.

A **Workspace Template** is the YAML recipe for that kind of Workspace. A
**Prepared Environment** is a warm, initialized worktree set that a Workspace
claims. An **Agent Session** is one coding-agent run that belongs to a
Workspace. A **Ticket** is one Markdown note in **Tickets home**. A **Project**
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
twt tickets create "Fix auth token refresh" --project core --status ready-for-agent
```

A claim is compare-and-set. When two agents race for one Ticket, the second
gets `locked` and the name of the holder. Agents claim with `--as NAME`.

### 2. Create a Workspace or move to the next Workspace

Create a Workspace and keep all other Workspaces active:

```sh
twt create fix-auth --template product
```

From a current Workspace, pick one or more open Tickets from one Project and
move to the next Workspace:

```sh
twt next
```

Ticket slugs claim those Tickets, create one Workspace, link the records, and
append a start comment to each Ticket.

```sh
twt next fix-auth-tokens
twt next fix-auth-tokens add-auth-tests
```

Both paths claim a Prepared Environment and create a branch. They build the
tmux session with your pane layout and start any Agent Sessions the
template declares. Then they switch your tmux client to the new session.
A warm start to a working session takes about six seconds. The replacement
environment prepares itself in the background.

Run `twt next` with no name to pick an open Ticket. Run it inside the tmux
session of the current Workspace. It archives that Workspace after the switch.
Use `twt create` when there is no current Workspace or when another Workspace
must stay active.

### 3. Work

Move between live Workspaces with the picker. `twt` uses `fzf` when `fzf` is
installed. Without `fzf`, `twt` shows a numbered list:

```sh
twt switch          # name, template, status, age
twt switch fix-api  # skip the picker
```

Pick an Agent Session the same way. The list is newest first. Each text line
is provider, ID, and age:

```sh
twt agents ls
twt agents open            # fzf preview is the transcript. Enter resumes in this pane.
twt agents open AGENT_ID   # skip the picker
```

The preview shows the same markdown as `twt agents transcript show`. Preview
of a discovered session does not register it. A selection registers it,
then starts the provider resume command in this pane.

Attach coding agents that already ran in the Workspace directories.
Registration infers the provider and session ID from the resume command.
`twt agents ls` also shows discovered Codex, Claude, and Grok sessions.
The first action on a discovered session registers it:

```sh
twt agents register -- codex resume SESSION_ID
twt agents discover --adopt
twt agents resume AGENT_ID
```

In Neovim, [twt.nvim](nvim/twt.nvim/README.md) picks an Agent Session with
`<leader>arp` and opens its transcript. Collect Review Notes from any regular
file buffer with `<leader>an`. Use `<leader>arr` to select a tmux pane or the
clipboard. Use `<leader>ara` to send through the selected Agent Session. Log
progress on the Ticket as you go:

```sh
echo "Root cause found in token refresh path." | twt tickets comment fix-auth-tokens --stdin
```

Already have a tmux session that you made by hand? Adopt it. Removal of an
adopted Workspace deletes only the twt state. It never deletes the
directories:

```sh
twt workspaces adopt my-session --name fix-auth
```

### 4. Finish

Close the Ticket, then remove the Workspace:

```sh
twt tickets close fix-auth-tokens
twt done
```

`close` sets the status to `done` and drops the claim in one write. `done`
archives the Workspace, verifies that nothing unpushed gets lost, and removes
the worktrees, branch, and records. When you run it from inside the
Workspace's own session, it moves your tmux client to another Workspace first.
A branch with unpushed commits blocks removal and prints the escape
commands. A branch with no new commits removes instantly, even offline.

When a Workspace has one open Ticket, `twt done` asks whether to close it.
When it has many open Tickets, `twt done` keeps them open and prints one close
command for each Ticket. Use `twt tickets set --status` and
`twt tickets unclaim` when you need only one of those two changes.

Not done yet, just pausing? `twt archive` stops the session and keeps
everything. `twt workspaces open NAME` brings it back, layout and all.

### Housekeeping

```sh
twt config                            # resolved settings and their sources
twt context                           # Workspace and repository for this directory
twt storage show                      # active vs archived bytes
twt environments list                 # the warm pool, with sizes and ages
twt workspaces remove --all-archived --older-than 14d --apply
twt storage clean --apply             # failed environments, orphan records
twt doctor                            # end-to-end health check
```

## Command map

`twt --help` groups the same commands. `twt schema` is the live contract.

**Workflows**

| Command | Job |
| --- | --- |
| `twt tickets` | Create, list, claim, start, comment, and close Markdown Tickets |
| `twt projects` | Create, list, and show durable Ticket Projects |
| `twt create` | Create a Workspace and keep other Workspaces active |
| `twt next` | Pick Tickets or a name, create a Workspace, switch, and archive the current Workspace |
| `twt tickets start` | Claim one or more Tickets and start one Workspace |
| `twt switch` | Move the tmux client to a Workspace |
| `twt agents` | List, open, resume, send, and read Agent Sessions |
| `twt archive` | Stop a Workspace session and keep its data |
| `twt done` | Archive a Workspace and remove its data |
| `twt workspaces` (`twt w`) | Create, open, adopt, archive, and remove Workspaces |
| `twt templates` | Create and edit Workspace Templates. Prepare Environments |

**Inspect and maintain**

| Command | Job |
| --- | --- |
| `twt config` | Show every resolved setting and its source |
| `twt context` | Show the Workspace and repository for a directory or pane |
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
`twt agents ls`, and `twt workspaces ls`.

These commands are interactive. They have no apply operation. They refuse
`--output json`:

- `twt next`
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

Shell completion covers commands, template names, Workspace names, ticket
slugs, and Agent Session IDs:

```sh
# After `compinit` in ~/.zshrc. `compdef` exists only then.
eval "$(twt completion zsh)"
```

Or write a file on `fpath` before `compinit`:

```sh
mkdir -p ~/.local/share/zsh/site-functions
twt completion zsh > ~/.local/share/zsh/site-functions/_twt
```

Use `twt completion bash`, `twt completion fish`, or
`twt completion powershell` for the other shells.

### Define a Workspace Template

A template declares what every Workspace of its kind gets: repositories,
initialization, your tmux pane layout, and default agents.

```sh
twt templates create product
twt templates repos add product api git@github.com:acme/api.git --depth 1
twt templates repos init set product api -- sh -c './init.sh && direnv allow .'
```

Or write `~/.config/twt/templates/product.yaml` directly. twt validates the
file on every load. A `session` command runs each time twt creates the
Workspace's session. This example builds a three-pane layout (editor, shell
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
Use Workspaces and Workspace Templates for new work.

## License

MIT
