local buffers = require("twt.buffers")
local M = {}

local defaults = {
  command = "twt",
  tmux_command = "tmux",
  default_keymaps = true,
  max_agents = 40,
  clear_after_send = true,
  snapshot_split = "tab drop",
  runner = nil,
  tmux_runner = nil,
  clipboard = function(text)
    if vim.fn.has("clipboard") ~= 1 and not vim.g.clipboard then
      error("Neovim has no clipboard provider", 0)
    end
    vim.fn.setreg("+", text, "v")
  end,
  select = function(items, opts, done)
    vim.ui.select(items, opts, done)
  end,
  confirm = function(question, done)
    done(vim.fn.confirm(question, "&Yes\n&No", 2) == 1)
  end,
  directory = function()
    local fixed = vim.b[buffers.workspace_directory]
    if fixed and fixed ~= "" then return fixed end
    local name = vim.api.nvim_buf_get_name(0)
    return name ~= "" and vim.fs.dirname(name) or vim.fn.getcwd()
  end,
}

local options = vim.deepcopy(defaults)

function M.setup(user)
  options = vim.tbl_deep_extend("force", vim.deepcopy(defaults), user or {})
end

function M.get()
  return options
end

return M
