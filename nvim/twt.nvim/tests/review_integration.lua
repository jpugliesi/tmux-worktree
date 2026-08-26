local plugin = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(plugin)

local socket = "twt-nvim-review-test-" .. tostring(vim.fn.getpid())
local function tmux(arguments, stdin)
  local command = { "tmux", "-L", socket, "-f", "/dev/null" }
  vim.list_extend(command, arguments)
  local result = vim.system(command, { stdin = stdin, text = true }):wait()
  assert(result.code == 0, result.stderr)
  return vim.trim(result.stdout)
end

local current = tmux({ "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "review-test", "--", "cat" })
local target = tmux({ "split-window", "-d", "-P", "-F", "#{pane_id}", "-t", current, "--", "cat" })
tmux({ "new-session", "-d", "-s", "unrelated-test", "--", "cat" })
local server_live = true
vim.api.nvim_create_autocmd("VimLeavePre", {
  callback = function()
    if server_live then pcall(tmux, { "kill-server" }) end
  end,
})
local old_tmux, old_pane = vim.env.TMUX, vim.env.TMUX_PANE
vim.env.TMUX, vim.env.TMUX_PANE = "/tmp/tmux/review-test,1,0", current

local copied
require("twt").setup({
  default_keymaps = false,
  clipboard = function(text) copied = text end,
  select = function(items, _, done) done(items[1]) end,
  tmux_runner = function(argv, opts, done)
    local command = { argv[1], "-L", socket, "-f", "/dev/null" }
    vim.list_extend(command, vim.list_slice(argv, 2))
    vim.system(command, { stdin = opts.stdin, text = true }, vim.schedule_wrap(done))
  end,
})

local listed
require("twt.tmux").list(function(err, panes)
  assert(err == nil, err)
  listed = panes
end)
assert(vim.wait(5000, function() return listed ~= nil end), "tmux pane list timed out")
assert(#listed == 1 and listed[1].id == target, vim.inspect(listed))

local root = vim.fn.tempname()
assert(vim.fn.mkdir(root, "p") == 1)
local path = root .. "/agent transcript.md"
assert(vim.fn.writefile({ "first line", "review line" }, path) == 0)
local buffer = vim.fn.bufadd(path)
vim.fn.bufload(buffer)
vim.api.nvim_set_current_buf(buffer)

local review = require("twt").review
local added
review.add("fix this response", 2, 2, function(err)
  assert(err == nil, err)
  added = true
end)
assert(added)

local finished, delivery_error
review.deliver(function(err)
  delivery_error = err
  finished = true
end)
assert(vim.wait(5000, function() return finished end), "tmux review delivery timed out")
assert(delivery_error == nil, delivery_error)
local capture = tmux({ "capture-pane", "-p", "-t", target })
assert(capture:find("fix this response", 1, true), capture)
assert(capture:find("agent transcript.md#L2", 1, true), capture)
assert(#review.list() == 1, "a successful tmux delivery must keep the Review Batch")

review.clear()
review.add("copy this response", 1, 1, function(err) assert(err == nil, err) end)
vim.env.TMUX, vim.env.TMUX_PANE = nil, nil
finished, delivery_error = false, nil
review.deliver(function(err)
  delivery_error = err
  finished = true
end)
assert(finished and delivery_error == nil, delivery_error)
assert(copied and copied:find("copy this response", 1, true), copied)
assert(#review.list() == 1, "a clipboard copy must keep the Review Batch")

vim.env.TMUX, vim.env.TMUX_PANE = old_tmux, old_pane
tmux({ "kill-server" })
server_live = false
print("twt.nvim standalone review integration: ok")
