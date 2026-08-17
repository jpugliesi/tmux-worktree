# shellcheck shell=bash
# Sourced by bin/twt — do not execute directly.

_tw_create_usage() {
  cat <<EOF
Usage: twt create [--with-shared] [--shallow] [--depth <n>] [--remote <name>=<url>]... <url> <name>

Clone a bare repo (if not already present), add a worktree, and start a tmux
session. The session layout is defined by on_session_create() in your config.

Options:
  --with-shared         Enable shared files before the worktree is checked out
  --shallow             Shallow-clone the bare repo (depth 1; ignored if bare exists)
  --depth <n>           Shallow-clone with history depth n (implies --shallow)
  --remote <name>=<url> Add or update an extra git remote on the bare repo
                        (repeatable; primary <url> is always remote "origin")

Arguments:
  url    Git clone URL (e.g. git@github.com:org/repo.git)
  name   Worktree + session name (typically the branch name)

Paths:
  bare repo:  \$TMUX_WORKTREE_DIR/.<repo>.git
  worktree:   \$TMUX_WORKTREE_DIR/<name>
EOF
}

cmd_create() {
  local with_shared=false shallow=false depth=""
  local -a extra_remotes=()
  local remote_spec remote_name remote_url existing_url
  while [ $# -gt 0 ]; do
    case "$1" in
      --with-shared)
        with_shared=true
        shift
        ;;
      --shallow)
        shallow=true
        shift
        ;;
      --depth)
        if [ $# -lt 2 ] || ! [[ "$2" =~ ^[1-9][0-9]*$ ]]; then
          echo "twt create: --depth requires a positive integer" >&2
          _tw_create_usage >&2
          return 1
        fi
        shallow=true
        depth="$2"
        shift 2
        ;;
      --depth=*)
        depth="${1#--depth=}"
        if ! [[ "$depth" =~ ^[1-9][0-9]*$ ]]; then
          echo "twt create: --depth requires a positive integer" >&2
          _tw_create_usage >&2
          return 1
        fi
        shallow=true
        shift
        ;;
      --remote)
        if [ $# -lt 2 ] || [[ "$2" != *=* ]] || [ -z "${2%%=*}" ] || [ -z "${2#*=}" ]; then
          echo "twt create: --remote requires name=url" >&2
          _tw_create_usage >&2
          return 1
        fi
        if [ "${2%%=*}" = "origin" ]; then
          echo "twt create: use the primary <url> argument for origin; --remote cannot set origin" >&2
          return 1
        fi
        extra_remotes+=("$2")
        shift 2
        ;;
      --remote=*)
        remote_spec="${1#--remote=}"
        if [[ "$remote_spec" != *=* ]] || [ -z "${remote_spec%%=*}" ] || [ -z "${remote_spec#*=}" ]; then
          echo "twt create: --remote requires name=url" >&2
          _tw_create_usage >&2
          return 1
        fi
        if [ "${remote_spec%%=*}" = "origin" ]; then
          echo "twt create: use the primary <url> argument for origin; --remote cannot set origin" >&2
          return 1
        fi
        extra_remotes+=("$remote_spec")
        shift
        ;;
      -h|--help)
        _tw_create_usage
        return 0
        ;;
      --)
        shift
        break
        ;;
      -*)
        echo "twt create: unknown option: $1" >&2
        _tw_create_usage >&2
        return 1
        ;;
      *)
        break
        ;;
    esac
  done

  if [ $# -lt 2 ]; then
    _tw_create_usage
    return 0
  fi

  if $shallow && [ -z "$depth" ]; then
    depth=1
  fi

  local url="$1" name="$2"
  local basename="${url##*/}"
  local repo="${basename%.git}"

  local bare="$TMUX_WORKTREE_DIR/.${repo}.git"
  local wt="$TMUX_WORKTREE_DIR/$name"

  mkdir -p "$TMUX_WORKTREE_DIR"

  if [ ! -d "$bare" ]; then
    local clone_args=(--bare)
    if $shallow; then
      clone_args+=(--depth "$depth")
      echo "Cloning bare repo (shallow, depth $depth): $url"
    else
      echo "Cloning bare repo: $url"
    fi
    git clone "${clone_args[@]}" "$url" "$bare"
    if $shallow; then
      # Only track the default branch when shallow — fetching every head at
      # depth 1 still pulls tip commits for all remote branches.
      local default_branch
      default_branch="$(git -C "$bare" symbolic-ref --short HEAD 2>/dev/null || true)"
      if [ -z "$default_branch" ]; then
        if git -C "$bare" rev-parse --verify main >/dev/null 2>&1; then
          default_branch=main
        elif git -C "$bare" rev-parse --verify master >/dev/null 2>&1; then
          default_branch=master
        else
          echo "Error: could not determine default branch after shallow clone" >&2
          return 1
        fi
      fi
      git -C "$bare" config remote.origin.fetch \
        "+refs/heads/${default_branch}:refs/remotes/origin/${default_branch}"
      git -C "$bare" fetch --depth "$depth" origin
      git -C "$bare" symbolic-ref refs/remotes/origin/HEAD \
        "refs/remotes/origin/${default_branch}"
    else
      git -C "$bare" config remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*'
      git -C "$bare" fetch origin
    fi
  elif $shallow; then
    echo "Note: bare repo already exists at $bare; --shallow/--depth ignored" >&2
  fi

  for remote_spec in "${extra_remotes[@]+"${extra_remotes[@]}"}"; do
    remote_name="${remote_spec%%=*}"
    remote_url="${remote_spec#*=}"
    if existing_url="$(git -C "$bare" remote get-url "$remote_name" 2>/dev/null)"; then
      if [ "$existing_url" != "$remote_url" ]; then
        echo "Updating remote '$remote_name': $existing_url -> $remote_url"
        git -C "$bare" remote set-url "$remote_name" "$remote_url"
      fi
    else
      echo "Adding remote '$remote_name' -> $remote_url"
      git -C "$bare" remote add "$remote_name" "$remote_url"
    fi
  done

  if $with_shared; then
    _tw_shared_install "$bare"
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

  tmux set-environment -t "$name" TWT_BASE_SESSION "$name"

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
