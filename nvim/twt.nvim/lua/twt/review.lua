local agents = require("twt.agents")
local clipboard = require("twt.clipboard")
local config = require("twt.config")
local input = require("twt.input")
local tmux = require("twt.tmux")
local M = {}

-- Every callback in this module is error-first: `done(err)` on a failure, or
-- `done(nil, result)` on success. This module never notifies the user. The
-- caller decides what to show.

local namespace = vim.api.nvim_create_namespace("twt_review")
local notes = {}
local next_id = 1
local delivering = false

local function file_path(buffer)
  if not vim.api.nvim_buf_is_valid(buffer) or not vim.api.nvim_buf_is_loaded(buffer) then
    return nil, "a review-note buffer is not loaded"
  end
  if vim.bo[buffer].buftype ~= "" then
    return nil, "review notes need a regular file buffer"
  end
  local path = vim.api.nvim_buf_get_name(buffer)
  if path == "" or path:find("[%z\r\n]") or path:match("^%a[%w+.-]*://") then
    return nil, "save the file before you add a review note"
  end
  return vim.fs.normalize(vim.fn.fnamemodify(path, ":p"))
end

local function index_of(id)
  for index, note in ipairs(notes) do
    if note.id == id then return index end
  end
  return nil
end

-- Deletes one note and the sign that marks its lines.
local function remove(index)
  local note = notes[index]
  if not note then return false end
  if vim.api.nvim_buf_is_valid(note.buffer) then
    pcall(vim.api.nvim_buf_del_extmark, note.buffer, namespace, note.mark)
  end
  table.remove(notes, index)
  return true
end

local function location(note)
  local path, path_err = file_path(note.buffer)
  if not path then return nil, path_err end
  local marks = vim.api.nvim_buf_get_extmark_by_id(note.buffer, namespace, note.mark, { details = true })
  if #marks == 0 then
    return nil, "a review note no longer has a valid line"
  end
  local line = marks[1] + 1
  local last = marks[3] and marks[3].end_row or line
  local snippet = vim.api.nvim_buf_get_lines(note.buffer, line - 1, last, false)
  return {
    line = line,
    last = last,
    path = path,
    snippet = table.concat(snippet, "\n"),
  }
end

function M.add(comment, start_line, end_line, done)
  done = done or function() end
  local buffer = vim.api.nvim_get_current_buf()
  if vim.trim(comment or "") == "" then
    done("save the file and enter a review comment")
    return
  end
  local _, path_err = file_path(buffer)
  if path_err then
    done(path_err)
    return
  end
  start_line = start_line or vim.fn.line(".")
  end_line = end_line or start_line
  local mark = vim.api.nvim_buf_set_extmark(buffer, namespace, start_line - 1, 0, {
    end_row = end_line,
    end_col = 0,
    right_gravity = false,
    end_right_gravity = true,
    sign_text = "R",
    sign_hl_group = "DiagnosticWarn",
  })
  notes[#notes + 1] = {
    id = next_id,
    revision = 1,
    buffer = buffer,
    mark = mark,
    comment = vim.trim(comment),
  }
  next_id = next_id + 1
  done(nil, notes[#notes])
end

local function markdown_fence(text)
  local longest = 0
  for run in text:gmatch("`+") do longest = math.max(longest, #run) end
  return string.rep("`", math.max(3, longest + 1))
end

-- Builds the Review Batch of this Neovim session. It returns `err`, or
-- `nil, { text = text, notes = { { id = id, revision = revision } } }`.
function M.format()
  if #notes == 0 then return "this Neovim session has no review notes" end
  local parts = { "Please address these review notes:" }
  local batch_notes = {}
  for index, note in ipairs(notes) do
    local place, err = location(note)
    if not place then return err end
    batch_notes[#batch_notes + 1] = { id = note.id, revision = note.revision }
    local suffix = place.line == place.last and tostring(place.line) or (place.line .. "-" .. place.last)
    local fence = markdown_fence(place.snippet)
    parts[#parts + 1] = string.format("\n%d. @%s#L%s\n%s\n%s\n%s\n%s", index, place.path, suffix, fence, place.snippet, fence, note.comment)
  end
  return nil, { text = table.concat(parts, "\n"), notes = batch_notes }
end

function M.clear()
  for index = #notes, 1, -1 do remove(index) end
end

function M.clear_current(done)
  done = done or function() end
  if #notes == 0 then
    done("this Neovim session has no review notes")
    return
  end
  config.get().confirm("Are you sure you want to clear all review notes?", function(yes)
    if not yes then return end
    M.clear()
    done(nil)
  end)
end

function M.delete(id)
  local index = index_of(id)
  return index ~= nil and remove(index)
end

-- Replaces the comment of one note. Empty text is an error.
function M.update(id, comment)
  local index = index_of(id)
  if not index then return "the review note no longer exists" end
  local trimmed = vim.trim(comment or "")
  if trimmed == "" then return "save the file and enter a review comment" end
  notes[index].comment = trimmed
  notes[index].revision = notes[index].revision + 1
  return nil
end

-- Notes whose extmark range overlaps the given lines in one buffer.
local function notes_covering(buffer, start_line, end_line)
  local matches = {}
  for _, note in ipairs(notes) do
    if note.buffer == buffer then
      local place = location(note)
      if place and place.line <= end_line and place.last >= start_line then
        matches[#matches + 1] = note
      end
    end
  end
  return matches
end

local function label(note)
  local place = location(note)
  local where = place and string.format("%s:%d", place.path, place.line) or "no valid line"
  local first = vim.split(note.comment, "\n", { plain = true })[1]
  return string.format("%s · %s", where, first)
end

-- Markdown for the snacks picker preview. LazyVim replaces vim.ui.select
-- with Snacks.picker.select. The default select layout hides preview, so
-- the caller passes these options through opts.snacks.
local function preview_text(note)
  local place, err = location(note)
  local parts = {}
  if place then
    local suffix = place.line == place.last and tostring(place.line) or (place.line .. "-" .. place.last)
    parts[#parts + 1] = string.format("%s:%s", place.path, suffix)
    parts[#parts + 1] = ""
    if place.snippet ~= "" then
      local fence = markdown_fence(place.snippet)
      parts[#parts + 1] = fence
      parts[#parts + 1] = place.snippet
      parts[#parts + 1] = fence
      parts[#parts + 1] = ""
    end
  else
    parts[#parts + 1] = err or "no valid line"
    parts[#parts + 1] = ""
  end
  parts[#parts + 1] = note.comment
  return table.concat(parts, "\n")
end

local function note_select_opts(prompt)
  return {
    prompt = prompt,
    format_item = label,
    kind = "twt_review_note",
    snacks = {
      preview = function(ctx)
        local note = ctx.item and (ctx.item.item or ctx.item)
        if not note then return end
        ctx.preview:reset()
        ctx.preview:set_lines(vim.split(preview_text(note), "\n", { plain = true }))
        ctx.preview:highlight({ ft = "markdown" })
      end,
      layout = { preset = "default" },
    },
  }
end

local function clear_batch(batch_notes)
  for _, sent in ipairs(batch_notes) do
    local index = index_of(sent.id)
    if index and notes[index].revision == sent.revision then remove(index) end
  end
end

local function start_delivery(done, route)
  done = done or function() end
  if delivering then
    done("a review delivery is already in progress")
    return
  end
  local format_err, batch = M.format()
  if format_err then
    done(format_err)
    return
  end
  delivering = true
  local finished = false
  local function finish(err, result, clear_after)
    if finished then return end
    finished = true
    delivering = false
    if not err and clear_after then clear_batch(batch.notes) end
    done(err, result)
  end
  local ok, route_err = pcall(route, batch.text, finish)
  if not ok then finish("review delivery failed: " .. tostring(route_err)) end
end

function M.list()
  return vim.deepcopy(notes)
end

function M.jump(id)
  local index = index_of(id)
  if not index then return "the review note no longer exists" end
  local note = notes[index]
  local place, err = location(note)
  if not place then return err end
  vim.api.nvim_set_current_buf(note.buffer)
  vim.api.nvim_win_set_cursor(0, { place.line, 0 })
  return nil
end

function M.send(done)
  local clear_after = config.get().clear_after_send
  start_delivery(done, function(text, finish)
    agents.send(text, function(err, result)
      finish(err, result, clear_after and not (result and result.canceled))
    end)
  end)
end

local function copy_text(text, finish)
  clipboard.copy(text, function(err)
    finish(err, err and nil or "review notes copied to the clipboard", false)
  end)
end

-- Copies the complete Review Batch. Copying does not clear notes because the
-- clipboard can change before the user pastes it.
function M.copy(done)
  start_delivery(done, copy_text)
end

local function send_to_pane(pane, text, clear_after, finish)
  tmux.send(pane.id, text, function(err)
    finish(err, err and nil or ("review notes sent to " .. pane.label), clear_after)
  end)
end

function M.send_pane(done)
  local clear_after = config.get().clear_after_send
  start_delivery(done, function(text, finish)
    tmux.pick(function(err, pane)
      if err then finish(err); return end
      if not pane then finish(nil); return end
      send_to_pane(pane, text, clear_after, finish)
    end)
  end)
end

-- Uses only Neovim and tmux. Outside tmux, or when no other live pane exists,
-- it copies the Review Batch. In tmux, the user selects a pane or Clipboard.
function M.deliver(done)
  local clear_after = config.get().clear_after_send
  start_delivery(done, function(text, finish)
    if not tmux.available() then
      copy_text(text, finish)
      return
    end
    tmux.list(function(err, panes)
      if err then finish(err); return end
      if #panes == 0 then
        copy_text(text, finish)
        return
      end
      local destinations = {}
      for _, pane in ipairs(panes) do
        local destination = vim.deepcopy(pane)
        destination.kind = "pane"
        destinations[#destinations + 1] = destination
      end
      destinations[#destinations + 1] = { kind = "clipboard", label = "Clipboard" }
      config.get().select(destinations, {
        prompt = "Send review notes to",
        format_item = function(destination) return destination.label end,
      }, function(destination)
        if not destination then finish(nil); return end
        if destination.kind == "clipboard" then
          copy_text(text, finish)
        else
          send_to_pane(destination, text, clear_after, finish)
        end
      end)
    end)
  end)
end

local function open_note_window(opts, on_text)
  local source = opts.buffer
  local draft = vim.api.nvim_create_namespace("twt_review_draft")
  local mark = vim.api.nvim_buf_set_extmark(source, draft, opts.start_line - 1, 0, {
    end_row = opts.end_line,
    end_col = 0,
    hl_group = "Visual",
    hl_eol = true,
  })
  local function clear_draft()
    if vim.api.nvim_buf_is_valid(source) then
      pcall(vim.api.nvim_buf_del_extmark, source, draft, mark)
    end
  end
  input.open({
    title = "Note",
    text = opts.text,
    start_line = opts.start_line,
    end_line = opts.end_line,
    on_close = clear_draft,
    on_delete = opts.on_delete,
  }, on_text)
end

-- Opens the note window with the current comment. Save updates that note.
-- Ctrl-D deletes the note.
local function prompt_edit(note, done)
  local place, err = location(note)
  if not place then
    done(err)
    return
  end
  open_note_window({
    buffer = note.buffer,
    start_line = place.line,
    end_line = place.last,
    text = note.comment,
    on_delete = function()
      M.delete(note.id)
      done(nil, "review note deleted")
    end,
  }, function(text)
    if not text then return end
    if vim.trim(text) == "" then
      M.delete(note.id)
      done(nil, "review note deleted")
      return
    end
    local update_err = M.update(note.id, text)
    if update_err then
      done(update_err)
      return
    end
    done(nil, notes[index_of(note.id)])
  end)
end

-- Asks for a note comment for the current line or selection. A canceled window
-- adds no note. When the line already has a note, the window opens that note
-- for edit. The window sits below the selected block when the viewport has
-- room, and above it when the block is low in the window.
function M.prompt_add(done)
  done = done or function() end
  local start_line, end_line = vim.fn.line("."), vim.fn.line(".")
  local mode = vim.fn.mode()
  if mode:match("[vV\22]") then
    start_line, end_line = vim.fn.line("v"), vim.fn.line(".")
    if start_line > end_line then start_line, end_line = end_line, start_line end
  end
  local source = vim.api.nvim_get_current_buf()
  local existing = notes_covering(source, start_line, end_line)
  if #existing == 1 then
    prompt_edit(existing[1], done)
    return
  end
  if #existing > 1 then
    config.get().select(existing, note_select_opts("Select a review note"), function(note)
      if not note then done(nil); return end
      prompt_edit(note, done)
    end)
    return
  end
  open_note_window({
    buffer = source,
    start_line = start_line,
    end_line = end_line,
  }, function(text)
    if not text then return end
    M.add(text, start_line, end_line, done)
  end)
end

-- Deletes the review note on the current line or selection. A line with
-- more than one note asks which note to delete.
function M.prompt_delete(done)
  done = done or function() end
  local start_line, end_line = vim.fn.line("."), vim.fn.line(".")
  local mode = vim.fn.mode()
  if mode:match("[vV\22]") then
    start_line, end_line = vim.fn.line("v"), vim.fn.line(".")
    if start_line > end_line then start_line, end_line = end_line, start_line end
  end
  local existing = notes_covering(vim.api.nvim_get_current_buf(), start_line, end_line)
  if #existing == 0 then
    done("this line has no review note")
    return
  end
  local function drop(note)
    if not note then done(nil); return end
    M.delete(note.id)
    done(nil, "review note deleted")
  end
  if #existing == 1 then
    drop(existing[1])
    return
  end
  config.get().select(existing, note_select_opts("Delete a review note"), drop)
end

-- Lists the Review Notes of this Neovim session, then opens the selected note.
function M.prompt_notes(done)
  done = done or function() end
  if #notes == 0 then
    done("this Neovim session has no review notes")
    return
  end
  config.get().select(notes, note_select_opts("Select a review note"), function(note)
    if not note then done(nil); return end
    local jump_err = M.jump(note.id)
    if jump_err then done(jump_err); return end
    prompt_edit(note, done)
  end)
end

return M
