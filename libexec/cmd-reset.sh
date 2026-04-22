# shellcheck shell=bash
# Sourced by bin/tmux-worktree — do not execute directly.

cmd_reset() {
  if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
    cat <<EOF
Usage: tmux-worktree reset [window]

Reset all panes in the current (or given) tmux window, switch git to the branch
matching the first window's name, and hard-reset it to origin's default branch.

Arguments:
  window   Optional window name or index (defaults to current window)
EOF
    return 0
  fi

  local window="${1:-}"

  local branch
  branch=$(tmux list-windows -F '#{window_index}:#{window_name}' | sort -n | head -1 | cut -d: -f2)
  echo "Renaming session to $branch"
  tmux rename-session "$branch"

  local panes current
  panes=$(tmux list-panes ${window:+-t "$window"} -F '#{pane_index}' | sort -n)
  current=$(tmux display-message -p '#{pane_index}')
  echo "Panes: $(echo $panes | tr '\n' ' ')(running in $current)"

  for idx in $panes; do
    if [ "$idx" = "$current" ]; then
      echo "  skip pane $idx (running this command)"
      continue
    fi
    echo "  respawn pane $idx"
    if [ -z "$window" ]; then
      tmux respawn-pane -k -t ".$idx"
    else
      tmux respawn-pane -k -t "${window}.$idx"
    fi
  done

  if ! git rev-parse --verify "$branch" >/dev/null 2>&1; then
    echo "Warning: branch '$branch' does not exist, skipping git reset"
    return 0
  fi

  local default
  if git rev-parse --verify origin/main >/dev/null 2>&1; then
    default=main
  elif git rev-parse --verify origin/master >/dev/null 2>&1; then
    default=master
  else
    echo "Error: no origin/main or origin/master" >&2
    return 1
  fi

  echo "Switching to $branch, reset --hard to origin/$default"
  git switch "$branch"
  git fetch origin "$default"
  git reset --hard "origin/$default"
}
