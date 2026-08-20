local buffers = require("twt.buffers")
local M = {
  agents = require("twt.agents"),
  review = require("twt.review"),
  snapshot = require("twt.snapshot"),
}

-- The one place that shows a message to the user. Each action answers
-- `done(err)`, or `done(nil, ...)` on success.
local function report(err, message)
  if err then
    vim.notify("twt: " .. err, vim.log.levels.ERROR)
  elseif message then
    vim.notify("twt: " .. message, vim.log.levels.INFO)
  end
end

-- One entry for each action. It gives the user command, the default mapping,
-- and the message that a success shows. An action with no `ok` message shows
-- errors only.
local specs = {
  {
    cmd = "TwtAgents",
    lhs = "<leader>arp",
    desc = "Select twt Agent Session",
    fn = function(done) M.agents.pick(done) end,
  },
  {
    cmd = "TwtNote",
    lhs = "<leader>an",
    modes = { "n", "v" },
    desc = "Add twt review note",
    ok = "review note added",
    fn = function(done) M.review.prompt_add(done) end,
  },
  {
    cmd = "TwtReview",
    lhs = "<leader>arr",
    desc = "Send twt review notes",
    ok = "review notes sent",
    fn = function(done) M.review.send(done) end,
  },
  {
    cmd = "TwtSend",
    lhs = "<leader>ars",
    desc = "Send a twt Agent Session message",
    ok = "message sent",
    fn = function(done) M.agents.prompt_send(done) end,
  },
  {
    cmd = "TwtNotes",
    desc = "List the twt review notes of this Project",
    fn = function(done) M.review.prompt_notes(done) end,
  },
  {
    cmd = "TwtResume",
    lhs = "<leader>aru",
    desc = "Resume the selected twt Agent Session",
    fn = function(done) M.agents.resume(done) end,
  },
  {
    cmd = "TwtFocus",
    lhs = "<leader>arf",
    desc = "Focus the selected twt Agent Session",
    fn = function(done) M.agents.focus(done) end,
  },
  {
    cmd = "TwtRefresh",
    lhs = "<leader>arR",
    desc = "Write a new twt transcript snapshot",
    fn = function(done) M.agents.refresh(done) end,
  },
  {
    cmd = "TwtClear",
    lhs = "<leader>arx",
    desc = "Clear twt review notes",
    ok = "Project review notes cleared",
    fn = function(done) M.review.clear_current(done) end,
  },
}

local function runner(spec)
  return function()
    spec.fn(function(err) report(err, spec.ok) end)
  end
end

function M.setup(options)
  local config = require("twt.config")
  config.setup(options)
  local group = vim.api.nvim_create_augroup("twt_nvim", { clear = true })
  for _, spec in ipairs(specs) do
    vim.api.nvim_create_user_command(spec.cmd, runner(spec), { desc = spec.desc })
  end
  -- Snapshot buffers read the file again after twt writes it.
  vim.api.nvim_create_autocmd({ "FocusGained", "CursorHold" }, {
    group = group,
    callback = function(event)
      if vim.b[event.buf][buffers.project_id] then
        pcall(vim.cmd, "checktime " .. event.buf)
      end
    end,
  })
  if not config.get().default_keymaps then return end
  for _, spec in ipairs(specs) do
    if spec.lhs then
      vim.keymap.set(spec.modes or "n", spec.lhs, runner(spec), { desc = spec.desc })
    end
  end
end

return M
