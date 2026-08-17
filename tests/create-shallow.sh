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
    [ -f "$TWT_TEST_TMUX_STATE" ] &&
      grep -Fxq "${3:-}" "$TWT_TEST_TMUX_STATE"
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
printf '%s\n' "commit one" >"$test_root/source/README.md"
git -C "$test_root/source" add README.md
git -C "$test_root/source" commit -qm "initial commit"
printf '%s\n' "commit two" >"$test_root/source/README.md"
git -C "$test_root/source" add README.md
git -C "$test_root/source" commit -qm "second commit"
printf '%s\n' "commit three" >"$test_root/source/README.md"
git -C "$test_root/source" add README.md
git -C "$test_root/source" commit -qm "third commit"

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
  *--shallow*) ;;
  *)
    echo "create help does not document --shallow" >&2
    exit 1
    ;;
esac
case "$help_output" in
  *--depth*) ;;
  *)
    echo "create help does not document --depth" >&2
    exit 1
    ;;
esac
case "$help_output" in
  *--remote*) ;;
  *)
    echo "create help does not document --remote" >&2
    exit 1
    ;;
esac

if run_twt create --depth 0 "$test_root/source" bad-depth >/dev/null 2>&1; then
  echo "--depth 0 should be rejected" >&2
  exit 1
fi

if run_twt create --remote origin=https://example.com/x.git "file://$test_root/source" bad-origin >/dev/null 2>&1; then
  echo "--remote origin= should be rejected" >&2
  exit 1
fi

run_twt create --shallow \
  --remote github=https://github.com/example/source.git \
  "file://$test_root/source" repo-shallow >/dev/null

bare="$test_root/worktrees/.source.git"
if [ ! -f "$bare/shallow" ]; then
  echo "--shallow did not produce a shallow bare repo" >&2
  exit 1
fi

commit_count="$(git -C "$bare" rev-list --count HEAD)"
if [ "$commit_count" -ne 1 ]; then
  echo "expected 1 commit in shallow bare repo, got $commit_count" >&2
  exit 1
fi

github_url="$(git -C "$bare" remote get-url github)"
if [ "$github_url" != "https://github.com/example/source.git" ]; then
  echo "expected github remote URL, got $github_url" >&2
  exit 1
fi

origin_url="$(git -C "$bare" remote get-url origin)"
case "$origin_url" in
  file://*) ;;
  *)
    echo "expected file:// origin, got $origin_url" >&2
    exit 1
    ;;
esac

wt="$test_root/worktrees/repo-shallow"
if [ ! -d "$wt" ]; then
  echo "--shallow did not create a worktree" >&2
  exit 1
fi

run_twt create --depth 2 "file://$test_root/source" repo-depth2 >/dev/null 2>"$test_root/depth2.err" || true
if ! grep -q "already exists" "$test_root/depth2.err"; then
  echo "expected note that --depth is ignored when bare exists" >&2
  exit 1
fi

# Fresh bare with --depth 2
rm -rf "$test_root/worktrees"
run_twt create --depth 2 "file://$test_root/source" repo-depth2 >/dev/null
bare="$test_root/worktrees/.source.git"
if [ ! -f "$bare/shallow" ]; then
  echo "--depth 2 did not produce a shallow bare repo" >&2
  exit 1
fi
commit_count="$(git -C "$bare" rev-list --count HEAD)"
if [ "$commit_count" -ne 2 ]; then
  echo "expected 2 commits with --depth 2, got $commit_count" >&2
  exit 1
fi

echo "create --shallow/--depth/--remote: ok"
