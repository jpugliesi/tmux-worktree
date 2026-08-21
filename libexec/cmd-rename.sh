# shellcheck shell=bash
# Sourced by bin/twt-legacy — do not execute directly.

_tw_rename_usage() {
  cat <<EOF
Usage: twt rename [--] <name>

Rename the current tmux session. This does not rename the Git branch or
worktree. A later twt start or twt reset command can replace this name.

Use -- before a name that starts with a hyphen.
EOF
}

cmd_rename() {
  case "${1:-}" in
    -h|--help)
      _tw_rename_usage
      return 0
      ;;
    --) shift ;;
    -*)
      echo "twt rename: unknown option: $1" >&2
      _tw_rename_usage >&2
      return 1
      ;;
  esac
  if [ $# -ne 1 ]; then
    _tw_rename_usage >&2
    return 1
  fi
  if [ -z "$1" ]; then
    echo "Error: session name cannot be empty" >&2
    return 1
  fi
  if [ -z "${TMUX:-}" ]; then
    echo "Error: twt rename must be run inside tmux" >&2
    return 1
  fi

  local session_id error
  session_id=$(tmux display-message -p '#{session_id}')
  _tw_session_base "$session_id" >/dev/null
  if ! error=$(tmux rename-session -t "$session_id" -- "$1" 2>&1); then
    printf 'twt rename: %s\n' "$error" >&2
    return 1
  fi
}
