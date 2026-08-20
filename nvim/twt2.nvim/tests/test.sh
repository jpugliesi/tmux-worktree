#!/usr/bin/env bash
set -euo pipefail

repository_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

cd "$repository_root"
go build -o "$test_root/twt2" ./cmd/twt2
nvim --headless -u NONE -l nvim/twt2.nvim/tests/run.lua
TWT2_TEST_BINARY="$test_root/twt2" nvim --headless -u NONE -l nvim/twt2.nvim/tests/integration.lua
