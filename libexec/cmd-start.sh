# shellcheck shell=bash
# Sourced by bin/twt — do not execute directly.

_tw_start_usage() {
  cat <<EOF
Usage: twt start <branch> [start-point]

Create a task branch and rename the current tmux session from its stable
workspace name to <workspace>-<branch>.

Arguments:
  branch       New branch name
  start-point  Optional commit or branch to start from

Tmux represents periods in branch names as underscores in the session name.
EOF
}

cmd_start() {
  if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
    _tw_start_usage
    return 0
  fi
  if [ $# -lt 1 ] || [ $# -gt 2 ]; then
    _tw_start_usage >&2
    return 1
  fi
  if [ -z "${TMUX:-}" ]; then
    echo "Error: twt start must be run inside tmux" >&2
    return 1
  fi
  if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "Error: twt start must be run inside a git worktree" >&2
    return 1
  fi

  local branch="$1" start_point="${2:-}"
  if ! git check-ref-format --branch "$branch" >/dev/null 2>&1; then
    echo "Error: invalid branch name '$branch'" >&2
    return 1
  fi
  if git show-ref --verify --quiet "refs/heads/$branch"; then
    echo "Error: branch '$branch' already exists" >&2
    return 1
  fi
  if [ -n "$start_point" ] &&
    ! git rev-parse --verify "$start_point^{commit}" >/dev/null 2>&1; then
    echo "Error: invalid start point '$start_point'" >&2
    return 1
  fi

  local session_id base target
  session_id=$(tmux display-message -p '#{session_id}')
  base="$(_tw_session_base "$session_id")"
  target="$(_tw_task_session_name "$base" "$branch")"

  if tmux has-session -t "=$target" 2>/dev/null; then
    echo "Error: tmux session '$target' already exists" >&2
    return 1
  fi

  if [ -n "$start_point" ]; then
    git switch -c "$branch" "$start_point"
  else
    git switch -c "$branch"
  fi
  tmux rename-session -t "$session_id" "$target"
}
