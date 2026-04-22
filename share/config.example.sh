# Example config for tmux-worktree.
# Copy to ~/.config/tmux-worktree/config.sh and edit.

# Base directory where bare repos and worktrees live.
# Bare repos are stored at: $TMUX_WORKTREE_DIR/.<repo>.git
# Worktrees are created at: $TMUX_WORKTREE_DIR/<name>
TMUX_WORKTREE_DIR="$HOME/code"

# Build the tmux session when `tmux-worktree create` is invoked.
# Arguments: $1=session name, $2=worktree path.
#
# Default (if unset) is a single window with a single pane.
# The example below reproduces a 3-pane editor window plus a 'deploy' window.
on_session_create() {
  local session="$1" path="$2"

  tmux new-session -d -s "$session" -n "$session" -c "$path"

  # Window 1: left column (editor top + shell bottom) + right column (wide)
  tmux split-window -h -p 34 -t "$session":1   -c "$path"
  tmux split-window -v -p 25 -t "$session":1.1 -c "$path"

  # Window 2: single pane for dev servers, docker compose, etc.
  tmux new-window -t "$session" -n deploy -c "$path"

  # Focus editor pane of window 1
  tmux select-window -t "$session":1
  tmux select-pane  -t "$session":1.1
}

# Optional: run after the worktree is created, before the session starts.
# Useful for bootstrapping deps, copying env files, etc.
#
# on_worktree_create() {
#   local path="$1"
#   (cd "$path" && make deps)
# }
