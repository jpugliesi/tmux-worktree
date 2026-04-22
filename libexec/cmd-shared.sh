# shellcheck shell=bash
# Sourced by bin/tmux-worktree — do not execute directly.

cmd_shared() {
  local sub="${1:-help}"
  shift || true
  case "$sub" in
    enable)  _tw_shared_enable "$@" ;;
    disable) _tw_shared_disable "$@" ;;
    help|-h|--help|"")
      cat <<EOF
Usage: tmux-worktree shared <subcommand>

Manage the optional shared-files mechanism: files placed under a bare repo's
shared/ directory are symlinked into each worktree on checkout. Existing
(non-symlink) files in the worktree are never overwritten.

Subcommands:
  enable    Install post-checkout hook in the current bare repo
  disable   Remove the post-checkout hook

Run from inside a bare git repository (e.g. cd \$TMUX_WORKTREE_DIR/.repo.git).
EOF
      ;;
    *) echo "Unknown subcommand: $sub" >&2; return 1 ;;
  esac
}

_tw_shared_bare() {
  if ! git rev-parse --is-bare-repository 2>/dev/null | grep -q true; then
    echo "Error: must be run from inside a bare git repository" >&2
    return 1
  fi
  local d
  d="$(git rev-parse --git-dir)"
  (cd "$d" && pwd)
}

_tw_shared_enable() {
  local bare
  bare="$(_tw_shared_bare)" || return 1
  mkdir -p "$bare/shared" "$bare/hooks"
  cp "$LIBEXEC/hooks/post-checkout.tmpl" "$bare/hooks/post-checkout"
  chmod +x "$bare/hooks/post-checkout"
  echo "Enabled shared-files mechanism in $bare"
  echo "Place files under $bare/shared/ — they'll be symlinked into worktrees on checkout."
  echo "Tip: 'cd $bare/shared && git init' to version your local-only files."
}

_tw_shared_disable() {
  local bare
  bare="$(_tw_shared_bare)" || return 1
  rm -f "$bare/hooks/post-checkout"
  echo "Removed $bare/hooks/post-checkout"
  echo "Note: existing symlinks in worktrees are not cleaned up."
}
