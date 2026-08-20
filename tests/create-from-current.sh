#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

mkdir -p "$test_root/bin" "$test_root/home" "$test_root/source"

cat >"$test_root/bin/tmux" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  has-session)
    target="${3#=}"
    [ -f "$TWT_TEST_TMUX_STATE" ] &&
      grep -Fxq "$target" "$TWT_TEST_TMUX_STATE"
    ;;
  new-session)
    shift
    session=""
    while [ $# -gt 0 ]; do
      case "$1" in
        -s)
          session="$2"
          shift 2
          ;;
        *)
          shift
          ;;
      esac
    done
    [ -n "$session" ]
    printf '%s\n' "$session" >>"$TWT_TEST_TMUX_STATE"
    ;;
  switch-client | set-environment)
    ;;
  *)
    echo "Unexpected tmux command: ${1:-}" >&2
    exit 1
    ;;
esac
EOF
chmod +x "$test_root/bin/tmux"

git init -q -b main "$test_root/source"
git -C "$test_root/source" config user.name "tmux-worktree test"
git -C "$test_root/source" config user.email "test@example.com"
printf '%s\n' "test repository" >"$test_root/source/README.md"
git -C "$test_root/source" add README.md
git -C "$test_root/source" commit -qm "initial commit"

run_twt() {
  PATH="$test_root/bin:$PATH" \
    HOME="$test_root/home" \
    TMUX="test" \
    TMUX_WORKTREE_DIR="$test_root/worktrees" \
    TWT_TEST_TMUX_STATE="$test_root/tmux-sessions" \
    "$project_root/bin/twt-legacy" "$@"
}

help_output="$(run_twt create --help)"
case "$help_output" in
  *"no arguments"*"next numbered workspace"*) ;;
  *)
    echo "create help does not explain the no-argument mode" >&2
    exit 1
    ;;
esac

run_twt create "$test_root/source" source-0 >/dev/null

bare="$test_root/worktrees/.source.git"
git -C "$bare" config --unset-all remote.origin.fetch
git -C "$bare" config --add remote.origin.fetch \
  '+refs/heads/main:refs/remotes/origin/main'
git -C "$bare" remote add github https://github.com/example/source.git
remote_config_before="$(git -C "$bare" config --get-regexp '^remote\.')"

mkdir -p "$test_root/worktrees/source-0/nested/path"
create_output="$(
  printf 'y\n' |
    (cd "$test_root/worktrees/source-0/nested/path" && run_twt create)
)"

if [ ! -d "$test_root/worktrees/source-1" ]; then
  echo "create without arguments did not create source-1" >&2
  exit 1
fi
if ! grep -Fxq source-1 "$test_root/tmux-sessions"; then
  echo "create without arguments did not create the source-1 session" >&2
  exit 1
fi
case "$create_output" in
  *"source-0"*"source-1"*"[y/N]"*) ;;
  *)
    echo "create did not show the source, target, and confirmation prompt" >&2
    exit 1
    ;;
esac

remote_config_after="$(git -C "$bare" config --get-regexp '^remote\.')"
if [ "$remote_config_after" != "$remote_config_before" ]; then
  echo "create changed the shared remote configuration" >&2
  exit 1
fi

decline_output="$(
  printf 'n\n' |
    (cd "$test_root/worktrees/source-1" && run_twt create)
)"
if [ -e "$test_root/worktrees/source-2" ]; then
  echo "a declined confirmation created source-2" >&2
  exit 1
fi
case "$decline_output" in
  *"Canceled."*) ;;
  *)
    echo "a declined confirmation did not report cancellation" >&2
    exit 1
    ;;
esac

(cd "$test_root/worktrees/source-1" && run_twt create </dev/null) >/dev/null
if [ -e "$test_root/worktrees/source-2" ]; then
  echo "an empty confirmation created source-2" >&2
  exit 1
fi

mkdir "$test_root/worktrees/source-2"
git -C "$bare" branch source-3 origin/main
printf '%s\n' source-4 >>"$test_root/tmux-sessions"
git -C "$bare" worktree add --detach "$test_root/worktrees/source-5" origin/main >/dev/null
rm -rf "$test_root/worktrees/source-5"
printf '%s\n' source-6-task >>"$test_root/tmux-sessions"

collision_output="$(
  printf 'y\n' |
    (cd "$test_root/worktrees/source-1" && run_twt create)
)"
if [ ! -d "$test_root/worktrees/source-6" ]; then
  echo "create did not skip occupied workspace names" >&2
  exit 1
fi
case "$collision_output" in
  *"source-6"*) ;;
  *)
    echo "create did not show the selected name after collisions" >&2
    exit 1
    ;;
esac

if (cd "$test_root/source" && run_twt create </dev/null) >"$test_root/outside.out" 2>"$test_root/outside.err"; then
  echo "create without arguments accepted a repository outside the twt directory" >&2
  exit 1
fi
if ! grep -Fq "not a direct child" "$test_root/outside.err"; then
  echo "create did not explain why the outside repository was rejected" >&2
  exit 1
fi

run_twt create "$test_root/source" slot-0 >/dev/null
printf 'y\n' | (cd "$test_root/worktrees/slot-0" && run_twt create) >/dev/null
if [ ! -d "$test_root/worktrees/slot-1" ]; then
  echo "create required the workspace prefix to match the repository name" >&2
  exit 1
fi

run_twt create "$test_root/source" custom >/dev/null
if (cd "$test_root/worktrees/custom" && run_twt create </dev/null) >"$test_root/custom.out" 2>"$test_root/custom.err"; then
  echo "create without arguments accepted a worktree without a numbered name" >&2
  exit 1
fi
if ! grep -Fq "must end with a number" "$test_root/custom.err"; then
  echo "create did not explain the numbered-name requirement" >&2
  exit 1
fi

echo "create from current worktree: ok"
