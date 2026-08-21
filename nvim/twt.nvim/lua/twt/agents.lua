local buffers = require("twt.buffers")
local client = require("twt.client")
local config = require("twt.config")
local input = require("twt.input")
local snapshot = require("twt.snapshot")
local M = {}

-- Every callback in this module is error-first: `done(err)` on a failure, or
-- `done(nil, result)` on success. This module never notifies the user. The
-- caller decides what to show.

local cannot_send = "the selected Agent Session cannot receive feedback"

-- One record for each Project: the selected Agent Session, the two
-- one-at-a-time guards, and the labels of the last listing (for the
-- statusline).
local projects = {}
local last_project_id

local function project(project_id)
  local record = projects[project_id]
  if not record then
    record = { agents = {} }
    projects[project_id] = record
  end
  return record
end

local function can(agent, capability)
  local capabilities = agent.capabilities
  return capabilities ~= nil and capabilities[capability] == true
end

local function notify_refresh()
  vim.api.nvim_exec_autocmds("User", { pattern = "TwtRefresh" })
end

-- Keeps the label and the liveness of the last listing, for the statusline.
local function remember(project_id, agents)
  local known = {}
  for _, agent in ipairs(agents) do
    known[agent.id] = { label = agent.label, live = agent.status == "live" }
  end
  project(project_id).agents = known
  last_project_id = project_id
end

function M.status()
  local project_id = vim.b[buffers.project_id] or last_project_id
  local record = project_id and projects[project_id]
  if not record then return nil end
  local entry = record.selected_id and record.agents[record.selected_id]
  if not entry then return nil end
  return { label = entry.label, live = entry.live }
end

local function list_for(context, directory, done)
  client.request({
    "agents",
    "list",
    "--project",
    context.project.id,
    "--limit",
    tostring(config.get().max_agents),
  }, { cwd = directory }, function(err, result)
    if err then
      done(err)
      return
    end
    if result.projectId ~= context.project.id then
      done("twt returned Agent Sessions for a different Project")
      return
    end
    local agents = result.agents or {}
    remember(context.project.id, agents)
    done(nil, agents, context, directory)
  end)
end

function M.list(done)
  client.project_context(function(err, context, directory)
    if err then
      done(err)
      return
    end
    list_for(context, directory, done)
  end)
end

-- Writes a new transcript snapshot for one Agent Session and opens the file.
-- It answers `done(nil, { agent = agent, path = path })`.
local function take_snapshot(agent, project_id, directory, done)
  if not can(agent, "canReadTranscript") then
    done("the selected Agent Session has no linked transcript")
    return
  end
  local record = project(project_id)
  if record.snapshotting then
    done("a transcript snapshot is already in progress for this Project")
    return
  end
  record.snapshotting = true
  client.request({ "agents", "transcript", "snapshot", agent.id, "--project", project_id }, { cwd = directory }, function(transcript_err, transcript)
    record.snapshotting = nil
    if transcript_err then
      done(transcript_err)
      return
    end
    if transcript.projectId ~= project_id then
      done("twt returned a transcript for a different Project or Agent Session")
      return
    end
    -- A discovered session ID is the provider session ID. Snapshot adopts
    -- that session and returns the new Agent Session ID.
    if transcript.agentId ~= agent.id then
      local discovered = agent.status == "discovered" and agent.providerSessionId == agent.id
      if not discovered then
        done("twt returned a transcript for a different Project or Agent Session")
        return
      end
      local adopted = vim.deepcopy(agent)
      adopted.id = transcript.agentId
      agent = adopted
    end
    local path, path_err = snapshot.resolve(transcript, project_id)
    if not path then
      done(path_err)
      return
    end
    local _, open_err = snapshot.open(path, project_id, directory)
    if open_err then
      done(open_err)
      return
    end
    record.selected_id = agent.id
    notify_refresh()
    done(nil, { agent = agent, path = path })
  end)
end

-- Lists the Agent Sessions of the current Project, then writes and opens the
-- transcript snapshot of the selected one. A canceled selection answers
-- `done(nil)` with no result.
function M.pick(done)
  done = done or function() end
  M.list(function(err, agents, context, directory)
    if err then
      done(err)
      return
    end
    if #agents == 0 then
      done("this Project has no Agent Sessions")
      return
    end
    config.get().select(agents, {
      prompt = "Select a twt Agent Session",
      format_item = function(agent)
        return string.format("%s · %s · %s", agent.label, agent.provider, agent.status)
      end,
    }, function(agent)
      if not agent then
        done(nil)
        return
      end
      take_snapshot(agent, context.project.id, directory, done)
    end)
  end)
end

local function with_selected(done, expected)
  client.project_context(function(err, context, directory)
    if err then
      done(err)
      return
    end
    if expected and context.project.id ~= expected.project_id then
      done("the current buffer changed to a different Project")
      return
    end
    list_for(context, directory, function(list_err, agents)
      if list_err then
        done(list_err)
        return
      end
      local id = project(context.project.id).selected_id
      for _, agent in ipairs(agents) do
        if agent.id == id then
          done(nil, agent, context, directory)
          return
        end
      end
      done("select an Agent Session for this Project first")
    end)
  end, expected and expected.directory or nil)
end

-- Sends `text` to one live Agent Session. One send at a time for each Project.
local function send_now(agent, project_id, directory, text, done)
  if not can(agent, "canSend") then
    done(cannot_send)
    return
  end
  local record = project(project_id)
  if record.sending then
    done("a review send is already in progress for this Project")
    return
  end
  record.sending = true
  client.request({ "agents", "send", agent.id, "--project", project_id, "--stdin" }, { cwd = directory, stdin = text }, function(send_err, result)
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
    local project_id = context.project.id
    if can(agent, "canSend") then
      send_now(agent, project_id, directory, text, done)
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
        local from = { project_id = project_id, directory = directory }
        with_selected(function(live_err, live_agent, live_context, live_directory)
          if live_err then
            done(live_err)
            return
          end
          send_now(live_agent, live_context.project.id, live_directory, text, done)
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

-- Writes a new snapshot for the Agent Session that the Project already
-- selected. It answers `done(nil, { agent = agent, path = path })`.
function M.refresh(done)
  done = done or function() end
  with_selected(function(err, agent, context, directory)
    if err then
      done(err)
      return
    end
    take_snapshot(agent, context.project.id, directory, done)
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
