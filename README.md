# tmux-worktree

A small tool for managing git worktrees with dedicated tmux sessions.

Each worktree lives at `$TMUX_WORKTREE_DIR/<name>` and is backed by a shared
bare repo at `$TMUX_WORKTREE_DIR/.<repo>.git`. Each worktree gets its own tmux
session, laid out however you want.

## Install

### Manual

```sh
git clone https://github.com/jpugliesi/tmux-worktree ~/.tmux-worktree
echo 'export PATH="$HOME/.tmux-worktree/bin:$PATH"' >> ~/.zshrc
```

Update later with `git -C ~/.tmux-worktree pull`.

## Quick start

No config needed — works out of the box:

```sh
twt create git@github.com:org/repo.git feature-xyz
```

This clones the repo (as a bare repo under `$TMUX_WORKTREE_DIR`), creates a
worktree on a new `feature-xyz` branch, and opens a tmux session. By default
the session has one window with one pane — customize via config (below).

Other commands:

```sh
twt start ticket-123    # create a task branch + rename the current session
twt reset               # reset panes + hard-reset branch to origin default (silent)
twt reset -v            # same, but print per-step progress and git output
twt shared enable       # (run inside a bare repo) enable symlinked shared files
```

## Using tmux + twt with coding agents

I start tmux, then create one worktree and session per agent:

```sh
tmux
twt create --with-shared https://github.com/jpugliesi/my-repo my-repo-0
twt create https://github.com/jpugliesi/my-repo my-repo-1
twt create --with-shared https://github.com/jpugliesi/another-repo another-repo-0
```

Each repository is cloned once. Each command creates a branch, worktree, and
tmux session with the supplied name, then switches to it. I run one agent and
task per session while tmux keeps everything alive. `--with-shared` enables
shared project files before the first worktree is checked out.

Once I pick a ticket, I create its branch and rename the current session:

```sh
twt start home-bug
```

This runs `git switch -c` and renames the session from its stable slot name,
such as `core-4`, to `core-4-home-bug`. `twt` remembers the slot name so later
branch changes do not stack session names.

![tmux sessions named by repository slot and task](docs/images/tmux-sessions.png)

My `on_session_create` hook creates three panes in each session. I use them
for:

1. Neovim
2. A shell
3. A coding agent TUI

![Neovim, shell, and coding agent TUI](docs/images/tmux-session.png)

```text
/Users/jpugliesi/code/firetiger/.core.git/shared/.lazy.lua
```

That shared file customizes LazyVim for the Firetiger project and is symlinked
into all of its worktrees. See [Shared files](#shared-files-optional).

When the task is done and its work is pushed, I reset the current workspace:

```sh
twt reset
```

This restores the stable branch and session name, such as `core-4`, respawns
the other panes, and hard-resets tracked files to the origin default branch.
It does not remove untracked files.

I use [tmux-resurrect](https://github.com/tmux-plugins/tmux-resurrect) to save
and restore the sessions, windows, and pane layouts across tmux server
restarts.

## Agent skill

Install the `twt` skill for Claude Code, Codex, and other compatible agents:

```sh
npx skills add jpugliesi/tmux-worktree --skill twt
```

## Configure (optional)

Only needed if you want a custom tmux layout or a different data directory.

```sh
mkdir -p ~/.config/tmux-worktree
curl -fsSL https://raw.githubusercontent.com/jpugliesi/tmux-worktree/main/share/config.example.sh \
  > ~/.config/tmux-worktree/config.sh
```

The example ships a three-pane work window. Edit to taste.

Available knobs:

| Variable / hook | Purpose | Default |
|---|---|---|
| `TMUX_WORKTREE_DIR` | Where bare repos and worktrees live | `${XDG_DATA_HOME:-$HOME/.local/share}/tmux-worktree` |
| `on_session_create session path` | Build the tmux layout | One window, one pane |
| `on_worktree_create path` | Run after worktree is created | No-op |

## Shared files (optional)

Each bare repo can opt into a symlink-based "shared files" mechanism: any file
under `<bare>.git/shared/` is symlinked into each worktree's matching path when
the worktree is checked out. Existing non-symlink files in the worktree are
never overwritten.

Useful for:

- `.env.local`, secrets, credentials
- Editor project config (e.g. LazyVim `.lazy.lua`)
- `.rgignore`, `.claude/settings.local.json`, etc.

Enable while creating the first worktree for a repo:

```sh
twt create --with-shared git@github.com:org/repo.git repo-0
```

Or enable it later from the bare repo:

```sh
cd "$TMUX_WORKTREE_DIR/.repo.git"
twt shared enable
```

Then drop files into `shared/`. Optionally `cd shared && git init` to version
them in their own (separate) repo.

Disable with `twt shared disable`.

## License

MIT
