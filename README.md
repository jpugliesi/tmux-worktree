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
tmux-worktree create git@github.com:org/repo.git feature-xyz
```

This clones the repo (as a bare repo under `$TMUX_WORKTREE_DIR`), creates a
worktree on a new `feature-xyz` branch, and opens a tmux session. By default
the session has one window with one pane — customize via config (below).

Other commands:

```sh
tmux-worktree reset               # reset panes + hard-reset branch to origin default
tmux-worktree shared enable       # (run inside a bare repo) enable symlinked shared files
```

## Configure (optional)

Only needed if you want a custom tmux layout or a different data directory.

```sh
mkdir -p ~/.config/tmux-worktree
curl -fsSL https://raw.githubusercontent.com/jpugliesi/tmux-worktree/main/share/config.example.sh \
  > ~/.config/tmux-worktree/config.sh
```

The example ships a 3-pane editor window plus a `deploy` window. Edit to taste.

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

Enable once per bare repo:

```sh
cd "$TMUX_WORKTREE_DIR/.repo.git"
tmux-worktree shared enable
```

Then drop files into `shared/`. Optionally `cd shared && git init` to version
them in their own (separate) repo.

Disable with `tmux-worktree shared disable`.

## License

MIT
