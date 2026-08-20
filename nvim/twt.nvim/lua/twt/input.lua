local M = {}

-- Opens a centered floating window for multi-line text.
-- `<C-s>` closes the window and sends the text to `done`. `q` closes the window
-- and sends `nil`, which tells the caller that the user canceled. `done` runs
-- one time only.
function M.open(opts, done)
  local buffer = vim.api.nvim_create_buf(false, true)
  local width, height = math.min(76, vim.o.columns - 4), 6
  local window = vim.api.nvim_open_win(buffer, true, {
    relative = "editor", width = width, height = height,
    row = math.floor((vim.o.lines - height) / 2), col = math.floor((vim.o.columns - width) / 2),
    border = "single", title = " " .. (opts.title or "twt") .. " ", title_pos = "center",
  })
  vim.bo[buffer].filetype = opts.filetype or "markdown"
  vim.bo[buffer].bufhidden = "wipe"
  local finished = false
  local function finish(text)
    if finished then return end
    finished = true
    if vim.api.nvim_win_is_valid(window) then
      vim.api.nvim_win_close(window, true)
    end
    done(text)
  end
  vim.keymap.set({ "n", "i" }, "<C-s>", function()
    finish(vim.trim(table.concat(vim.api.nvim_buf_get_lines(buffer, 0, -1, false), "\n")))
  end, { buffer = buffer })
  vim.keymap.set("n", "q", function() finish(nil) end, { buffer = buffer })
  vim.cmd("startinsert")
  return buffer, window
end

return M
