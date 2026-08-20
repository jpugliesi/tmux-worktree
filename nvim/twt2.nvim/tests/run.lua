local plugin = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(plugin)

local failures = {}
local function test(name, body)
  local ok, err = pcall(body)
  if not ok then
    failures[#failures + 1] = name .. ": " .. tostring(err)
  end
end

local calls = {}
local context = {
  schemaVersion = 1,
  project = { id = "project-1", name = "change-one" },
  repositoryName = "app",
}
local other_context = {
  schemaVersion = 1,
  project = { id = "project-2", name = "change-two" },
  repositoryName = "app",
}
local agents_response = {
  schemaVersion = 1,
  projectId = "project-1",
  agents = {
    {
      id = "agent-1",
      projectId = "project-1",
      provider = "codex",
      label = "review",
      status = "live",
      capabilities = { canResume = true, canSend = true, canFocus = true, canReadTranscript = true },
    },
  },
}
local other_agents_response = {
  schemaVersion = 1,
  projectId = "project-2",
  agents = {
    {
      id = "agent-2",
      projectId = "project-2",
      provider = "codex",
      label = "other-review",
      status = "live",
      capabilities = { canResume = true, canSend = true, canFocus = true, canReadTranscript = true },
    },
  },
}
local third_agent = {
  id = "agent-3",
  projectId = "project-2",
  provider = "codex",
  label = "other-plan",
  status = "live",
  capabilities = { canResume = true, canSend = true, canFocus = true, canReadTranscript = true },
}

local before_finish
local transcript_by_agent = {
  ["agent-1"] = "# Project one transcript\n",
  ["agent-2"] = "# Project two transcript\n",
}

local function runner(argv, opts, done)
  calls[#calls + 1] = { argv = vim.deepcopy(argv), stdin = opts.stdin, cwd = opts.cwd }
  local joined = table.concat(argv, " ")
  local value = joined:find(" context ", 1, true) and (opts.cwd == "/work/other" and other_context or context)
    or joined:find(" agents list ", 1, true) and (opts.cwd == "/work/other" and other_agents_response or agents_response)
    or joined:find(" agents transcript show ", 1, true) and {
      schemaVersion = 1,
      projectId = opts.cwd == "/work/other" and "project-2" or "project-1",
      agentId = opts.cwd == "/work/other" and "agent-2" or "agent-1",
      provider = "codex",
      repositoryName = "app",
      updatedAt = "2026-08-20T00:00:00Z",
      markdown = transcript_by_agent[opts.cwd == "/work/other" and "agent-2" or "agent-1"],
    }
    or { schemaVersion = 1, status = "sent", agentId = "agent-1" }
  if before_finish then before_finish(joined) end
  done({ code = 0, stdout = vim.json.encode(value), stderr = "" })
end

require("twt2").setup({
  command = "/test/twt2",
  default_keymaps = false,
  runner = runner,
  directory = function()
    return "/work/app"
  end,
  snapshot_root = vim.fn.tempname(),
  select = function(items, _, done)
    done(items[1])
  end,
})

test("lists and selects Agents through exact Project context", function()
  local selected
  require("twt2").agents.pick(function(agent)
    selected = agent
  end)
  assert(selected.id == "agent-1")
  assert(table.concat(calls[1].argv, " ") == "/test/twt2 context --directory /work/app --output json")
  assert(table.concat(calls[2].argv, " ") == "/test/twt2 agents list --project project-1 --limit 40 --output json")
end)

test("revalidates the selected Agent and sends feedback on standard input", function()
  local ok
  require("twt2").agents.send("review text", function(err)
    assert(err == nil)
    ok = true
  end)
  assert(ok)
  local sent = calls[#calls]
  assert(table.concat(sent.argv, " ") == "/test/twt2 agents send agent-1 --project project-1 --stdin --output json")
  assert(sent.stdin == "review text")
end)

test("uses extmarks and repository names for current review lines", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/.git", "p")
  vim.fn.mkdir(root .. "/src", "p")
  local path = root .. "/src/file.go"
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, path)
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "two", "three" })
  vim.api.nvim_set_current_buf(buffer)
  context.repositoryName = "app"
  require("twt2.config").get().directory = function()
    return root .. "/src"
  end
  local added
  require("twt2").review.add("change this", 2, 2, function(err)
    assert(err == nil)
    added = true
  end)
  assert(added)
  vim.api.nvim_buf_set_lines(buffer, 0, 0, false, { "inserted" })
  local prompt = require("twt2").review.format("project-1")
  assert(prompt:find("app:src/file.go#L3\n", 1, true))
  assert(not prompt:find("#L3-4", 1, true))
  assert(prompt:find("two", 1, true))
  assert(not prompt:find("three", 1, true))
end)

test("keeps a review send in its captured Project when the current buffer changes", function()
  local directory = "/work/other"
  require("twt2.config").get().directory = function() return directory end
  require("twt2").agents.pick(function(agent) assert(agent.id == "agent-2") end)
  directory = "/work/app"
  local changed = false
  before_finish = function(joined)
    if not changed and joined:find(" context ", 1, true) then
      changed = true
      directory = "/work/other"
    end
  end
  local sent
  require("twt2").review.send(function(err)
    assert(err == nil, err)
    sent = calls[#calls]
  end)
  before_finish = nil
  assert(sent)
  assert(table.concat(sent.argv, " "):find("agents send agent%-1"))
  assert(sent.cwd == "/work/app")
end)

test("writes and reopens a private latest transcript for each Project", function()
  local snapshot_root = vim.fn.tempname()
  local directory = "/work/app"
  require("twt2.config").get().snapshot_root = snapshot_root
  require("twt2.config").get().directory = function() return directory end

  local first_path
  require("twt2").agents.pick(function(agent, err, path)
    assert(err == nil, err)
    assert(agent.id == "agent-1")
    first_path = path
  end)
  directory = "/work/other"
  local second_path
  require("twt2").agents.pick(function(agent, err, path)
    assert(err == nil, err)
    assert(agent.id == "agent-2")
    second_path = path
  end)

  assert(first_path == snapshot_root .. "/project-1/latest.md")
  assert(second_path == snapshot_root .. "/project-2/latest.md")
  assert(first_path ~= second_path)
  assert(table.concat(vim.fn.readfile(first_path), "\n") == "# Project one transcript")
  assert(table.concat(vim.fn.readfile(second_path), "\n") == "# Project two transcript")
  assert(vim.uv.fs_stat(first_path).mode % 512 == 384)
  assert(vim.uv.fs_stat(vim.fs.dirname(first_path)).mode % 512 == 448)

  transcript_by_agent["agent-1"] = "# Project one transcript, refreshed\n"
  directory = "/work/app"
  require("twt2").agents.pick(function(_, err) assert(err == nil, err) end)
  assert(table.concat(vim.fn.readfile(first_path), "\n") == "# Project one transcript, refreshed")
  assert(table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), "\n") == "# Project one transcript, refreshed")
  assert(vim.bo.modifiable == false)
  assert(vim.bo.readonly == true)

  require("twt2.snapshot").open("project-1")
  assert(vim.fn.resolve(vim.api.nvim_buf_get_name(0)) == vim.fn.resolve(first_path))
end)

test("commits only the newest successful Agent Session selection", function()
  other_agents_response.agents[2] = third_agent
  local directory = "/work/other"
  local choice = 1
  require("twt2.config").get().directory = function() return directory end
  require("twt2.config").get().select = function(items, _, done) done(items[choice]) end
  local pending = {}
  require("twt2.config").get().runner = function(argv, opts, done)
    local joined = table.concat(argv, " ")
    if joined:find(" agents transcript show ", 1, true) then
      pending[#pending + 1] = { argv = argv, opts = opts, done = done }
    else
      runner(argv, opts, done)
    end
  end

  local first_error
  require("twt2").agents.pick(function(_, err) first_error = err end)
  choice = 2
  local second_done = false
  require("twt2").agents.pick(function(agent, err)
    assert(err == nil, err)
    assert(agent.id == "agent-3")
    second_done = true
  end)
  assert(#pending == 2)
  pending[2].done({
    code = 0,
    stdout = vim.json.encode({
      schemaVersion = 1, projectId = "project-2", agentId = "agent-3",
      provider = "codex", repositoryName = "app", updatedAt = "2026-08-20T00:00:00Z",
      markdown = "# Newest selection\n",
    }),
    stderr = "",
  })
  pending[1].done({
    code = 0,
    stdout = vim.json.encode({
      schemaVersion = 1, projectId = "project-2", agentId = "agent-2",
      provider = "codex", repositoryName = "app", updatedAt = "2026-08-20T00:00:00Z",
      markdown = "# Stale selection\n",
    }),
    stderr = "",
  })
  assert(second_done)
  assert(first_error and first_error:find("newer Agent Session selection", 1, true))
  local path = require("twt2.snapshot").path("project-2")
  assert(table.concat(vim.fn.readfile(path), "\n") == "# Newest selection")

  require("twt2.config").get().runner = runner
  require("twt2.config").get().select = function(items, _, done) done(items[1]) end
end)

test("keeps the new Agent selection when its snapshot cannot open", function()
	local old_directory = require("twt2.config").get().directory
	require("twt2.config").get().directory = function() return "/work/app" end
  local added = vim.deepcopy(agents_response.agents[1])
  added.id = "agent-open-fails"
  added.label = "open-fails"
  agents_response.agents[#agents_response.agents + 1] = added
  local old_select = require("twt2.config").get().select
  require("twt2.config").get().select = function(items, _, done) done(items[#items]) end
  local old_runner = require("twt2.config").get().runner
  require("twt2.config").get().runner = function(argv, opts, done)
    if table.concat(argv, " "):find(" agents transcript show ", 1, true) then
      done({ code = 0, stdout = vim.json.encode({
        schemaVersion = 1,
        projectId = "project-1",
        agentId = "agent-open-fails",
        provider = "codex",
        repositoryName = "app",
        updatedAt = "2026-08-20T00:00:00Z",
        markdown = "# New file\n",
      }), stderr = "" })
    else
      runner(argv, opts, done)
    end
  end
  local snapshot = require("twt2.snapshot")
  local old_open = snapshot.open
  snapshot.open = function() return nil, "injected open failure" end
  local pick_error
  require("twt2").agents.pick(function(_, err) pick_error = err end)
  assert(pick_error == "injected open failure")
  snapshot.open = old_open
  require("twt2.config").get().runner = old_runner
  require("twt2.config").get().select = old_select

  local send_argv
  require("twt2.config").get().runner = function(argv, opts, done)
    if table.concat(argv, " "):find(" agents send ", 1, true) then
      send_argv = table.concat(argv, " ")
      done({ code = 0, stdout = vim.json.encode({ schemaVersion = 1, status = "sent", agentId = "agent-open-fails" }), stderr = "" })
    else
      runner(argv, opts, done)
    end
  end
  require("twt2").agents.send("review", function(err) assert(err == nil, err) end)
  assert(send_argv and send_argv:find("agents send agent%-open%-fails"))
  require("twt2.config").get().runner = old_runner
	require("twt2.config").get().select = function(items, _, done) done(items[1]) end
	require("twt2").agents.pick(function(_, err) assert(err == nil, err) end)
	require("twt2.config").get().directory = old_directory
	require("twt2.config").get().select = old_select
  table.remove(agents_response.agents)
end)

test("allows one feedback send per Project at the same time", function()
  local directory = "/work/app"
  require("twt2.config").get().directory = function() return directory end
  local pending = {}
  require("twt2.config").get().runner = function(argv, opts, done)
    if table.concat(argv, " "):find(" agents send ", 1, true) then
      pending[#pending + 1] = { argv = argv, opts = opts, done = done }
    else
      runner(argv, opts, done)
    end
  end

  local first_done = false
  require("twt2").agents.send("first", function(err)
    assert(err == nil, err)
    first_done = true
  end)
  assert(#pending == 1)
  local duplicate_error
  require("twt2").agents.send("duplicate", function(err) duplicate_error = err end)
  assert(duplicate_error and duplicate_error:find("already in progress", 1, true))
  assert(#pending == 1)

  directory = "/work/other"
  local second_done = false
  require("twt2").agents.send("second", function(err)
    assert(err == nil, err)
    second_done = true
  end)
  assert(#pending == 2)
  pending[1].done({ code = 0, stdout = vim.json.encode({ schemaVersion = 1, status = "sent", agentId = "agent-1" }), stderr = "" })
  pending[2].done({ code = 0, stdout = vim.json.encode({ schemaVersion = 1, status = "sent", agentId = "agent-3" }), stderr = "" })
  assert(first_done and second_done)
  require("twt2.config").get().runner = runner
end)

test("rejects an unsupported JSON schema", function()
  local client = require("twt2.client")
  local old = context.schemaVersion
  context.schemaVersion = 2
  local received
  client.context("/work/app", function(err)
    received = err
  end)
  context.schemaVersion = old
  assert(received and received:find("schema version", 1, true))
end)

if #failures > 0 then
  error(table.concat(failures, "\n"))
end
print("twt2.nvim tests: ok")
