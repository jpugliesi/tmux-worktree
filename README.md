# tmux-worktree

A small tool for managing git worktrees with dedicated tmux sessions.

Each worktree lives at `$TMUX_WORKTREE_DIR/<name>` and is backed by a shared
bare repo at `$TMUX_WORKTREE_DIR/.<repo>.git`. Each worktree gets its own tmux
session, laid out however you want.

## Install

```sh
git clone https://github.com/YOUR/tmux-worktree ~/.local/share/tmux-worktree
ln -sf ~/.local/share/tmux-worktree/bin/tmux-worktree ~/.local/bin/tmux-worktree
```

Make sure `~/.local/bin` is on your `PATH`.

## Configure

Create `~/.config/tmux-worktree/config.sh`. Start from the example:

```sh
mkdir -p ~/.config/tmux-worktree
cp ~/.local/share/tmux-worktree/share/config.example.sh \
   ~/.config/tmux-worktree/config.sh
```

Minimum config:

```sh
TMUX_WORKTREE_DIR="$HOME/code"

on_session_create() {
  local session="$1" path="$2"
  tmux new-session -d -s "$session" -c "$path"
}
```

See [`share/config.example.sh`](share/config.example.sh) for a richer layout.

## Usage

```sh
# Clone (if needed) + create worktree + start session
tmux-worktree create git@github.com:org/repo.git feature-xyz

# Reset current window's panes and hard-reset branch to origin's default
tmux-worktree reset

# Enable optional shared-files mechanism in a bare repo
cd "$TMUX_WORKTREE_DIR/.repo.git"
tmux-worktree shared enable
```

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

## Hooks

Two hooks, both optional:

| Hook | Arguments | When |
|---|---|---|
| `on_session_create` | `session` `path` | After the worktree exists, to build the tmux session |
| `on_worktree_create` | `path` | After a new worktree is created, before the session starts |

Define them in `config.sh`. Inside the hooks you can run any tmux/shell command.

## License

MIT
