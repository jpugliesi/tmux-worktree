local client = require("twt.client")
local M = {}

local Loader = {}
Loader.__index = Loader

local omitted = "_Earlier Agent Transcript content was omitted from this preview._\n\n"

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
    text = "Could not load the Agent Transcript.\n\n" .. err
  elseif result.workspaceId ~= self.workspace_id or result.agentId ~= request.agent.id then
    text = "Could not load the Agent Transcript.\n\ntwt returned a transcript for a different Agent Session."
  elseif result.untrusted ~= true or type(result.markdown) ~= "string" then
    text = "Could not load the Agent Transcript.\n\ntwt returned an invalid transcript response."
  elseif result.markdown == "" then
    text = "This Agent Transcript is empty."
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

  if not (agent.capabilities and agent.capabilities.canReadTranscript == true) then
    render(self, request, "This Agent Session has no linked transcript.")
    self.pending = nil
    return
  end
  local cached = self:cache_get(agent.id)
  if cached then
    render(self, request, cached)
    self.pending = nil
    return
  end

  render(self, request, "Loading Agent Transcript...")
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

return M
