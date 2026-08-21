local M = {}

local function clamp(value, min_value, max_value)
  if max_value < min_value then return min_value end
  return math.max(min_value, math.min(max_value, value))
end

-- Window row of a buffer line (1-based). Restores the view after the read.
local function win_row(win, line)
  return vim.api.nvim_win_call(win, function()
    local view = vim.fn.winsaveview()
    vim.api.nvim_win_set_cursor(0, { line, 0 })
    local row = vim.fn.winline()
    vim.fn.winrestview(view)
    return row
  end)
end

local function scroll_parent(win, count)
  if count == 0 then return end
  vim.api.nvim_win_call(win, function()
    local key = count > 0 and "<C-e>" or "<C-y>"
    vim.cmd(("normal! %d%s"):format(math.abs(count), vim.api.nvim_replace_termcodes(key, true, false, true)))
  end)
end

local save_hint = " C-s save · q quit "
local delete_hint = " C-s save · C-d delete · q quit "

-- Inner width of a boxed float. The box stays under 76 columns and uses
-- about 70% of the pane. col is the left edge inside the parent window.
local function box_width(available, requested, col)
  col = col or 0
  if requested then return requested end
  local inner = math.min(76, math.floor(available * 0.7), available - col - 2)
  return math.max(1, inner)
end

-- Column that centers one boxed float in the available width.
local function box_col(available, inner)
  return math.max(0, math.floor((available - inner - 2) / 2))
end

-- Columns taken by line numbers, the sign column, and the fold column.
local function text_offset(win)
  local info = vim.fn.getwininfo(win)[1]
  return info and info.textoff or 0
end

-- Display column of the first non-blank of a buffer line, plus the text
-- offset, so the box does not cover the line numbers.
local function line_indent(win, line)
  return vim.api.nvim_win_call(win, function()
    return vim.fn.indent(line)
  end)
end

-- Returns the floating-window config for a text entry. A start_line anchors
-- the window to that line or visual block inside the current window: below
-- when the block is high, above when the block is low. The parent window
-- scrolls when the block would sit under the note. The box lines up with the
-- start of the selected line.
function M.placement(opts)
  opts = opts or {}
  local height = opts.height or (opts.start_line and 8 or 6)
  local width = box_width(vim.o.columns, opts.width)
  if not opts.start_line then
    return {
      relative = "editor",
      width = width,
      height = height,
      row = math.floor((vim.o.lines - height) / 2),
      col = box_col(vim.o.columns, width),
    }
  end

  local win = opts.win or vim.api.nvim_get_current_win()
  local start_line = opts.start_line
  local end_line = opts.end_line or start_line
  if start_line > end_line then
    start_line, end_line = end_line, start_line
  end
  local win_height = vim.api.nvim_win_get_height(win)
  local border = 2
  local total = height + border
  local start_row = win_row(win, start_line)
  local end_row = win_row(win, end_line)
  local below_row = end_row
  local above_row = start_row - 1 - total
  local below_fits = below_row + total <= win_height
  local above_fits = above_row >= 0
  local low_in_view = (start_row + end_row) / 2 > win_height / 2
  local row
  if low_in_view and above_fits then
    row = above_row
  elseif below_fits then
    row = below_row
  elseif above_fits then
    row = above_row
  else
    scroll_parent(win, end_row + total - win_height)
    end_row = win_row(win, end_line)
    row = clamp(end_row, 0, math.max(0, win_height - total))
  end

  local win_width = vim.api.nvim_win_get_width(win)
  local gutter = text_offset(win)
  local indent = line_indent(win, start_line)
  local col = math.min(gutter + indent, math.max(gutter, win_width - 28 - 2))
  local inner = box_width(win_width, opts.width, col)
  return {
    relative = "win",
    win = win,
    width = inner,
    height = height,
    row = row,
    col = col,
  }
end

-- Ensure the float highlight groups exist. default=true keeps a user override.
-- Pmenu and FloatTitle are the colorscheme groups that stay readable on a
-- dark editor background. FloatBorder often matches that background.
local function ensure_highlights()
  vim.api.nvim_set_hl(0, "TwtFloat", { default = true, link = "Pmenu" })
  vim.api.nvim_set_hl(0, "TwtFloatBorder", { default = true, link = "FloatTitle" })
  vim.api.nvim_set_hl(0, "TwtFloatTitle", { default = true, link = "FloatTitle" })
  vim.api.nvim_set_hl(0, "TwtFloatFooter", { default = true, link = "FloatTitle" })
end

-- Opens a floating window for multi-line text.
-- `<C-s>` closes the window and sends the text to `done`. `q` closes the window
-- and sends `nil`, which tells the caller that the user canceled. When
-- on_delete is set, `<C-d>` closes the window and runs that callback. `done`
-- runs one time only. A start_line option anchors the window to a source
-- block. A text option fills the buffer before insert starts.
function M.open(opts, done)
  opts = opts or {}
  local buffer = vim.api.nvim_create_buf(false, true)
  local place = M.placement(opts)
  local config = {
    relative = place.relative,
    width = place.width,
    height = place.height,
    row = place.row,
    col = place.col,
    title = " " .. (opts.title or "twt") .. " ",
    title_pos = "center",
    footer = opts.on_delete and delete_hint or save_hint,
    footer_pos = "right",
    style = "minimal",
    zindex = 50,
    border = "rounded",
  }
  if place.win then
    config.win = place.win
  end
  ensure_highlights()
  local window = vim.api.nvim_open_win(buffer, true, config)
  vim.wo[window].winhighlight = "Normal:TwtFloat,FloatBorder:TwtFloatBorder,FloatTitle:TwtFloatTitle,FloatFooter:TwtFloatFooter"
  vim.bo[buffer].filetype = opts.filetype or "markdown"
  vim.bo[buffer].bufhidden = "wipe"
  if opts.text and opts.text ~= "" then
    local lines = vim.split(opts.text, "\n", { plain = true })
    vim.api.nvim_buf_set_lines(buffer, 0, -1, false, lines)
    vim.api.nvim_win_set_cursor(window, { #lines, #lines[#lines] })
  end
  local finished = false
  local function finish(text, deleted)
    if finished then return end
    finished = true
    if opts.on_close then opts.on_close() end
    if vim.api.nvim_win_is_valid(window) then
      vim.api.nvim_win_close(window, true)
    end
    if deleted then
      opts.on_delete()
      return
    end
    done(text)
  end
  vim.keymap.set({ "n", "i" }, "<C-s>", function()
    finish(vim.trim(table.concat(vim.api.nvim_buf_get_lines(buffer, 0, -1, false), "\n")))
  end, { buffer = buffer, nowait = true })
  vim.keymap.set("n", "q", function() finish(nil) end, { buffer = buffer, nowait = true })
  if opts.on_delete then
    vim.keymap.set({ "n", "i" }, "<C-d>", function() finish(nil, true) end, { buffer = buffer, nowait = true })
  end
  vim.cmd(opts.text and opts.text ~= "" and "startinsert!" or "startinsert")
  return buffer, window
end

return M
