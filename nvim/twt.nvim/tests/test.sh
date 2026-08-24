#!/usr/bin/env bash
set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

cd "$repository_root"
go build -o "$test_root/twt" ./cmd/twt
nvim --headless -u NONE -l nvim/twt.nvim/tests/run.lua
nvim --headless -u NONE -l nvim/twt.nvim/tests/keymaps.lua
nvim --headless -u NONE -l nvim/twt.nvim/tests/agent_send.lua
nvim --headless -u NONE -l nvim/twt.nvim/tests/review_integration.lua
TWT_TEST_BINARY="$test_root/twt" nvim --headless -u NONE -l nvim/twt.nvim/tests/integration.lua
