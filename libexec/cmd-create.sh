# shellcheck shell=bash
# Sourced by bin/tmux-worktree — do not execute directly.

cmd_create() {
  if [ $# -lt 2 ] || [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
    cat <<EOF
Usage: tmux-worktree create <url> <name>

Clone a bare repo (if not already present), add a worktree, and start a tmux
session. The session layout is defined by on_session_create() in your config.

Arguments:
  url    Git clone URL (e.g. git@github.com:org/repo.git)
  name   Worktree + session name (typically the branch name)

Paths:
  bare repo:  \$TMUX_WORKTREE_DIR/.<repo>.git
  worktree:   \$TMUX_WORKTREE_DIR/<name>
EOF
    return 0
  fi

  local url="$1" name="$2"
  local basename="${url##*/}"
  local repo="${basename%.git}"

  local bare="$TMUX_WORKTREE_DIR/.${repo}.git"
  local wt="$TMUX_WORKTREE_DIR/$name"

  mkdir -p "$TMUX_WORKTREE_DIR"

  if [ ! -d "$bare" ]; then
    echo "Cloning bare repo: $url"
    git clone --bare "$url" "$bare"
    git -C "$bare" config remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*'
    git -C "$bare" fetch origin
  fi

  if [ ! -d "$wt" ]; then
    _tw_add_worktree "$bare" "$wt" "$name"
    on_worktree_create "$wt"
  fi

  if tmux has-session -t "$name" 2>/dev/null; then
    echo "Session '$name' already exists — attaching"
  else
    echo "Creating tmux session '$name'"
    on_session_create "$name" "$wt"
    if ! tmux has-session -t "$name" 2>/dev/null; then
      echo "Error: on_session_create did not produce a session named '$name'" >&2
      return 1
    fi
  fi

  if [ -z "${TMUX:-}" ]; then
    exec tmux attach -t "$name"
  else
    tmux switch-client -t "$name"
  fi
}

_tw_add_worktree() {
  local bare="$1" wt="$2" name="$3"

  local default
  default=$(git -C "$bare" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@' || true)
  if [ -z "$default" ]; then
    if git -C "$bare" rev-parse --verify origin/main >/dev/null 2>&1; then
      default=main
    elif git -C "$bare" rev-parse --verify origin/master >/dev/null 2>&1; then
      default=master
    else
      echo "Error: could not determine default branch" >&2
      return 1
    fi
  fi

  if git -C "$bare" rev-parse --verify "$name" >/dev/null 2>&1; then
    echo "Using existing branch '$name'"
    git -C "$bare" worktree add "$wt" "$name"
  else
    echo "Creating branch '$name' from '$default'"
    git -C "$bare" worktree add -b "$name" "$wt" "$default"
    git -C "$wt" branch --set-upstream-to="origin/$default" "$name"
  fi
}
