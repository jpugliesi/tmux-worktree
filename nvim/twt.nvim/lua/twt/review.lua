local agents = require("twt.agents")
local client = require("twt.client")
local config = require("twt.config")
local input = require("twt.input")
local M = {}

-- Every callback in this module is error-first: `done(err)` on a failure, or
-- `done(nil, result)` on success. This module never notifies the user. The
-- caller decides what to show.

local namespace = vim.api.nvim_create_namespace("twt_review")
local notes = {}
local next_id = 1

local function root_for(path)
  return vim.fs.root(path, { ".git" })
end

-- Returns the notes of one Project, or all notes when `project_id` is nil.
local function notes_for(project_id)
  local matches = {}
  for _, note in ipairs(notes) do
    if not project_id or note.project_id == project_id then
      matches[#matches + 1] = note
    end
  end
  return matches
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
  if not vim.api.nvim_buf_is_valid(note.buffer) or not vim.api.nvim_buf_is_loaded(note.buffer) then
    return nil, "a review-note buffer is not loaded"
  end
  local marks = vim.api.nvim_buf_get_extmark_by_id(note.buffer, namespace, note.mark, { details = true })
  if #marks == 0 then
    return nil, "a review note no longer has a valid line"
  end
  local path = vim.api.nvim_buf_get_name(note.buffer)
  if root_for(path) ~= note.root then
    return nil, "a review-note file moved outside its repository"
  end
  local line = marks[1] + 1
  local last = marks[3] and marks[3].end_row or line
  local snippet = vim.api.nvim_buf_get_lines(note.buffer, line - 1, last, false)
  return {
    line = line,
    last = last,
    path = vim.fs.relpath(note.root, path),
    snippet = table.concat(snippet, "\n"),
  }
end

function M.add(comment, start_line, end_line, done)
  done = done or function() end
  local buffer = vim.api.nvim_get_current_buf()
  local path = vim.api.nvim_buf_get_name(buffer)
  if path == "" or vim.trim(comment or "") == "" then
    done("save the file and enter a review comment")
    return
  end
  local root = root_for(path)
  if not root then
    done("the file is not in a Git repository")
    return
  end
  start_line = start_line or vim.fn.line(".")
  end_line = end_line or start_line
  client.context(vim.fs.dirname(path), function(err, context)
    if err then
      done(err)
      return
    end
    if not context.repositoryName or context.repositoryName == "" then
      done("the file is not in a twt Project repository")
      return
    end
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
      project_id = context.project.id,
      repository = context.repositoryName,
      root = root,
      buffer = buffer,
      mark = mark,
      comment = vim.trim(comment),
    }
    next_id = next_id + 1
    done(nil, notes[#notes])
  end)
end

-- Builds the message of one Project review batch. It returns
-- `err`, or `nil, { text = text, note_ids = note_ids }`.
function M.format(project_id)
  local batch = notes_for(project_id)
  if #batch == 0 then return "this Project has no review notes" end
  local parts = { "Please address these review notes:" }
  local note_ids = {}
  for index, note in ipairs(batch) do
    local place, err = location(note)
    if not place then return err end
    note_ids[#note_ids + 1] = note.id
    local suffix = place.line == place.last and tostring(place.line) or (place.line .. "-" .. place.last)
    parts[#parts + 1] = string.format("\n%d. @%s:%s#L%s\n```\n%s\n```\n%s", index, note.repository, place.path, suffix, place.snippet, note.comment)
  end
  return nil, { text = table.concat(parts, "\n"), note_ids = note_ids }
end

function M.clear(project_id)
  for index = #notes, 1, -1 do
    if not project_id or notes[index].project_id == project_id then
      remove(index)
    end
  end
end

function M.clear_current(done)
  done = done or function() end
  client.project_context(function(err, context)
    if err then
      done(err)
      return
    end
    M.clear(context.project.id)
    done(nil, context.project.id)
  end)
end

function M.delete(id)
  local index = index_of(id)
  return index ~= nil and remove(index)
end

local function clear_ids(ids)
  for _, id in ipairs(ids) do M.delete(id) end
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
  done = done or function() end
  client.project_context(function(err, context, directory)
    if err then
      done(err)
      return
    end
    local format_err, batch = M.format(context.project.id)
    if format_err then
      done(format_err)
      return
    end
    agents.send(batch.text, function(send_err, result)
      if not send_err and config.get().clear_after_send then clear_ids(batch.note_ids) end
      done(send_err, result)
    end, { project_id = context.project.id, directory = directory })
  end)
end

-- Asks for a note comment for the current line or selection. A canceled window
-- adds no note.
function M.prompt_add(done)
  done = done or function() end
  local start_line, end_line = vim.fn.line("."), vim.fn.line(".")
  local mode = vim.fn.mode()
  if mode:match("[vV\22]") then
    start_line, end_line = vim.fn.line("v"), vim.fn.line(".")
    if start_line > end_line then start_line, end_line = end_line, start_line end
  end
  input.open({ title = "Review note" }, function(text)
    if not text then return end
    M.add(text, start_line, end_line, done)
  end)
end

local function label(note)
  local place = location(note)
  local where = place and string.format("%s:%d", place.path, place.line) or "no valid line"
  local first = vim.split(note.comment, "\n", { plain = true })[1]
  return string.format("%s · %s", where, first)
end

-- Lists the review notes of the current Project, then deletes one or moves to it.
function M.prompt_notes(done)
  done = done or function() end
  client.project_context(function(err, context)
    if err then
      done(err)
      return
    end
    local project_notes = notes_for(context.project.id)
    if #project_notes == 0 then
      done("this Project has no review notes")
      return
    end
    config.get().select(project_notes, {
      prompt = "Select a twt review note",
      format_item = label,
    }, function(note)
      if not note then done(nil); return end
      config.get().select({ "Delete", "Go to the line" }, { prompt = label(note) }, function(choice)
        if choice == "Delete" then
          M.delete(note.id)
          done(nil, "deleted")
        elseif choice == "Go to the line" then
          done(M.jump(note.id), "jumped")
        else
          done(nil)
        end
      end)
    end)
  end)
end

return M
