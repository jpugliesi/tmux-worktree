local agents = require("twt2.agents")
local client = require("twt2.client")
local config = require("twt2.config")
local M = {}

local namespace = vim.api.nvim_create_namespace("twt2_review")
local notes = {}
local next_id = 1

local function root_for(path)
  return vim.fs.root(path, { ".git" })
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
      done("the file is not in a twt2 Project repository")
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

function M.format(project_id)
	local parts = { "Please address these review notes:" }
	local included = {}
  local count = 0
  for _, note in ipairs(notes) do
    if note.project_id == project_id then
      local place, err = location(note)
      if not place then return nil, err end
			count = count + 1
			included[#included + 1] = note.id
      local suffix = place.line == place.last and tostring(place.line) or (place.line .. "-" .. place.last)
      parts[#parts + 1] = string.format("\n%d. @%s:%s#L%s\n```\n%s\n```\n%s", count, note.repository, place.path, suffix, place.snippet, note.comment)
    end
  end
  if count == 0 then return nil, "this Project has no review notes" end
	return table.concat(parts, "\n"), nil, included
end

function M.clear(project_id)
  for index = #notes, 1, -1 do
    local note = notes[index]
    if not project_id or note.project_id == project_id then
      if vim.api.nvim_buf_is_valid(note.buffer) then
        pcall(vim.api.nvim_buf_del_extmark, note.buffer, namespace, note.mark)
      end
      table.remove(notes, index)
    end
  end
end

function M.clear_current(done)
  done = done or function() end
  local directory = config.get().directory()
  client.context(directory, function(err, context)
    if err then
      done(err)
      return
    end
    M.clear(context.project.id)
    done(nil, context.project.id)
  end)
end

local function clear_ids(ids)
	local remove = {}
	for _, id in ipairs(ids) do remove[id] = true end
	for index = #notes, 1, -1 do
		local note = notes[index]
		if remove[note.id] then
			if vim.api.nvim_buf_is_valid(note.buffer) then
				pcall(vim.api.nvim_buf_del_extmark, note.buffer, namespace, note.mark)
			end
			table.remove(notes, index)
		end
	end
end

function M.list()
  return vim.deepcopy(notes)
end

function M.send(done)
  done = done or function() end
  local directory = config.get().directory()
  client.context(directory, function(err, context)
    if err then done(err); return end
		local prompt, format_err, included = M.format(context.project.id)
    if format_err then done(format_err); return end
		agents.send(prompt, function(send_err, result)
			if not send_err and config.get().clear_after_send then clear_ids(included) end
			done(send_err, result)
		end, { project_id = context.project.id, directory = directory })
  end)
end

function M.prompt_add()
  local start_line, end_line = vim.fn.line("."), vim.fn.line(".")
  local mode = vim.fn.mode()
  if mode:match("[vV\22]") then
    start_line, end_line = vim.fn.line("v"), vim.fn.line(".")
    if start_line > end_line then start_line, end_line = end_line, start_line end
  end
  local buffer = vim.api.nvim_create_buf(false, true)
  local width, height = math.min(76, vim.o.columns - 4), 6
  local window = vim.api.nvim_open_win(buffer, true, {
    relative = "editor", width = width, height = height,
    row = math.floor((vim.o.lines - height) / 2), col = math.floor((vim.o.columns - width) / 2),
    border = "single", title = " Review note ", title_pos = "center",
  })
  vim.bo[buffer].filetype = "markdown"
  local function save()
    local text = vim.trim(table.concat(vim.api.nvim_buf_get_lines(buffer, 0, -1, false), "\n"))
    vim.api.nvim_win_close(window, true)
    M.add(text, start_line, end_line, function(err)
      vim.notify(err and ("twt2: " .. err) or "twt2: review note added", err and vim.log.levels.ERROR or vim.log.levels.INFO)
    end)
  end
  vim.keymap.set({ "n", "i" }, "<C-s>", save, { buffer = buffer })
  vim.keymap.set("n", "q", function() vim.api.nvim_win_close(window, true) end, { buffer = buffer })
  vim.cmd("startinsert")
end

return M
