# shellcheck shell=bash
# Sourced by bin/twt-legacy — do not execute directly.

_tw_session_environment_value() {
  local target="$1" name="$2" value
  value=$(tmux show-environment -t "$target" "$name" 2>/dev/null || true)
  case "$value" in
    "$name="*) printf '%s\n' "${value#*=}" ;;
  esac
}

_tw_session_base() {
  local target="$1" base candidate worktree_root

  base="$(_tw_session_environment_value "$target" TWT_BASE_SESSION)"
  if [ -z "$base" ]; then
    base="$(_tw_session_environment_value "$target" GCB_BASE_SESSION)"
  fi
  if [ -z "$base" ]; then
    candidate=$(tmux list-windows -t "$target" -F '#{window_index}:#{window_name}' 2>/dev/null |
      sort -n | head -1 | cut -d: -f2-)
    if git show-ref --verify --quiet "refs/heads/$candidate" 2>/dev/null; then
      base="$candidate"
    fi
  fi
  if [ -z "$base" ]; then
    worktree_root=$(git rev-parse --show-toplevel 2>/dev/null || true)
    candidate="${worktree_root##*/}"
    if [ -n "$candidate" ] && git show-ref --verify --quiet "refs/heads/$candidate"; then
      base="$candidate"
    fi
  fi
  if [ -z "$base" ]; then
    base=$(tmux display-message -t "$target" -p '#S')
  fi

  if [ -z "$base" ]; then
    echo "Error: could not determine the stable tmux session name" >&2
    return 1
  fi

  tmux set-environment -t "$target" TWT_BASE_SESSION "$base"
  printf '%s\n' "$base"
}

_tw_task_session_name() {
  local base="$1" branch="$2"
  printf '%s-%s\n' "$base" "${branch//./_}"
}
