local buffers = require("twt.buffers")
local agent_preview = require("twt.agent_preview")
local client = require("twt.client")
local config = require("twt.config")
local input = require("twt.input")
local snapshot = require("twt.snapshot")
local M = {}

-- Every callback in this module is error-first: `done(err)` on a failure, or
-- `done(nil, result)` on success. This module never notifies the user. The
-- caller decides what to show.

local cannot_send = "the selected Agent Session cannot receive feedback"

-- One record for each Workspace: the selected Agent Session, the two
-- one-at-a-time guards, and the labels of the last listing (for the
-- statusline).
local workspaces = {}
local last_workspace_id

local function workspace(workspace_id)
  local record = workspaces[workspace_id]
  if not record then
    record = { agents = {} }
    workspaces[workspace_id] = record
  end
  return record
end

local function can(agent, capability)
  local capabilities = agent.capabilities
  if capabilities == nil then return false end
  if capabilities[capability] ~= nil then return capabilities[capability] == true end
  if capability == "canPreview" or capability == "canSnapshotTranscript" then
    return capabilities.canReadTranscript == true
  end
  return false
end

local function notify_refresh()
  vim.api.nvim_exec_autocmds("User", { pattern = "TwtRefresh" })
end

-- Keeps the label and the liveness of the last listing, for the statusline.
local function remember(workspace_id, agents)
  local known = {}
  for _, agent in ipairs(agents) do
    known[agent.id] = { label = agent.label, live = agent.status == "live" }
  end
  workspace(workspace_id).agents = known
  last_workspace_id = workspace_id
end

local function validate(agents)
  local seen = {}
  for _, agent in ipairs(agents) do
    if type(agent.id) ~= "string" or agent.id == "" then
      return "twt returned an Agent Session without an ID"
    end
    if seen[agent.id] then return "twt returned duplicate Agent Session IDs" end
    seen[agent.id] = true
  end
end

function M.status()
  local workspace_id = vim.b[buffers.workspace_id] or last_workspace_id
  local record = workspace_id and workspaces[workspace_id]
  if not record then return nil end
  local entry = record.selected_id and record.agents[record.selected_id]
  if not entry then return nil end
  return { label = entry.label, live = entry.live }
end

local function list_for(context, directory, done)
  client.request({
    "agents",
    "list",
    "--workspace",
    context.workspace.id,
    "--limit",
    tostring(config.get().max_agents),
  }, { cwd = directory }, function(err, result)
    if err then
      done(err)
      return
    end
    if result.workspaceId ~= context.workspace.id then
      done("twt returned Agent Sessions for a different Workspace")
      return
    end
    local agents = result.agents or {}
    if result.complete == false then
      local detail = result.diagnostics and result.diagnostics[1]
      done(detail and ("Agent Session discovery is incomplete: " .. detail) or "Agent Session discovery is incomplete")
      return
    end
    local validation_err = validate(agents)
    if validation_err then
      done(validation_err)
      return
    end
    remember(context.workspace.id, agents)
    done(nil, agents, context, directory)
  end)
end

local function unique_prefixes(agents)
  local prefixes = {}
  for _, agent in ipairs(agents) do
    local length = math.min(8, #agent.id)
    while length < #agent.id do
      local prefix = agent.id:sub(1, length)
      local unique = true
      for _, other in ipairs(agents) do
        if other ~= agent and other.id:sub(1, length) == prefix then
          unique = false
          break
        end
      end
      if unique then break end
      length = length + 1
    end
    prefixes[agent.id] = agent.id:sub(1, length)
  end
  return prefixes
end

local function display_time(agent)
  local value = agent.lastActivity or agent.updatedAt
  if not value or value == "" then return nil end
  local date, clock = value:match("^(%d%d%d%d%-%d%d%-%d%d)T(%d%d:%d%d):%d%dZ$")
  return date and (date .. " " .. clock .. " UTC") or value
end

local function picker_label(agent, prefixes)
  local parts = { agent.label or agent.provider or "Agent Session" }
  if agent.provider and agent.provider:lower() ~= parts[1]:lower() then parts[#parts + 1] = agent.provider end
  parts[#parts + 1] = prefixes[agent.id]
  parts[#parts + 1] = agent.status or "unknown"
  local time = display_time(agent)
  if time then parts[#parts + 1] = time end
  return table.concat(parts, " · ")
end

function M.list(done)
  client.workspace_context(function(err, context, directory)
    if err then
      done(err)
      return
    end
    list_for(context, directory, done)
  end)
end

-- Writes a new transcript snapshot for one Agent Session and opens the file.
-- It answers `done(nil, { agent = agent, path = path })`.
local select_without_snapshot
local function take_snapshot(agent, workspace_id, directory, done)
  if not can(agent, "canSnapshotTranscript") then
    done("the selected Agent Session has no linked transcript")
    return
  end
  local record = workspace(workspace_id)
  if record.snapshotting then
    done("a transcript snapshot is already in progress for this Workspace")
    return
  end
  record.snapshotting = true
  client.request({ "agents", "transcript", "snapshot", agent.id, "--workspace", workspace_id }, { cwd = directory }, function(transcript_err, transcript, error_code)
    record.snapshotting = nil
    if transcript_err then
      if error_code == "not_found" and can(agent, "canPreview") and (can(agent, "canSend") or can(agent, "canFocus")) then
        select_without_snapshot(agent, workspace_id, directory, done)
        return
      end
      done(transcript_err)
      return
    end
    if transcript.workspaceId ~= workspace_id then
      done("twt returned a transcript for a different Workspace or Agent Session")
      return
    end
    -- A discovered session ID is the provider session ID. Snapshot adopts
    -- that session and returns the new Agent Session ID.
    if transcript.agentId ~= agent.id then
      local discovered = agent.status == "discovered" and type(agent.providerSessionId) == "string" and agent.providerSessionId ~= ""
      if not discovered then
        done("twt returned a transcript for a different Workspace or Agent Session")
        return
      end
      local adopted = vim.deepcopy(agent)
      adopted.id = transcript.agentId
      agent = adopted
    end
    local path, path_err = snapshot.resolve(transcript, workspace_id)
    if not path then
      done(path_err)
      return
    end
    local _, open_err = snapshot.open(path, workspace_id, directory)
    if open_err then
      done(open_err)
      return
    end
    record.selected_id = agent.id
    notify_refresh()
    done(nil, { agent = agent, path = path })
  end)
end

-- Selects an Agent Session that has an Agent Preview but no verified
-- transcript. A discovered live pane is adopted on this first action.
select_without_snapshot = function(agent, workspace_id, directory, done)
  local function select(selected)
    if selected.workspaceId ~= workspace_id then
      done("twt returned an Agent Session for a different Workspace")
      return
    end
    local record = workspace(workspace_id)
    record.selected_id = selected.id
    record.agents[selected.id] = { label = selected.label, live = selected.status == "live" }
    notify_refresh()
    done(nil, {
      agent = selected,
      message = "selected Agent Session " .. (selected.label or selected.provider or selected.id),
    })
  end

  if agent.registration ~= "discovered" then
    select(agent)
    return
  end
  client.request({ "agents", "adopt", agent.id, "--workspace", workspace_id }, { cwd = directory }, function(err, result)
    if err then
      done(err)
      return
    end
    if type(result.agent) ~= "table" then
      done("twt returned an invalid Agent Session response")
      return
    end
    select(result.agent)
  end)
end

local function select_agent(agent, workspace_id, directory, done)
  if can(agent, "canSnapshotTranscript") then
    take_snapshot(agent, workspace_id, directory, done)
  else
    select_without_snapshot(agent, workspace_id, directory, done)
  end
end

-- Lists the Agent Sessions of the current Workspace. A verified transcript
-- selection opens a snapshot. A live-pane selection adopts and selects it.
-- A canceled selection answers `done(nil)` with no result.
function M.pick(done)
  done = done or function() end
  M.list(function(err, agents, context, directory)
    if err then
      done(err)
      return
    end
    if #agents == 0 then
      done("this Workspace has no Agent Sessions")
      return
    end
    local prefixes = unique_prefixes(agents)
    local cfg = config.get()
    local preview = agent_preview.new({
      workspace_id = context.workspace.id,
      directory = directory,
      max_bytes = cfg.agent_preview_max_bytes,
      cache_bytes = cfg.agent_preview_cache_bytes,
      title = function(agent) return "Agent Preview · " .. prefixes[agent.id] end,
    })
    config.get().select(agents, {
      prompt = "Select a twt Agent Session",
      kind = "twt_agent_session",
      format_item = function(agent) return picker_label(agent, prefixes) end,
      snacks = {
        preview = function(ctx) preview:show(ctx) end,
        layout = { preset = "default" },
      },
    }, function(agent)
      preview:close()
      if not agent then
        done(nil)
        return
      end
      select_agent(agent, context.workspace.id, directory, done)
    end)
  end)
end

local function with_selected(done, expected)
  client.workspace_context(function(err, context, directory)
    if err then
      done(err)
      return
    end
    if expected and context.workspace.id ~= expected.workspace_id then
      done("the current buffer changed to a different Workspace")
      return
    end
    list_for(context, directory, function(list_err, agents)
      if list_err then
        done(list_err)
        return
      end
      local id = workspace(context.workspace.id).selected_id
      for _, agent in ipairs(agents) do
        if agent.id == id then
          done(nil, agent, context, directory)
          return
        end
      end
      done("select an Agent Session for this Workspace first")
    end)
  end, expected and expected.directory or nil)
end

-- Sends `text` to one live Agent Session. One send at a time for each Workspace.
local function send_now(agent, workspace_id, directory, text, done)
  if not can(agent, "canSend") then
    done(cannot_send)
    return
  end
  local record = workspace(workspace_id)
  if record.sending then
    done("a review send is already in progress for this Workspace")
    return
  end
  record.sending = true
  client.request({ "agents", "send", agent.id, "--workspace", workspace_id, "--stdin" }, { cwd = directory, stdin = text }, function(send_err, result)
    record.sending = nil
    if not send_err then notify_refresh() end
    done(send_err, result)
  end)
end

local function send_text(text, done, expected)
  with_selected(function(err, agent, context, directory)
    if err then
      done(err)
      return
    end
    local workspace_id = context.workspace.id
    if can(agent, "canSend") then
      send_now(agent, workspace_id, directory, text, done)
      return
    end
    if not can(agent, "canResume") then
      done(cannot_send)
      return
    end
    config.get().confirm("The Agent Session is not live. Resume and send?", function(yes)
      if not yes then
        done(cannot_send)
        return
      end
      client.request({ "agents", "resume", agent.id }, { cwd = directory }, function(resume_err)
        if resume_err then
          done(resume_err)
          return
        end
        notify_refresh()
        -- Reads the Agent Session one more time, so the send uses the
        -- capabilities of the resumed Agent Session.
        local from = { workspace_id = workspace_id, directory = directory }
        with_selected(function(live_err, live_agent, live_context, live_directory)
          if live_err then
            done(live_err)
            return
          end
          send_now(live_agent, live_context.workspace.id, live_directory, text, done)
        end, from)
      end)
    end)
  end, expected)
end

function M.send(text, done, expected)
  done = done or function() end
  if not text or text == "" then
    done("review text is empty")
    return
  end
  send_text(text, done, expected)
end

-- Asks for free text, then sends it. A canceled window sends nothing.
function M.prompt_send(done)
  done = done or function() end
  input.open({ title = "Agent message" }, function(text)
    if not text then return end
    if text == "" then
      done("enter a message first")
      return
    end
    M.send(text, done)
  end)
end

-- Writes a new snapshot for the Agent Session that the Workspace already
-- selected. It answers `done(nil, { agent = agent, path = path })`.
function M.refresh(done)
  done = done or function() end
  with_selected(function(err, agent, context, directory)
    if err then
      done(err)
      return
    end
    take_snapshot(agent, context.workspace.id, directory, done)
  end)
end

local function action(name, capability, done)
  done = done or function() end
  local function finish(err, result)
    if not err then notify_refresh() end
    done(err, result)
  end
  with_selected(function(err, agent, _, directory)
    if err then
      finish(err)
      return
    end
    if capability and not can(agent, capability) then
      finish("the selected Agent Session cannot " .. name)
      return
    end
    client.request({ "agents", name, agent.id }, { cwd = directory }, finish)
  end)
end

function M.resume(done) action("resume", "canResume", done) end
function M.focus(done) action("focus", "canFocus", done) end

return M
