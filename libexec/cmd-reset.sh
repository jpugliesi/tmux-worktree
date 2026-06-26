# shellcheck shell=bash
# Sourced by bin/twt — do not execute directly.

cmd_reset() {
  local verbose=false window=""
  while [ $# -gt 0 ]; do
    case "$1" in
      -h|--help)
        cat <<EOF
Usage: twt reset [-v|--verbose] [window]

Reset all tmux panes in the current (or given) tmux window, switch git to the branch
matching the first window's name, and hard-reset it to origin's default branch.

Runs silently on success. Warnings and errors are always shown.

Arguments:
  window           Optional window name or index (defaults to current window)

Options:
  -v, --verbose    Print per-step progress and the underlying git output
EOF
        return 0
        ;;
      -v|--verbose) verbose=true ;;
      --) shift; window="${1:-}"; break ;;
      -*) echo "twt reset: unknown option: $1" >&2; return 1 ;;
      *) window="$1" ;;
    esac
    shift
  done

  # say: print progress only in verbose mode (':' is a no-op that ignores its args)
  local say=:
  $verbose && say=echo

  local branch
  branch=$(tmux list-windows -F '#{window_index}:#{window_name}' | sort -n | head -1 | cut -d: -f2)
  $say "Renaming session to $branch"
  tmux rename-session "$branch"

  local panes current
  panes=$(tmux list-panes ${window:+-t "$window"} -F '#{pane_index}' | sort -n)
  current=$(tmux display-message -p '#{pane_index}')
  $say "Panes: $(echo $panes | tr '\n' ' ')(running in $current)"

  for idx in $panes; do
    if [ "$idx" = "$current" ]; then
      $say "  skip pane $idx (running this command)"
      continue
    fi
    $say "  respawn pane $idx"
    if [ -z "$window" ]; then
      tmux respawn-pane -k -t ".$idx"
    else
      tmux respawn-pane -k -t "${window}.$idx"
    fi
  done

  if ! git rev-parse --verify "$branch" >/dev/null 2>&1; then
    echo "Warning: branch '$branch' does not exist, skipping git reset" >&2
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

  $say "Switching to $branch, reset --hard to origin/$default"
  if $verbose; then
    git switch "$branch"
    git fetch origin "$default"
    git reset --hard "origin/$default"
  else
    # --quiet silences git's own messages, but git forwards the post-checkout
    # hook's stdout to its stderr — so the shared-files "Linked:" lines survive
    # a plain redirect. Capture the switch's output and only replay it (to
    # stderr) if the switch actually fails, so real errors are never hidden.
    local out
    if ! out=$(git switch --quiet "$branch" 2>&1 1>/dev/null); then
      [ -n "$out" ] && printf '%s\n' "$out" >&2
      return 1
    fi
    git fetch --quiet origin "$default"
    git reset --hard --quiet "origin/$default"
  fi
}
