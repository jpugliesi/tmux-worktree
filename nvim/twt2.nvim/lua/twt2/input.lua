local M = {}

-- Opens a centered floating window for multi-line text.
-- `<C-s>` sends the text to `done`. `q` closes the window and sends nothing.
function M.open(opts, done)
  local buffer = vim.api.nvim_create_buf(false, true)
  local width, height = math.min(76, vim.o.columns - 4), 6
  local window = vim.api.nvim_open_win(buffer, true, {
    relative = "editor", width = width, height = height,
    row = math.floor((vim.o.lines - height) / 2), col = math.floor((vim.o.columns - width) / 2),
    border = "single", title = " " .. (opts.title or "twt2") .. " ", title_pos = "center",
  })
  vim.bo[buffer].filetype = opts.filetype or "markdown"
  local function save()
    local text = vim.trim(table.concat(vim.api.nvim_buf_get_lines(buffer, 0, -1, false), "\n"))
    vim.api.nvim_win_close(window, true)
    done(text)
  end
  vim.keymap.set({ "n", "i" }, "<C-s>", save, { buffer = buffer })
  vim.keymap.set("n", "q", function() vim.api.nvim_win_close(window, true) end, { buffer = buffer })
  vim.cmd("startinsert")
  return buffer, window
end

return M
