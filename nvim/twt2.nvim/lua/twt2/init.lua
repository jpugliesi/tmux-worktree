local M = {
  agents = require("twt2.agents"),
  review = require("twt2.review"),
  snapshot = require("twt2.snapshot"),
}

local function report(err, message)
  vim.notify(err and ("twt2: " .. err) or ("twt2: " .. message), err and vim.log.levels.ERROR or vim.log.levels.INFO)
end

local function send_review()
  M.review.send(function(err) report(err, "review notes sent") end)
end

local function show_notes()
  M.review.prompt_notes(function(err) if err then report(err) end end)
end

local function clear_notes()
  M.review.clear_current(function(err) report(err, "Project review notes cleared") end)
end

function M.setup(options)
  require("twt2.config").setup(options)
  local group = vim.api.nvim_create_augroup("twt2_nvim", { clear = true })
  vim.api.nvim_create_user_command("Twt2Agents", function() M.agents.pick() end, { desc = "Select twt2 Agent Session" })
  vim.api.nvim_create_user_command("Twt2Send", function() M.agents.prompt_send() end, { desc = "Send a message to the selected twt2 Agent Session" })
  vim.api.nvim_create_user_command("Twt2Notes", show_notes, { desc = "List the twt2 review notes of this Project" })
  vim.api.nvim_create_user_command("Twt2Resume", function() M.agents.resume() end, { desc = "Resume the selected twt2 Agent Session" })
  vim.api.nvim_create_user_command("Twt2Refresh", function() M.agents.refresh() end, { desc = "Write a new twt2 transcript snapshot" })
  -- Snapshot buffers read the file again after twt2 writes it.
  vim.api.nvim_create_autocmd({ "FocusGained", "CursorHold" }, {
    group = group,
    callback = function(event)
      if vim.b[event.buf].twt2_project_id then
        pcall(vim.cmd, "checktime " .. event.buf)
      end
    end,
  })
  if not require("twt2.config").get().default_keymaps then return end
  vim.keymap.set("n", "<leader>arp", M.agents.pick, { desc = "Select twt2 Agent Session" })
  vim.keymap.set({ "n", "v" }, "<leader>an", M.review.prompt_add, { desc = "Add twt2 review note" })
  vim.keymap.set("n", "<leader>arr", send_review, { desc = "Send twt2 review notes" })
  vim.keymap.set("n", "<leader>ars", M.agents.prompt_send, { desc = "Send a twt2 Agent Session message" })
  vim.keymap.set("n", "<leader>aru", M.agents.resume, { desc = "Resume selected twt2 Agent Session" })
  vim.keymap.set("n", "<leader>arf", M.agents.focus, { desc = "Focus selected twt2 Agent Session" })
  vim.keymap.set("n", "<leader>arR", M.agents.refresh, { desc = "Refresh the twt2 transcript snapshot" })
  vim.keymap.set("n", "<leader>arx", clear_notes, { desc = "Clear twt2 review notes" })
end

return M
