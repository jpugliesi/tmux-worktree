#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
tmux_socket="twt-test-$$"
real_tmux="$(command -v tmux)"

cleanup() {
  "$real_tmux" -L "$tmux_socket" kill-server 2>/dev/null || true
  rm -rf "$test_root"
}
trap cleanup EXIT

mkdir -p "$test_root/bin" "$test_root/home" "$test_root/source"

cat >"$test_root/bin/tmux" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [ "${1:-}" = "switch-client" ]; then
  exit 0
fi

if [ "${TMUX:-}" = "test" ]; then
  unset TMUX TMUX_PANE
fi

exec "$TWT_TEST_REAL_TMUX" -L "$TWT_TEST_TMUX_SOCKET" -f /dev/null "$@"
EOF
chmod +x "$test_root/bin/tmux"

git init -q -b main "$test_root/source"
git -C "$test_root/source" config user.name "tmux-worktree test"
git -C "$test_root/source" config user.email "test@example.com"
printf '%s\n' "test repository" >"$test_root/source/README.md"
git -C "$test_root/source" add README.md
git -C "$test_root/source" commit -qm "initial commit"
initial_commit="$(git -C "$test_root/source" rev-parse HEAD)"
printf '%s\n' "latest change" >>"$test_root/source/README.md"
git -C "$test_root/source" commit -qam "latest commit"

export PATH="$test_root/bin:$PATH"
export HOME="$test_root/home"
export TMUX_WORKTREE_DIR="$test_root/worktrees"
export TWT_TEST_REAL_TMUX="$real_tmux"
export TWT_TEST_TMUX_SOCKET="$tmux_socket"

tmux_test() {
  "$real_tmux" -L "$tmux_socket" -f /dev/null "$@"
}

run_twt() {
  TMUX="test" "$project_root/bin/twt" "$@"
}

rename_help="$(TMUX='' "$project_root/bin/twt" rename --help)"
case "$rename_help" in
  *"Usage: twt rename [--] <name>"*) ;;
  *)
    echo "rename help did not show its usage" >&2
    exit 1
    ;;
esac

if TMUX='' "$project_root/bin/twt" rename >/dev/null 2>"$test_root/rename-missing.stderr"; then
  echo "rename accepted a missing name" >&2
  exit 1
fi
if ! grep -Fq "Usage: twt rename [--] <name>" "$test_root/rename-missing.stderr"; then
  echo "rename did not show usage for a missing name" >&2
  exit 1
fi
if TMUX='' "$project_root/bin/twt" rename one two >/dev/null 2>"$test_root/rename-extra.stderr"; then
  echo "rename accepted extra arguments" >&2
  exit 1
fi
if TMUX='' "$project_root/bin/twt" rename outside >/dev/null 2>"$test_root/rename-outside.stderr"; then
  echo "rename ran outside tmux" >&2
  exit 1
fi
if ! grep -Fq "must be run inside tmux" "$test_root/rename-outside.stderr"; then
  echo "rename did not explain that tmux is required" >&2
  exit 1
fi

run_in_session() {
  local session="$1" label="$2"
  shift 2

  local expected_status="${TWT_EXPECT_STATUS:-0}"
  local status_file="$test_root/$label.status"
  local stdout_file="$test_root/$label.stdout"
  local stderr_file="$test_root/$label.stderr"
  local command=""

  printf -v command '%q ' "$project_root/bin/twt" "$@"
  # Expand these variables inside the tmux pane, not while building the command.
  # shellcheck disable=SC2016
  printf -v command '%s> %q 2> %q; twt_exit_code=$?; printf "%%s\\n" "$twt_exit_code" > %q' \
    "$command" "$stdout_file" "$stderr_file" "$status_file"

  tmux_test send-keys -t "$session" "$command" C-m

  local attempt
  for ((attempt = 0; attempt < 100; attempt++)); do
    [ -f "$status_file" ] && break
    sleep 0.05
  done

  if [ ! -f "$status_file" ]; then
    tmux_test capture-pane -p -t "$session" >&2 || true
    echo "$label timed out" >&2
    exit 1
  fi

  if [ "$(cat "$status_file")" -ne "$expected_status" ]; then
    cat "$stdout_file" >&2
    cat "$stderr_file" >&2
    echo "$label returned $(cat "$status_file"), expected $expected_status" >&2
    exit 1
  fi
}

tmux_test new-session -d -s rename-empty
TWT_EXPECT_STATUS=1 run_in_session rename-empty rename-empty rename ""

tmux_test new-session -d -s rename-leading
run_in_session rename-leading rename-leading rename -- -work
tmux_test has-session -t "=-work"
run_in_session -work rename-same rename -- -work
tmux_test has-session -t "=-work"

tmux_test new-session -d -s rename-without-base -c "$test_root"
run_in_session rename-without-base rename-without-base rename renamed-with-base
if [ "$(tmux_test show-environment -t renamed-with-base TWT_BASE_SESSION)" != "TWT_BASE_SESSION=rename-without-base" ]; then
  echo "rename did not save the old stable session name" >&2
  exit 1
fi
if [ -s "$test_root/rename-without-base.stderr" ]; then
  cat "$test_root/rename-without-base.stderr" >&2
  echo "rename printed an unrelated error outside Git" >&2
  exit 1
fi

tmux_test new-session -d -s rename-option
TWT_EXPECT_STATUS=1 run_in_session rename-option rename-option rename -option-name
tmux_test has-session -t "=rename-option"

tmux_test new-session -d -s rename-source
tmux_test new-session -d -s rename-target
TWT_EXPECT_STATUS=1 run_in_session rename-source rename-collision rename rename-target
tmux_test has-session -t "=rename-source"
tmux_test has-session -t "=rename-target"
if ! grep -Fq "twt rename: duplicate session: rename-target" "$test_root/rename-collision.stderr"; then
  echo "rename collision did not report a clear error" >&2
  exit 1
fi

run_twt create "$test_root/source" core-4 >/dev/null

if [ "$(tmux_test show-environment -t core-4 TWT_BASE_SESSION)" != "TWT_BASE_SESSION=core-4" ]; then
  echo "create did not record the stable session name" >&2
  exit 1
fi

run_in_session core-4 rename-manual rename manual-name

if tmux_test has-session -t "=core-4" 2>/dev/null; then
  echo "rename kept the old session name" >&2
  exit 1
fi
tmux_test has-session -t "=manual-name"
if [ "$(tmux_test show-environment -t manual-name TWT_BASE_SESSION)" != "TWT_BASE_SESSION=core-4" ]; then
  echo "rename changed the stable session name" >&2
  exit 1
fi

run_in_session manual-name start-home-bug start home-bug "$initial_commit"

if [ "$(git -C "$TMUX_WORKTREE_DIR/core-4" branch --show-current)" != "home-bug" ]; then
  echo "start did not create and switch to the requested branch" >&2
  exit 1
fi
if [ "$(git -C "$TMUX_WORKTREE_DIR/core-4" rev-parse HEAD)" != "$initial_commit" ]; then
  echo "start did not use the explicit start point" >&2
  exit 1
fi
tmux_test has-session -t "=core-4-home-bug"

run_in_session core-4-home-bug start-follow-up start follow-up

tmux_test has-session -t "=core-4-follow-up"
if tmux_test has-session -t "=core-4-home-bug-follow-up" 2>/dev/null; then
  echo "start stacked the new branch onto the previous session name" >&2
  exit 1
fi

printf '%s\n' "dirty" >>"$TMUX_WORKTREE_DIR/core-4/README.md"
printf '%s\n' "keep me" >"$TMUX_WORKTREE_DIR/core-4/untracked.txt"

run_in_session core-4-follow-up reset reset

tmux_test has-session -t "=core-4"
if [ "$(git -C "$TMUX_WORKTREE_DIR/core-4" branch --show-current)" != "core-4" ]; then
  echo "reset did not restore the stable branch" >&2
  exit 1
fi
if ! git -C "$TMUX_WORKTREE_DIR/core-4" diff --quiet; then
  echo "reset did not discard tracked changes" >&2
  exit 1
fi
if [ ! -f "$TMUX_WORKTREE_DIR/core-4/untracked.txt" ]; then
  echo "reset removed an untracked file" >&2
  exit 1
fi

run_twt create "$test_root/source" core-5 >/dev/null
tmux_test set-environment -u -t core-5 TWT_BASE_SESSION
tmux_test set-environment -t core-5 GCB_BASE_SESSION core-5
tmux_test rename-session -t core-5 core-5-old-ticket

run_in_session core-5-old-ticket legacy-start start new-ticket

tmux_test has-session -t "=core-5-new-ticket"
if [ "$(tmux_test show-environment -t core-5-new-ticket TWT_BASE_SESSION)" != "TWT_BASE_SESSION=core-5" ]; then
  echo "start did not migrate GCB_BASE_SESSION" >&2
  exit 1
fi

TWT_EXPECT_STATUS=1 run_in_session core-5-new-ticket existing-branch start home-bug
tmux_test has-session -t "=core-5-new-ticket"
if [ "$(git -C "$TMUX_WORKTREE_DIR/core-5" branch --show-current)" != "new-ticket" ]; then
  echo "an existing branch changed the current branch" >&2
  exit 1
fi

TWT_EXPECT_STATUS=1 run_in_session core-5-new-ticket invalid-start start bad-start does-not-exist
if git -C "$TMUX_WORKTREE_DIR/core-5" show-ref --verify --quiet refs/heads/bad-start; then
  echo "an invalid start point created a branch" >&2
  exit 1
fi
tmux_test has-session -t "=core-5-new-ticket"

tmux_test new-session -d -s core-5-collision
TWT_EXPECT_STATUS=1 run_in_session core-5-new-ticket session-collision start collision
if git -C "$TMUX_WORKTREE_DIR/core-5" show-ref --verify --quiet refs/heads/collision; then
  echo "a session collision created a branch" >&2
  exit 1
fi
tmux_test has-session -t "=core-5-new-ticket"
tmux_test kill-session -t "=core-5-collision"

run_in_session core-5-new-ticket dotted-branch start fix.v2
tmux_test has-session -t "=core-5-fix_v2"
if [ "$(git -C "$TMUX_WORKTREE_DIR/core-5" branch --show-current)" != "fix.v2" ]; then
  echo "start changed a dotted Git branch name" >&2
  exit 1
fi

TWT_EXPECT_STATUS=1 run_in_session core-5-fix_v2 dotted-collision start fix_v2
if git -C "$TMUX_WORKTREE_DIR/core-5" show-ref --verify --quiet refs/heads/fix_v2; then
  echo "a normalized session collision created a branch" >&2
  exit 1
fi
tmux_test has-session -t "=core-5-fix_v2"

run_twt create "$test_root/source" core-6 >/dev/null
tmux_test set-environment -u -t core-6 TWT_BASE_SESSION
tmux_test set-environment -u -t core-6 GCB_BASE_SESSION
tmux_test rename-window -t core-6:0 shell
tmux_test rename-session -t core-6 core-6-old-ticket

run_in_session core-6-old-ticket no-variable-fallback start fallback-task

tmux_test has-session -t "=core-6-fallback-task"
if [ "$(tmux_test show-environment -t core-6-fallback-task TWT_BASE_SESSION)" != "TWT_BASE_SESSION=core-6" ]; then
  echo "start did not recover the stable name from the worktree" >&2
  exit 1
fi

echo "start and reset lifecycle: ok"
