local M = {
  agents = require("twt2.agents"),
  review = require("twt2.review"),
}

function M.setup(options)
  require("twt2.config").setup(options)
  local group = vim.api.nvim_create_augroup("twt2_nvim", { clear = true })
  if not require("twt2.config").get().default_keymaps then return end
  vim.keymap.set("n", "<leader>arp", M.agents.pick, { desc = "Select twt2 Agent Session" })
  vim.keymap.set({ "n", "v" }, "<leader>an", M.review.prompt_add, { desc = "Add twt2 review note" })
  vim.keymap.set("n", "<leader>arr", function()
    M.review.send(function(err)
      vim.notify(err and ("twt2: " .. err) or "twt2: review notes sent", err and vim.log.levels.ERROR or vim.log.levels.INFO)
    end)
  end, { desc = "Send twt2 review notes" })
  vim.keymap.set("n", "<leader>aru", M.agents.resume, { desc = "Resume selected twt2 Agent Session" })
  vim.keymap.set("n", "<leader>arf", M.agents.focus, { desc = "Focus selected twt2 Agent Session" })
  vim.keymap.set("n", "<leader>arx", M.review.clear, { desc = "Clear twt2 review notes" })
  vim.api.nvim_create_autocmd("User", { group = group, pattern = "Twt2Refresh", callback = function() end })
end

return M
