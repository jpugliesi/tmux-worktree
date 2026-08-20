local client = require("twt2.client")
local config = require("twt2.config")
local snapshot = require("twt2.snapshot")
local M = {}

local selected = {}
local selection_generation = {}
local sending = {}

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
    done(nil, result.agents or {}, context, directory)
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
      local project_id = context.project.id
      local generation = (selection_generation[project_id] or 0) + 1
      selection_generation[project_id] = generation
      if not agent.capabilities or not agent.capabilities.canReadTranscript then
        local message = "the selected Agent Session has no linked transcript"
        vim.notify("twt2: " .. message, vim.log.levels.WARN)
        if done then done(agent, message) end
        return
      end
      client.request({ "agents", "transcript", "show", agent.id, "--project", context.project.id }, { cwd = directory }, function(transcript_err, transcript)
        if selection_generation[project_id] ~= generation then
          if done then done(agent, "a newer Agent Session selection replaced this request") end
          return
        end
        if transcript_err then
          vim.notify("twt2: " .. transcript_err, vim.log.levels.ERROR)
          if done then done(agent, transcript_err) end
          return
        end
        if transcript.projectId ~= context.project.id or transcript.agentId ~= agent.id then
          local message = "twt2 returned a transcript for a different Project or Agent Session"
          vim.notify("twt2: " .. message, vim.log.levels.ERROR)
          if done then done(agent, message) end
          return
        end
        local path, write_err = snapshot.write(context.project.id, transcript.markdown)
        if not path then
          vim.notify("twt2: " .. write_err, vim.log.levels.ERROR)
          if done then done(agent, write_err) end
          return
        end
		selected[project_id] = agent.id
        local _, open_err = snapshot.open(context.project.id, directory)
        if open_err then
          vim.notify("twt2: " .. open_err, vim.log.levels.ERROR)
          if done then done(agent, open_err) end
          return
        end
        if done then done(agent, nil, path) end
      end)
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

function M.send(text, done, expected)
  done = done or function() end
  if not text or text == "" then
    done("review text is empty")
    return
  end
	with_selected(function(err, agent, context, directory)
    if err then
      done(err)
      return
    end
    if not agent.capabilities or not agent.capabilities.canSend then
      done("the selected Agent Session cannot receive feedback")
      return
    end
		local project_id = context.project.id
		if sending[project_id] then
			done("a review send is already in progress for this Project")
			return
		end
		sending[project_id] = true
		client.request({ "agents", "send", agent.id, "--project", context.project.id, "--stdin" }, { cwd = directory, stdin = text }, function(send_err, result)
			sending[project_id] = nil
			done(send_err, result)
		end)
	end, expected)
end

local function action(name, capability, done)
  local function finish(err, result)
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
