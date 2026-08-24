local buffers = require("twt.buffers")
local client = require("twt.client")
local config = require("twt.config")
local M = {}

local Loader = {}
Loader.__index = Loader

local omitted = "_Earlier Agent Preview content was omitted from this preview._\n\n"

local function can_preview(agent)
  local capabilities = agent.capabilities or {}
  if capabilities.canPreview ~= nil then return capabilities.canPreview == true end
  return capabilities.canReadTranscript == true
end

local function limited(text, max_bytes)
  if #text <= max_bytes then return text end
  local keep = math.max(max_bytes - #omitted, 1)
  local start = #text - keep + 1
  while start <= #text do
    local byte = text:byte(start)
    if not byte or byte < 128 or byte >= 192 then break end
    start = start + 1
  end
  local newline = text:find("\n", start, true)
  if newline and #text - newline <= keep then start = newline + 1 end
  return omitted .. text:sub(start)
end

local function can_render(loader, request)
  if loader.closed or loader.active_id ~= request.agent.id or loader.generation ~= request.generation then return false end
  local ctx = request.ctx
  if ctx.picker and ctx.picker.closed then return false end
  local win = ctx.preview and ctx.preview.win
  if win and type(win.valid) == "function" and not win:valid() then return false end
  return ctx.preview ~= nil
end

local function render(loader, request, text)
  if not can_render(loader, request) then return end
  pcall(function()
    request.ctx.preview:reset()
    request.ctx.preview:set_title(loader.title(request.agent))
    request.ctx.preview:set_lines(vim.split(text, "\n", { plain = true }))
    request.ctx.preview:highlight({ ft = "markdown" })
  end)
end

function Loader:cache_get(id)
  local value = self.cache[id]
  if not value then return nil end
  self.clock = self.clock + 1
  value.used = self.clock
  return value.text
end

function Loader:cache_put(id, text)
  if self.cache_bytes_limit <= 0 or #text > self.cache_bytes_limit then return end
  local previous = self.cache[id]
  if previous then self.cache_size = self.cache_size - previous.bytes end
  self.clock = self.clock + 1
  self.cache[id] = { text = text, bytes = #text, used = self.clock }
  self.cache_size = self.cache_size + #text
  while self.cache_size > self.cache_bytes_limit do
    local oldest_id, oldest
    for candidate_id, candidate in pairs(self.cache) do
      if not oldest or candidate.used < oldest.used then
        oldest_id, oldest = candidate_id, candidate
      end
    end
    if not oldest_id then break end
    self.cache[oldest_id] = nil
    self.cache_size = self.cache_size - oldest.bytes
  end
end

function Loader:result(request, err, result)
  if self.running ~= request then return end
  self.running = nil
  local text
  if err then
    text = "Could not load the Agent Preview.\n\n" .. err
  elseif result.workspaceId ~= self.workspace_id or result.agentId ~= request.agent.id then
    text = "Could not load the Agent Preview.\n\ntwt returned a preview for a different Agent Session."
  elseif result.untrusted ~= true or type(result.markdown) ~= "string" then
    text = "Could not load the Agent Preview.\n\ntwt returned an invalid preview response."
  elseif result.markdown == "" then
    text = "This Agent Preview is empty."
    self:cache_put(request.agent.id, text)
  else
    text = limited(result.markdown, self.max_bytes)
    self:cache_put(request.agent.id, text)
  end
  render(self, request, text)

  local pending = self.pending
  self.pending = nil
  if self.closed or not pending or self.active_id ~= pending.agent.id or self.generation ~= pending.generation then return end
  local cached = self:cache_get(pending.agent.id)
  if cached then
    render(self, pending, cached)
  else
    self:start(pending)
  end
end

function Loader:start(request)
  self.running = request
  vim.schedule(function()
    if self.closed or self.running ~= request then return end
    request.started = true
    client.request({
      "agents", "open", "--preview", request.agent.id, "--workspace", self.workspace_id,
    }, { cwd = self.directory }, function(err, result)
      self:result(request, err, result)
    end)
  end)
end

function Loader:show(ctx)
  local agent = ctx.item and (ctx.item.item or ctx.item)
  if not agent or not agent.id then return end
  self.generation = self.generation + 1
  self.active_id = agent.id
  local request = { agent = agent, ctx = ctx, generation = self.generation, started = false }

  if not can_preview(agent) then
    render(self, request, "This Agent Session has no available preview.")
    self.pending = nil
    return
  end
  local cached = self:cache_get(agent.id)
  if cached then
    render(self, request, cached)
    self.pending = nil
    return
  end

  render(self, request, "Loading Agent Preview...")
  if not self.running then
    self:start(request)
  elseif not self.running.started then
    self.running = nil
    self:start(request)
  elseif self.running.agent.id == agent.id then
    self.running.ctx = ctx
    self.running.generation = request.generation
    self.pending = nil
  else
    self.pending = request
  end
end

function Loader:close()
  self.closed = true
  self.pending = nil
end

function M.new(opts)
  return setmetatable({
    workspace_id = assert(opts.workspace_id),
    directory = assert(opts.directory),
    title = assert(opts.title),
    max_bytes = opts.max_bytes,
    cache_bytes_limit = opts.cache_bytes,
    cache = {},
    cache_size = 0,
    clock = 0,
    generation = 0,
  }, Loader)
end

local function preview_text(result, workspace_id, agent_id, max_bytes)
  if result.workspaceId ~= workspace_id or result.agentId ~= agent_id then
    return nil, "twt returned a preview for a different Agent Session"
  end
  if result.untrusted ~= true or type(result.markdown) ~= "string" then
    return nil, "twt returned an invalid preview response"
  end
  if result.markdown == "" then
    return "This Agent Preview is empty."
  end
  return limited(result.markdown, max_bytes)
end

local function reveal(bufnr)
  local wins = vim.fn.win_findbuf(bufnr)
  if wins[1] then
    vim.api.nvim_set_current_win(wins[1])
    return
  end
  local split = config.get().snapshot_split
  if split == "tab drop" then
    vim.cmd("tab sbuffer " .. bufnr)
    return
  end
  vim.cmd(split)
  vim.api.nvim_win_set_buf(0, bufnr)
end

-- Opens the Agent Preview of a live-pane Agent Session in a scratch buffer.
-- This is not a Transcript Snapshot and does not write a file.
function M.open(agent, workspace_id, directory, done)
  done = done or function() end
  client.request({
    "agents", "open", "--preview", agent.id, "--workspace", workspace_id,
  }, { cwd = directory }, function(err, result)
    if err then
      done(err)
      return
    end
    local text, text_err = preview_text(result, workspace_id, agent.id, config.get().agent_preview_max_bytes)
    if text_err then
      done(text_err)
      return
    end
    local name = "twt-preview://" .. workspace_id .. "/" .. agent.id
    local bufnr = vim.fn.bufnr(name)
    if bufnr == -1 then
      bufnr = vim.api.nvim_create_buf(true, true)
      vim.api.nvim_buf_set_name(bufnr, name)
    end
    vim.bo[bufnr].buftype = "nofile"
    vim.bo[bufnr].bufhidden = "hide"
    vim.bo[bufnr].swapfile = false
    vim.bo[bufnr].modifiable = true
    vim.bo[bufnr].readonly = false
    vim.api.nvim_buf_set_lines(bufnr, 0, -1, false, vim.split(text, "\n", { plain = true }))
    vim.bo[bufnr].filetype = "markdown"
    vim.bo[bufnr].modifiable = false
    vim.bo[bufnr].readonly = true
    vim.b[bufnr][buffers.workspace_id] = workspace_id
    vim.b[bufnr][buffers.workspace_directory] = directory
    reveal(bufnr)
    vim.cmd("normal! G")
    done(nil)
  end)
end

return M
