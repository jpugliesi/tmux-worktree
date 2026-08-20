local plugin = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(plugin)

local failures = {}
local initial_state = vim.fn.tempname()
vim.env.TWT_STATE_DIR = initial_state
local function test(name, body)
  local ok, err = pcall(body)
  if not ok then
    failures[#failures + 1] = name .. ": " .. tostring(err)
  end
end

-- Sets the config fields of `overrides` for `body` only, then puts the old
-- values back. It also puts them back when `body` fails.
local function with_config(overrides, body)
  local config = require("twt.config").get()
  local saved = {}
  for key in pairs(overrides) do saved[key] = config[key] end
  for key, value in pairs(overrides) do config[key] = value end
  local ok, err = pcall(body)
  for key in pairs(overrides) do config[key] = saved[key] end
  if not ok then error(err, 0) end
end

local function fixed_directory(directory)
  return function() return directory end
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

-- Mirrors twt's real layout: $STATE/snapshots/projects/<projectID>/agents/<agentID>.md
local function agent_snapshot_path(project_id, agent_id)
  return vim.env.TWT_STATE_DIR .. "/snapshots/projects/" .. project_id .. "/agents/" .. agent_id .. ".md"
end

local function save_snapshot(project_id, agent_id, markdown)
  local path = agent_snapshot_path(project_id, agent_id)
  vim.fn.mkdir(vim.fs.dirname(path), "p")
  assert(vim.uv.fs_chmod(vim.fs.dirname(path), 448))
  assert(vim.fn.writefile(vim.split(markdown, "\n", { plain = true }), path, "b") == 0)
  assert(vim.uv.fs_chmod(path, 384))
  return path
end

local function runner(argv, opts, done)
  calls[#calls + 1] = { argv = vim.deepcopy(argv), stdin = opts.stdin, cwd = opts.cwd }
  local joined = table.concat(argv, " ")
  local snapshot_agent = opts.cwd == "/work/other" and "agent-2" or "agent-1"
  local value = joined:find(" context ", 1, true) and (opts.cwd == "/work/other" and other_context or context)
    or joined:find(" agents list ", 1, true) and (opts.cwd == "/work/other" and other_agents_response or agents_response)
    or joined:find(" agents transcript snapshot ", 1, true) and {
      schemaVersion = 1,
      projectId = opts.cwd == "/work/other" and "project-2" or "project-1",
      agentId = snapshot_agent,
      provider = "codex",
      repositoryName = "app",
      updatedAt = "2026-08-20T00:00:00Z",
      status = "applied",
    }
    or { schemaVersion = 1, status = "sent", agentId = "agent-1" }
  if joined:find(" agents transcript snapshot ", 1, true) then
    value.path = save_snapshot(value.projectId, snapshot_agent, transcript_by_agent[snapshot_agent])
  end
  if before_finish then before_finish(joined) end
  done({ code = 0, stdout = vim.json.encode(value), stderr = "" })
end

require("twt").setup({
  command = "/test/twt",
  default_keymaps = false,
  runner = runner,
  directory = function()
    return "/work/app"
  end,
  select = function(items, _, done)
    done(items[1])
  end,
})

test("lists and selects Agents through exact Project context", function()
  local selected
  require("twt").agents.pick(function(err, result)
    assert(err == nil, err)
    selected = result.agent
  end)
  assert(selected.id == "agent-1")
  assert(table.concat(calls[1].argv, " ") == "/test/twt context --directory /work/app --output json")
  assert(table.concat(calls[2].argv, " ") == "/test/twt agents list --project project-1 --limit 40 --output json")
end)

test("revalidates the selected Agent and sends feedback on standard input", function()
  local ok
  require("twt").agents.send("review text", function(err)
    assert(err == nil)
    ok = true
  end)
  assert(ok)
  local sent = calls[#calls]
  assert(table.concat(sent.argv, " ") == "/test/twt agents send agent-1 --project project-1 --stdin --output json")
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
  require("twt.config").get().directory = function()
    return root .. "/src"
  end
  local added
  require("twt").review.add("change this", 2, 2, function(err)
    assert(err == nil)
    added = true
  end)
  assert(added)
  vim.api.nvim_buf_set_lines(buffer, 0, 0, false, { "inserted" })
  local format_err, batch = require("twt").review.format("project-1")
  assert(format_err == nil, format_err)
  assert(#batch.note_ids == 1)
  assert(batch.text:find("app:src/file.go#L3\n", 1, true))
  assert(not batch.text:find("#L3-4", 1, true))
  assert(batch.text:find("two", 1, true))
  assert(not batch.text:find("three", 1, true))
end)

test("keeps a review send in its captured Project when the current buffer changes", function()
  local directory = "/work/other"
  require("twt.config").get().directory = function() return directory end
  require("twt").agents.pick(function(err, result)
    assert(err == nil, err)
    assert(result.agent.id == "agent-2")
  end)
  directory = "/work/app"
  local changed = false
  before_finish = function(joined)
    if not changed and joined:find(" context ", 1, true) then
      changed = true
      directory = "/work/other"
    end
  end
  local sent
  require("twt").review.send(function(err)
    assert(err == nil, err)
    sent = calls[#calls]
  end)
  before_finish = nil
  assert(sent)
  assert(table.concat(sent.argv, " "):find("agents send agent%-1"))
  assert(sent.cwd == "/work/app")
end)

test("writes and reopens a private latest transcript for each Project", function()
  local state_root = vim.fn.tempname()
  local snapshot_root = state_root .. "/snapshots/projects"
  local directory = "/work/app"
  vim.env.TWT_STATE_DIR = state_root
  require("twt.config").get().directory = function() return directory end

  local first_path
  require("twt").agents.pick(function(err, result)
    assert(err == nil, err)
    assert(result.agent.id == "agent-1")
    first_path = result.path
  end)
  directory = "/work/other"
  local second_path
  require("twt").agents.pick(function(err, result)
    assert(err == nil, err)
    assert(result.agent.id == "agent-2")
    second_path = result.path
  end)

  assert(first_path == snapshot_root .. "/project-1/agents/agent-1.md")
  assert(second_path == snapshot_root .. "/project-2/agents/agent-2.md")
  assert(first_path ~= second_path)
  assert(table.concat(vim.fn.readfile(first_path), "\n") == "# Project one transcript")
  assert(table.concat(vim.fn.readfile(second_path), "\n") == "# Project two transcript")
  assert(vim.uv.fs_stat(first_path).mode % 512 == 384)
  assert(vim.uv.fs_stat(vim.fs.dirname(first_path)).mode % 512 == 448)

  transcript_by_agent["agent-1"] = "# Project one transcript, refreshed\n"
  directory = "/work/app"
  require("twt").agents.pick(function(err) assert(err == nil, err) end)
  assert(table.concat(vim.fn.readfile(first_path), "\n") == "# Project one transcript, refreshed")
  assert(table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), "\n") == "# Project one transcript, refreshed")
  assert(vim.bo.modifiable == false)
  assert(vim.bo.readonly == true)

  require("twt.snapshot").open(first_path, "project-1")
  assert(vim.fn.resolve(vim.api.nvim_buf_get_name(0)) == vim.fn.resolve(first_path))
end)

test("serializes transcript snapshots for one Project", function()
  other_agents_response.agents[2] = third_agent
  local choice = 1
  local pending = {}
  with_config({
    directory = fixed_directory("/work/other"),
    select = function(items, _, done) done(items[choice]) end,
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents transcript snapshot ", 1, true) then
        pending[#pending + 1] = { argv = argv, opts = opts, done = done }
      else
        runner(argv, opts, done)
      end
    end,
  }, function()
    local first_done = false
    require("twt").agents.pick(function(err)
      assert(err == nil, err)
      first_done = true
    end)
    choice = 2
    local blocked_error
    require("twt").agents.pick(function(err) blocked_error = err end)
    assert(blocked_error and blocked_error:find("already in progress", 1, true))
    assert(#pending == 1)
    local agent2_path = save_snapshot("project-2", "agent-2", "# First selection\n")
    pending[1].done({
      code = 0,
      stdout = vim.json.encode({
        schemaVersion = 1, projectId = "project-2", agentId = "agent-2",
        provider = "codex", repositoryName = "app", updatedAt = "2026-08-20T00:00:00Z",
        status = "applied", path = agent2_path,
      }),
      stderr = "",
    })
    assert(first_done)

    local second_done = false
    require("twt").agents.pick(function(err, result)
      assert(err == nil, err)
      assert(result.agent.id == "agent-3")
      second_done = true
    end)
    assert(#pending == 2)
    local agent3_path = save_snapshot("project-2", "agent-3", "# Second selection\n")
    pending[2].done({
      code = 0,
      stdout = vim.json.encode({
        schemaVersion = 1, projectId = "project-2", agentId = "agent-3",
        provider = "codex", repositoryName = "app", updatedAt = "2026-08-20T00:00:00Z",
        status = "applied", path = agent3_path,
      }),
      stderr = "",
    })
    assert(second_done)
    assert(agent3_path == agent_snapshot_path("project-2", "agent-3"))
    assert(table.concat(vim.fn.readfile(agent3_path), "\n") == "# Second selection")
  end)
end)

test("keeps the old Agent selection when a new snapshot cannot open", function()
  local added = vim.deepcopy(agents_response.agents[1])
  added.id = "agent-open-fails"
  added.label = "open-fails"
  agents_response.agents[#agents_response.agents + 1] = added
  local snapshot = require("twt.snapshot")
  local old_open = snapshot.open

  with_config({
    directory = fixed_directory("/work/app"),
    select = function(items, _, done) done(items[#items]) end,
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents transcript snapshot ", 1, true) then
        local open_fails_path = save_snapshot("project-1", "agent-open-fails", "# New file\n")
        done({ code = 0, stdout = vim.json.encode({
          schemaVersion = 1,
          projectId = "project-1",
          agentId = "agent-open-fails",
          provider = "codex",
          repositoryName = "app",
          updatedAt = "2026-08-20T00:00:00Z",
          status = "applied", path = open_fails_path,
        }), stderr = "" })
      else
        runner(argv, opts, done)
      end
    end,
  }, function()
    snapshot.open = function() return nil, "injected open failure" end
    local pick_error
    require("twt").agents.pick(function(err) pick_error = err end)
    snapshot.open = old_open
    assert(pick_error == "injected open failure")
  end)

  -- The Project still sends to the Agent Session of the last good snapshot.
  local send_argv
  with_config({
    directory = fixed_directory("/work/app"),
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents send ", 1, true) then
        send_argv = table.concat(argv, " ")
        done({ code = 0, stdout = vim.json.encode({ schemaVersion = 1, status = "sent", agentId = "agent-1" }), stderr = "" })
      else
        runner(argv, opts, done)
      end
    end,
  }, function()
    require("twt").agents.send("review", function(err) assert(err == nil, err) end)
  end)
  assert(send_argv and send_argv:find("agents send agent%-1"), send_argv)

  with_config({ directory = fixed_directory("/work/app") }, function()
    require("twt").agents.pick(function(err) assert(err == nil, err) end)
  end)
  table.remove(agents_response.agents)
end)

test("allows one feedback send per Project at the same time", function()
  local directory = "/work/app"
  local pending = {}
  with_config({
    directory = function() return directory end,
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents send ", 1, true) then
        pending[#pending + 1] = { argv = argv, opts = opts, done = done }
      else
        runner(argv, opts, done)
      end
    end,
  }, function()
    local first_done = false
    require("twt").agents.send("first", function(err)
      assert(err == nil, err)
      first_done = true
    end)
    assert(#pending == 1)
    local duplicate_error
    require("twt").agents.send("duplicate", function(err) duplicate_error = err end)
    assert(duplicate_error and duplicate_error:find("already in progress", 1, true))
    assert(#pending == 1)

    directory = "/work/other"
    local second_done = false
    require("twt").agents.send("second", function(err)
      assert(err == nil, err)
      second_done = true
    end)
    assert(#pending == 2)
    pending[1].done({ code = 0, stdout = vim.json.encode({ schemaVersion = 1, status = "sent", agentId = "agent-1" }), stderr = "" })
    pending[2].done({ code = 0, stdout = vim.json.encode({ schemaVersion = 1, status = "sent", agentId = "agent-3" }), stderr = "" })
    assert(first_done and second_done)
  end)
end)

test("rejects an unsupported JSON schema", function()
  local client = require("twt.client")
  local old = context.schemaVersion
  context.schemaVersion = 2
  local received
  client.context("/work/app", function(err)
    received = err
  end)
  context.schemaVersion = old
  assert(received and received:find("schema version", 1, true))
end)

test("falls back to the shared latest.md when a snapshot response has no path", function()
  -- An empty response is a response with no `path`: an older twt binary.
  local function legacy_path()
    return require("twt.snapshot").resolve({}, "project-1")
  end
  local old_state = vim.env.TWT_STATE_DIR
  local old_xdg = vim.env.XDG_STATE_HOME
  vim.env.TWT_STATE_DIR = "/explicit/state/twt"
  assert(legacy_path() == "/explicit/state/twt/snapshots/projects/project-1/latest.md")
  vim.env.TWT_STATE_DIR = nil
  vim.env.XDG_STATE_HOME = "/xdg/state"
  assert(legacy_path() == "/xdg/state/twt/snapshots/projects/project-1/latest.md")
  vim.env.TWT_STATE_DIR = old_state
  vim.env.XDG_STATE_HOME = old_xdg
  assert(require("twt.snapshot").resolve({ path = "/reported/path.md" }, "project-1") == "/reported/path.md")

  local fallback_opened
  with_config({
    directory = fixed_directory("/work/app"),
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents transcript snapshot ", 1, true) then
        local fallback = legacy_path()
        vim.fn.mkdir(vim.fs.dirname(fallback), "p")
        assert(vim.fn.writefile({ "# Legacy transcript" }, fallback, "b") == 0)
        done({ code = 0, stdout = vim.json.encode({
          schemaVersion = 1,
          projectId = "project-1",
          agentId = "agent-1",
          provider = "codex",
          repositoryName = "app",
          updatedAt = "2026-08-20T00:00:00Z",
          status = "applied",
          -- no `path`: simulates an older twt binary
        }), stderr = "" })
      else
        runner(argv, opts, done)
      end
    end,
  }, function()
    require("twt").agents.pick(function(err, result)
      assert(err == nil, err)
      fallback_opened = result.path
    end)
  end)
  assert(fallback_opened == legacy_path())
  assert(vim.fn.resolve(vim.api.nvim_buf_get_name(0)) == vim.fn.resolve(fallback_opened))
end)

test("registers one command for each action, without the default mappings", function()
  local commands = vim.api.nvim_get_commands({})
  local names = {
    "TwtAgents", "TwtNote", "TwtReview", "TwtSend",
    "TwtNotes", "TwtResume", "TwtFocus", "TwtRefresh", "TwtClear",
  }
  for _, name in ipairs(names) do
    assert(commands[name], name .. " is missing")
  end
  assert(vim.fn.maparg("<leader>ars", "n") == "")
end)

local function save_keymap(buffer)
  for _, map in ipairs(vim.api.nvim_buf_get_keymap(buffer, "n")) do
    if map.callback and map.lhs:lower():find("c%-s") then return map.callback end
  end
end

local function cancel_keymap(buffer)
  for _, map in ipairs(vim.api.nvim_buf_get_keymap(buffer, "n")) do
    if map.callback and map.lhs == "q" then return map.callback end
  end
end

test("answers the input window one time only", function()
  local answers = {}
  local buffer = require("twt.input").open({ title = "note" }, function(text)
    answers[#answers + 1] = text
  end)
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one line" })
  local save, cancel = save_keymap(buffer), cancel_keymap(buffer)
  save()
  cancel()
  assert(#answers == 1 and answers[1] == "one line", vim.inspect(answers))
end)

test("sends free text from the message window", function()
  require("twt.config").get().directory = function() return "/work/app" end
  require("twt").agents.pick(function(err) assert(err == nil, err) end)
  local sent_error = "not called"
  require("twt").agents.prompt_send(function(err) sent_error = err end)
  local buffer = vim.api.nvim_get_current_buf()
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "please add a test" })
  local save = save_keymap(buffer)
  assert(save, "the message window has no save mapping")
  save()
  assert(sent_error == nil, sent_error)
  local sent = calls[#calls]
  assert(table.concat(sent.argv, " "):find("agents send agent%-1"))
  assert(sent.stdin == "please add a test")
  assert(not vim.api.nvim_buf_is_valid(buffer), "the message buffer must not stay")
end)

test("cancels the message window one time and sends nothing", function()
  require("twt.config").get().directory = function() return "/work/app" end
  local answers = 0
  require("twt").agents.prompt_send(function(err)
    answers = answers + 1
    assert(err == nil, err)
  end)
  local buffer = vim.api.nvim_get_current_buf()
  local before = #calls
  local cancel = cancel_keymap(buffer)
  assert(cancel, "the message window has no cancel mapping")
  cancel()
  assert(answers == 0, "a canceled window must send no message and no error")
  assert(#calls == before)
  assert(not vim.api.nvim_buf_is_valid(buffer), "the message buffer must not stay")
end)

test("lists a review note and deletes it", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/src", "p")
  vim.fn.mkdir(root .. "/.git", "p")
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, root .. "/src/other.go")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "two", "three" })
  vim.api.nvim_set_current_buf(buffer)
  require("twt.config").get().directory = function() return root .. "/src" end
  local review = require("twt").review
  review.clear("project-1")
  review.add("first note", 2, 2, function(err) assert(err == nil, err) end)
  review.add("second note", 3, 3, function(err) assert(err == nil, err) end)

  local labels
  local choices = { 2, "Go to the line" }
  with_config({
    select = function(items, opts, done)
      local choice = table.remove(choices, 1)
      if type(choice) == "number" then
        labels = vim.tbl_map(opts.format_item, items)
        done(items[choice])
      else
        for _, item in ipairs(items) do
          if item == choice then done(item); return end
        end
        done(nil)
      end
    end,
  }, function()
    vim.api.nvim_win_set_cursor(0, { 1, 0 })
    review.prompt_notes(function(err) assert(err == nil, err) end)
    assert(#labels == 2)
    assert(labels[1]:find("src/other.go:2 · first note", 1, true), labels[1])
    assert(vim.api.nvim_win_get_cursor(0)[1] == 3)

    choices = { 1, "Delete" }
    review.prompt_notes(function(err) assert(err == nil, err) end)
    local left = {}
    for _, note in ipairs(review.list()) do
      if note.project_id == "project-1" then left[#left + 1] = note.comment end
    end
    assert(#left == 1 and left[1] == "second note", table.concat(left, ","))
  end)
  review.clear("project-1")
end)

test("writes a new snapshot without the picker", function()
  require("twt.config").get().directory = function() return "/work/app" end
  require("twt").agents.pick(function(err) assert(err == nil, err) end)
  transcript_by_agent["agent-1"] = "# Project one transcript, again\n"
  local picks = 0
  local refreshed
  with_config({
    select = function(items, _, done)
      picks = picks + 1
      done(items[1])
    end,
  }, function()
    require("twt").agents.refresh(function(err, result)
      assert(err == nil, err)
      assert(result.agent.id == "agent-1")
      refreshed = result.path
    end)
  end)
  assert(picks == 0, "refresh must not open the picker")
  assert(refreshed == agent_snapshot_path("project-1", "agent-1"))
  assert(table.concat(vim.fn.readfile(refreshed), "\n") == "# Project one transcript, again")
  assert(vim.bo.autoread == true)
end)

test("reuses the visible window for the same snapshot instead of splitting again", function()
  with_config({
    snapshot_split = "split",
    directory = fixed_directory("/work/app"),
  }, function()
    require("twt").agents.pick(function(err) assert(err == nil, err) end)
    local windows_after_first = #vim.api.nvim_tabpage_list_wins(0)
    require("twt").agents.refresh(function(err) assert(err == nil, err) end)
    local windows_after_second = #vim.api.nvim_tabpage_list_wins(0)
    assert(
      windows_after_second == windows_after_first,
      "refresh must reuse the existing window instead of opening another split"
    )
  end)
  vim.cmd("only")
end)

test("emits TwtRefresh after a pick, a send, and a refresh", function()
  require("twt.config").get().directory = function() return "/work/app" end
  local fired = 0
  local autocmd = vim.api.nvim_create_autocmd("User", {
    pattern = "TwtRefresh",
    callback = function() fired = fired + 1 end,
  })
  require("twt").agents.pick(function(err) assert(err == nil, err) end)
  assert(fired == 1)
  require("twt").agents.send("more feedback", function(err) assert(err == nil, err) end)
  assert(fired == 2)
  require("twt").agents.refresh(function(err) assert(err == nil, err) end)
  assert(fired == 3)
  vim.api.nvim_del_autocmd(autocmd)
end)

test("offers to resume a stopped Agent Session before a send", function()
  require("twt.config").get().directory = function() return "/work/app" end
  require("twt").agents.pick(function(err) assert(err == nil, err) end)
  local agent = agents_response.agents[1]
  local live = false
  local questions = {}
  local answer = false
  with_config({
    confirm = function(question, done)
      questions[#questions + 1] = question
      done(answer)
    end,
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents resume ", 1, true) then
        live = true
        done({ code = 0, stdout = vim.json.encode({ schemaVersion = 1, agentId = agent.id, status = "live" }), stderr = "" })
        return
      end
      agent.status = live and "live" or "stopped"
      agent.capabilities = { canResume = true, canSend = live, canFocus = live, canReadTranscript = true }
      runner(argv, opts, done)
    end,
  }, function()
    local refused
    require("twt").agents.send("feedback", function(err) refused = err end)
    assert(refused and refused:find("cannot receive feedback", 1, true))
    assert(#questions == 1 and questions[1]:find("Resume and send?", 1, true))

    answer = true
    local sent
    require("twt").agents.send("feedback after resume", function(err)
      assert(err == nil, err)
      sent = calls[#calls]
    end)
    assert(sent and sent.stdin == "feedback after resume")
    assert(table.concat(sent.argv, " "):find("agents send agent%-1"))
    assert(#questions == 2)
  end)
  agent.status = "live"
  agent.capabilities = { canResume = true, canSend = true, canFocus = true, canReadTranscript = true }
end)

test("reports the selected Agent Session for a statusline", function()
  require("twt.config").get().directory = function() return "/work/app" end
  require("twt").agents.pick(function(err) assert(err == nil, err) end)
  local status = require("twt").agents.status()
  assert(status and status.label == "review", vim.inspect(status))
  assert(status.live == true)
end)

if #failures > 0 then
  error(table.concat(failures, "\n"))
end
print("twt.nvim tests: ok")
