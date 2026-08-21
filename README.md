# tmux-worktree

`twt` manages agentic development with tmux. One **Project** = one unit of work:
its own git worktrees, its own tmux session with your pane layout, and its own
coding-agent sessions. Prepared Environments make Project creation take
seconds. A Markdown ticket tracker (`twt tickets`) holds the backlog your
agents pick from. Everything speaks JSON, dry-runs, and stable error codes, so
agents drive `twt` as well as you do.

## The daily loop

This is the workflow the tool exists for. Every step is one short command.

### 1. Pick work

Your backlog lives as Markdown tickets in your Obsidian vault. List what is
ready — unblocked, unclaimed, `ready-for-agent` — and claim one:

```sh
twt tickets list --ready
twt tickets claim fix-auth-tokens
```

File new work at any time, from anywhere:

```sh
twt tickets create "Fix auth token refresh" --board core --status ready-for-agent
```

A claim is compare-and-set: when two agents race for one ticket, the second
gets `locked` and the name of the holder. Agents claim with `--as NAME`.

### 2. Start a Project

```sh
twt start fix-auth-tokens
```

This claims a Prepared Environment — worktrees already cloned and
initialized — creates a branch, builds the tmux session with your declared
pane layout, starts any Agent Sessions the template declares, and switches
your tmux client to it. Warm start to working session: about six seconds.
The replacement environment prepares itself in the background.

Run `twt start` with no name to get a prompt. Run it from anywhere — inside
another Project it archives that Project after the switch; outside one it uses
your last template.

To claim a Ticket and start its Project in one step, run
`twt tickets start fix-auth-tokens`. It links the Project to the Ticket, and
`twt done` then offers to close that Ticket.

### 3. Work

Move between live Projects with the picker:

```sh
twt switch          # fzf-style picker: name, template, status, age
twt switch fix-api  # or go direct
```

Attach coding agents to the Project. Registration infers the provider and
session ID from the resume command, and `discover` finds sessions that already
ran in the Project's directories:

```sh
twt agents register -- codex resume SESSION_ID
twt agents discover --adopt
twt agents list
```

In Neovim, [twt.nvim](nvim/twt.nvim/README.md) picks an Agent Session with
`<leader>arp`, opens its transcript, collects review notes with `<leader>an`,
and sends the batch to the agent's pane with `<leader>arr`. Log progress on
the ticket as you go:

```sh
echo "Root cause found in token refresh path." | twt tickets comment fix-auth-tokens --stdin
```

### 4. Finish

```sh
twt done
```

One command: archives the Project, verifies nothing unpushed gets lost, and
removes the worktrees, branch, and records — reclaiming gigabytes. When you
run it from inside the Project's own session, it moves your tmux client to
another Project first. A branch with unpushed commits blocks removal with the
exact escape commands; a branch with no new commits removes instantly, even
offline. Close the ticket:

```sh
twt tickets set fix-auth-tokens --status done
twt tickets unclaim fix-auth-tokens
```

Not done yet, just pausing? `twt archive` stops the session and keeps
everything; `twt projects open NAME` brings it back, layout and all.

### Housekeeping

```sh
twt storage show                      # active vs archived bytes
twt environments list                 # the warm pool, with sizes and ages
twt projects remove --all-archived --older-than 14d --apply
twt storage clean --apply             # failed environments, orphan records
twt doctor                            # end-to-end health check
```

## One-time setup

### Install

Needs Go 1.23+, git, and tmux:

```sh
git clone https://github.com/jpugliesi/tmux-worktree ~/.tmux-worktree
cd ~/.tmux-worktree && go install ./cmd/twt
echo 'export PATH="$HOME/.tmux-worktree/bin:$PATH"' >> ~/.zshrc
```

Shell completion covers commands, template names, Project names, ticket
slugs, and Agent Session IDs:

```sh
twt completion zsh > "${fpath[1]}/_twt"
```

### Define a Project Template

A template declares what every Project of its kind gets: repositories,
initialization, your tmux pane layout, and default agents.

```sh
twt templates create product
twt templates repos add product api git@github.com:acme/api.git --depth 1
twt templates repos init set product api -- sh -c './init.sh && direnv allow .'
```

Or write `~/.config/twt/templates/product.yaml` directly (validated on every
load). A `session` command runs each time twt creates the Project's session —
this example builds a three-pane layout (editor, shell below, agent column):

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

Warm the pool once; twt keeps it warm after every claim:

```sh
twt templates prepare product
```

### Configure tickets

Point twt at the directory in your Obsidian vault that holds tickets:

```sh
echo 'ticketsHome: ~/Vaults/yourvault/tickets' > ~/.config/twt/config.yaml
twt tickets init
```

`init` scaffolds the vault hub with Bases views (Recent, Ready, Blocked,
Claimed) and a ticket template. It never overwrites existing notes, and
existing ticket files with extra frontmatter keep working — mutations preserve
unknown fields byte-for-byte.

### Give your agents the skill

```sh
twt skills install
```

This writes the version-stamped `twt` skill into the Cursor, Claude Code, and
Codex skill trees, so agents know to use JSON output, dry-runs, `--fields`,
the ready queue, and claims. Run it again after upgrades — `twt doctor` warns
when an installed copy is stale.

## For agents and scripts

Every command takes `--output json` (the default when output is piped),
`--dry-run` on every mutation, `--fields` and `--limit`/`--offset` on reads,
and `ndjson` for streaming lists. `twt schema` describes the whole surface at
runtime, including 25 typed `twt apply --stdin` operations and the stable
error and exit codes. Transcript text is sanitized and marked untrusted.
`twt` treats its caller as an untrusted operator — see
[Security posture](docs/security.md) and the
[Agent DX score](docs/agent-dx.md) (20/21).

The full reference — YAML shapes, JSON contract, retry and safety semantics —
is in the [twt guide](docs/twt.md).

## Legacy bash CLI

The original bash CLI is retired and installed as `twt-legacy`. Its
configuration stays at `~/.config/tmux-worktree/config.sh` and its data at
`$TMUX_WORKTREE_DIR`; `tests/start-reset.sh` still exercises it. Run its old
commands (`create`, `start`, `rename`, `reset`, `shared`) as `twt-legacy`.
Use Projects and Project Templates for new work.

## License

MIT
