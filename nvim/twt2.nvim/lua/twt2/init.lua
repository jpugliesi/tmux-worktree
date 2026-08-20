local buffers = require("twt2.buffers")
local M = {
  agents = require("twt2.agents"),
  review = require("twt2.review"),
  snapshot = require("twt2.snapshot"),
}

-- The one place that shows a message to the user. Each action answers
-- `done(err)`, or `done(nil, ...)` on success.
local function report(err, message)
  if err then
    vim.notify("twt2: " .. err, vim.log.levels.ERROR)
  elseif message then
    vim.notify("twt2: " .. message, vim.log.levels.INFO)
  end
end

-- One entry for each action. It gives the user command, the default mapping,
-- and the message that a success shows. An action with no `ok` message shows
-- errors only.
local specs = {
  {
    cmd = "Twt2Agents",
    lhs = "<leader>arp",
    desc = "Select twt2 Agent Session",
    fn = function(done) M.agents.pick(done) end,
  },
  {
    cmd = "Twt2Note",
    lhs = "<leader>an",
    modes = { "n", "v" },
    desc = "Add twt2 review note",
    ok = "review note added",
    fn = function(done) M.review.prompt_add(done) end,
  },
  {
    cmd = "Twt2Review",
    lhs = "<leader>arr",
    desc = "Send twt2 review notes",
    ok = "review notes sent",
    fn = function(done) M.review.send(done) end,
  },
  {
    cmd = "Twt2Send",
    lhs = "<leader>ars",
    desc = "Send a twt2 Agent Session message",
    ok = "message sent",
    fn = function(done) M.agents.prompt_send(done) end,
  },
  {
    cmd = "Twt2Notes",
    desc = "List the twt2 review notes of this Project",
    fn = function(done) M.review.prompt_notes(done) end,
  },
  {
    cmd = "Twt2Resume",
    lhs = "<leader>aru",
    desc = "Resume the selected twt2 Agent Session",
    fn = function(done) M.agents.resume(done) end,
  },
  {
    cmd = "Twt2Focus",
    lhs = "<leader>arf",
    desc = "Focus the selected twt2 Agent Session",
    fn = function(done) M.agents.focus(done) end,
  },
  {
    cmd = "Twt2Refresh",
    lhs = "<leader>arR",
    desc = "Write a new twt2 transcript snapshot",
    fn = function(done) M.agents.refresh(done) end,
  },
  {
    cmd = "Twt2Clear",
    lhs = "<leader>arx",
    desc = "Clear twt2 review notes",
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
  local config = require("twt2.config")
  config.setup(options)
  local group = vim.api.nvim_create_augroup("twt2_nvim", { clear = true })
  for _, spec in ipairs(specs) do
    vim.api.nvim_create_user_command(spec.cmd, runner(spec), { desc = spec.desc })
  end
  -- Snapshot buffers read the file again after twt2 writes it.
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
