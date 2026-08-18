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
  switch-client)
    ;;
  set-environment)
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
    "$project_root/bin/twt" "$@"
}

help_output="$(run_twt create --help)"
case "$help_output" in
  *--with-shared*) ;;
  *)
    echo "create help does not document --with-shared" >&2
    exit 1
    ;;
esac

run_twt create "$test_root/source" repo-0 >/dev/null

bare="$test_root/worktrees/.source.git"
if [ -e "$bare/hooks/post-checkout" ]; then
  echo "create enabled shared files without --with-shared" >&2
  exit 1
fi

(cd "$bare" && run_twt shared enable >/dev/null)
if [ ! -x "$bare/hooks/post-checkout" ]; then
  echo "twt shared enable no longer installs the post-checkout hook" >&2
  exit 1
fi
(cd "$bare" && run_twt shared disable >/dev/null)

mkdir -p "$bare/shared"
printf '%s\n' "return {}" >"$bare/shared/.lazy.lua"

run_twt create --with-shared "$test_root/source" repo-1 >/dev/null

if [ ! -x "$bare/hooks/post-checkout" ]; then
  echo "--with-shared did not install the post-checkout hook" >&2
  exit 1
fi

shared_link="$test_root/worktrees/repo-1/.lazy.lua"
if [ ! -L "$shared_link" ]; then
  echo "--with-shared did not link shared files during worktree creation" >&2
  exit 1
fi

actual_target="$(readlink "$shared_link")"
expected_target="$(cd "$bare/shared" && pwd -P)/.lazy.lua"
if [ "$actual_target" != "$expected_target" ]; then
  echo "--with-shared linked .lazy.lua to $actual_target, expected $expected_target" >&2
  exit 1
fi

echo "create --with-shared: ok"
