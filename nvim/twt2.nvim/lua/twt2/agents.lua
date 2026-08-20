local client = require("twt2.client")
local config = require("twt2.config")
local input = require("twt2.input")
local snapshot = require("twt2.snapshot")
local M = {}

local selected = {}
local snapshotting = {}
local sending = {}
local known = {}
local last_project_id

local function notify_refresh()
  vim.api.nvim_exec_autocmds("User", { pattern = "Twt2Refresh" })
end

-- Keeps the label and the liveness of the last listing, for the statusline.
local function remember(project_id, agents)
  local project = {}
  for _, agent in ipairs(agents) do
    project[agent.id] = { label = agent.label, live = agent.status == "live" }
  end
  known[project_id] = project
  last_project_id = project_id
end

function M.status()
  local project_id = vim.b.twt2_project_id or last_project_id
  if not project_id then return nil end
  local project = known[project_id]
  local id = selected[project_id]
  local entry = project and id and project[id]
  if not entry then return nil end
  return { label = entry.label, live = entry.live }
end

local function current(done, fixed_directory)
  local directory = fixed_directory or config.get().directory()
  client.context(directory, function(err, context)
    if err then
      done(err)
      return
    end
    done(nil, context, directory)
  end)
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
      done("twt2 returned Agent Sessions for a different Project")
      return
    end
    local agents = result.agents or {}
    remember(context.project.id, agents)
    done(nil, agents, context, directory)
  end)
end

function M.list(done)
  current(function(err, context, directory)
    if err then
      done(err)
      return
    end
    list_for(context, directory, done)
  end)
end

-- Resolves the file a snapshot response should open: the per-agent path
-- twt2 reports directly, or (only when an older twt2 omits it) the shared
-- latest.md fallback derived the way twt2 used to lay it out.
local function snapshot_path(transcript, project_id)
  if transcript.path and transcript.path ~= "" then
    return transcript.path
  end
  return snapshot.fallback_path(project_id)
end

-- Writes a new transcript snapshot for one Agent Session and opens the file.
local function take_snapshot(agent, project_id, directory, done)
  done = done or function() end
  local function fail(message, level)
    vim.notify("twt2: " .. message, level or vim.log.levels.ERROR)
    done(agent, message)
  end
  if not agent.capabilities or not agent.capabilities.canReadTranscript then
    fail("the selected Agent Session has no linked transcript", vim.log.levels.WARN)
    return
  end
  if snapshotting[project_id] then
    fail("a transcript snapshot is already in progress for this Project", vim.log.levels.WARN)
    return
  end
  snapshotting[project_id] = true
  client.request({ "agents", "transcript", "snapshot", agent.id, "--project", project_id }, { cwd = directory }, function(transcript_err, transcript)
    snapshotting[project_id] = nil
    if transcript_err then
      fail(transcript_err)
      return
    end
    if transcript.projectId ~= project_id or transcript.agentId ~= agent.id then
      fail("twt2 returned a transcript for a different Project or Agent Session")
      return
    end
    local path, path_err = snapshot_path(transcript, project_id)
    if not path then
      fail(path_err)
      return
    end
    local _, open_err = snapshot.open(path, project_id, directory)
    if open_err then
      fail(open_err)
      return
    end
    selected[project_id] = agent.id
    notify_refresh()
    done(agent, nil, path)
  end)
end

function M.pick(done)
  M.list(function(err, agents, context, directory)
    if err then
      vim.notify("twt2: " .. err, vim.log.levels.ERROR)
      if done then done(nil, err) end
      return
    end
    if #agents == 0 then
      vim.notify("twt2: this Project has no Agent Sessions", vim.log.levels.WARN)
      if done then done(nil, "no Agent Sessions") end
      return
    end
    config.get().select(agents, {
      prompt = "Select a twt2 Agent Session",
      format_item = function(agent)
        return string.format("%s · %s · %s", agent.label, agent.provider, agent.status)
      end,
    }, function(agent)
      if not agent then
        if done then done(nil) end
        return
      end
      take_snapshot(agent, context.project.id, directory, done)
    end)
  end)
end

local function with_selected(done, expected)
  current(function(err, context, directory)
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
      local id = selected[context.project.id]
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

local function send_text(text, done, expected, resumed)
  with_selected(function(err, agent, context, directory)
    if err then
      done(err)
      return
    end
    local project_id = context.project.id
    if not agent.capabilities or not agent.capabilities.canSend then
      if resumed or not agent.capabilities or not agent.capabilities.canResume then
        done("the selected Agent Session cannot receive feedback")
        return
      end
      config.get().confirm("The Agent Session is not live. Resume and send?", function(yes)
        if not yes then
          done("the selected Agent Session cannot receive feedback")
          return
        end
        client.request({ "agents", "resume", agent.id }, { cwd = directory }, function(resume_err)
          if resume_err then
            done(resume_err)
            return
          end
          notify_refresh()
          send_text(text, done, { project_id = project_id, directory = directory }, true)
        end)
      end)
      return
    end
    if sending[project_id] then
      done("a review send is already in progress for this Project")
      return
    end
    sending[project_id] = true
    client.request({ "agents", "send", agent.id, "--project", project_id, "--stdin" }, { cwd = directory, stdin = text }, function(send_err, result)
      sending[project_id] = nil
      if not send_err then notify_refresh() end
      done(send_err, result)
    end)
  end, expected)
end

function M.send(text, done, expected)
  done = done or function() end
  if not text or text == "" then
    done("review text is empty")
    return
  end
  send_text(text, done, expected, false)
end

function M.prompt_send()
  input.open({ title = "Agent message" }, function(text)
    if text == "" then
      vim.notify("twt2: enter a message first", vim.log.levels.WARN)
      return
    end
    M.send(text, function(err)
      vim.notify(err and ("twt2: " .. err) or "twt2: message sent", err and vim.log.levels.ERROR or vim.log.levels.INFO)
    end)
  end)
end

-- Writes a new snapshot for the Agent Session that the Project already selected.
function M.refresh(done)
  with_selected(function(err, agent, context, directory)
    if err then
      vim.notify("twt2: " .. err, vim.log.levels.ERROR)
      if done then done(nil, err) end
      return
    end
    take_snapshot(agent, context.project.id, directory, done)
  end)
end

local function action(name, capability, done)
  local function finish(err, result)
    if not err then notify_refresh() end
    if done then
      done(err, result)
    elseif err then
      vim.notify("twt2: " .. err, vim.log.levels.ERROR)
    end
  end
  with_selected(function(err, agent, _, directory)
    if err then
      finish(err)
      return
    end
    if capability and (not agent.capabilities or not agent.capabilities[capability]) then
      finish("the selected Agent Session cannot " .. name)
      return
    end
    client.request({ "agents", name, agent.id }, { cwd = directory }, finish)
  end)
end

function M.resume(done) action("resume", "canResume", done) end
function M.focus(done) action("focus", "canFocus", done) end

return M
