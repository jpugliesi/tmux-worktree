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
  schemaVersion = 2,
  workspace = { id = "workspace-1", name = "change-one" },
  repositoryName = "app",
}
local other_context = {
  schemaVersion = 2,
  workspace = { id = "workspace-2", name = "change-two" },
  repositoryName = "app",
}
local agents_response = {
  schemaVersion = 2,
  workspaceId = "workspace-1",
  agents = {
    {
      id = "agent-1",
      workspaceId = "workspace-1",
      provider = "codex",
      label = "review",
      status = "live",
      capabilities = { canResume = true, canSend = true, canFocus = true, canReadTranscript = true },
    },
  },
}
local other_agents_response = {
  schemaVersion = 2,
  workspaceId = "workspace-2",
  agents = {
    {
      id = "agent-2",
      workspaceId = "workspace-2",
      provider = "codex",
      label = "other-review",
      status = "live",
      capabilities = { canResume = true, canSend = true, canFocus = true, canReadTranscript = true },
    },
  },
}
local third_agent = {
  id = "agent-3",
  workspaceId = "workspace-2",
  provider = "codex",
  label = "other-plan",
  status = "live",
  capabilities = { canResume = true, canSend = true, canFocus = true, canReadTranscript = true },
}

local before_finish
local transcript_by_agent = {
  ["agent-1"] = "# Workspace one transcript\n",
  ["agent-2"] = "# Workspace two transcript\n",
}

-- Mirrors twt's real layout: $STATE/snapshots/projects/<workspaceID>/agents/<agentID>.md
local function agent_snapshot_path(workspace_id, agent_id)
  return vim.env.TWT_STATE_DIR .. "/snapshots/projects/" .. workspace_id .. "/agents/" .. agent_id .. ".md"
end

local function save_snapshot(workspace_id, agent_id, markdown)
  local path = agent_snapshot_path(workspace_id, agent_id)
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
  local preview_id = joined:match("agents open %-%-preview (%S+)")
  local value = joined:find(" context ", 1, true) and (opts.cwd == "/work/other" and other_context or context)
    or joined:find(" agents list ", 1, true) and (opts.cwd == "/work/other" and other_agents_response or agents_response)
    or joined:find(" agents transcript snapshot ", 1, true) and {
      schemaVersion = 2,
      workspaceId = opts.cwd == "/work/other" and "workspace-2" or "workspace-1",
      agentId = snapshot_agent,
      provider = "codex",
      repositoryName = "app",
      updatedAt = "2026-08-20T00:00:00Z",
      status = "applied",
    }
    or preview_id and {
      schemaVersion = 2,
      workspaceId = opts.cwd == "/work/other" and "workspace-2" or "workspace-1",
      agentId = preview_id,
      untrusted = true,
      markdown = "_Live pane preview. This is not the full Agent Transcript._\n\n# Agent Preview for " .. preview_id .. "\n",
    }
    or { schemaVersion = 2, status = "sent", agentId = "agent-1" }
  if joined:find(" agents transcript snapshot ", 1, true) then
    value.path = save_snapshot(value.workspaceId, snapshot_agent, transcript_by_agent[snapshot_agent])
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

test("lists and selects Agents through exact Workspace context", function()
  local selected
  require("twt").agents.pick(function(err, result)
    assert(err == nil, err)
    selected = result.agent
  end)
  assert(selected.id == "agent-1")
  assert(table.concat(calls[1].argv, " ") == "/test/twt context --directory /work/app --output json")
  assert(table.concat(calls[2].argv, " ") == "/test/twt agents list --workspace workspace-1 --limit 40 --output json")
end)

test("selects and sends to a discovered live Cursor Agent without a transcript snapshot", function()
  local saved = agents_response.agents
  local candidate = {
    id = "pane-v1-cursor-candidate",
    workspaceId = "workspace-1",
    provider = "cursor",
    label = "cursor",
    status = "discovered",
    registration = "discovered",
    runtime = "live",
    capabilities = {
      canResume = true,
      canSend = true,
      canFocus = true,
      canPreview = true,
      canReadTranscript = false,
      canSnapshotTranscript = false,
    },
  }
  local registered = vim.deepcopy(candidate)
  registered.id = "cursor-agent-session"
  registered.status = "live"
  registered.registration = "registered"
  agents_response.agents = { candidate }
  local selected
  local start = #calls
  local adopt_calls = 0
  with_config({
    runner = function(argv, opts, done)
      local joined = table.concat(argv, " ")
		if joined:find(" agents adopt ", 1, true) then
        adopt_calls = adopt_calls + 1
        agents_response.agents = { registered }
        done({
          code = 0,
          stdout = vim.json.encode({ schemaVersion = 2, agent = registered, liveness = {} }),
          stderr = "",
        })
        return
      end
      runner(argv, opts, done)
    end,
  }, function()
    require("twt").agents.pick(function(err, result)
      assert(err == nil, err)
      selected = result
    end)
    assert(selected.agent.id == registered.id, vim.inspect(selected))
    assert(selected.path == nil, vim.inspect(selected))
    assert(selected.message:find("selected Agent Session", 1, true), selected.message)
    local text = table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), "\n")
    assert(text:find("Agent Preview for cursor-agent-session", 1, true), text)
    assert(vim.bo.filetype == "markdown", vim.bo.filetype)
    assert(vim.bo.buftype == "nofile", vim.bo.buftype)
    require("twt").agents.send("Cursor review", function(err) assert(err == nil, err) end)
  end)
  local commands = {}
  for index = start + 1, #calls do commands[#commands + 1] = table.concat(calls[index].argv, " ") end
  local joined = table.concat(commands, "\n")
  agents_response.agents = saved
  assert(adopt_calls == 1, tostring(adopt_calls))
  assert(joined:find("agents send cursor%-agent%-session"), joined)
  assert(not joined:find("agents transcript snapshot", 1, true), joined)
end)

test("gives duplicate Agent labels stable unique row identities and a visible Snacks preview", function()
  local saved = agents_response.agents
  agents_response.agents = {
    {
      id = "aaaaaaaa-1111-2222-3333-444444444444",
      workspaceId = "workspace-1",
      provider = "Codex",
      label = "codex",
      status = "discovered",
      lastActivity = "2026-08-24T18:07:05Z",
      capabilities = { canResume = true, canSend = false, canFocus = false, canReadTranscript = true },
    },
    {
      id = "aaaaaaaa-2222-3333-4444-555555555555",
      workspaceId = "workspace-1",
      provider = "codex",
      label = "codex",
      status = "stopped",
      updatedAt = "2026-08-24T17:07:05Z",
      capabilities = { canResume = true, canSend = false, canFocus = false, canReadTranscript = true },
    },
  }
  local items, opts
  local call_count = #calls
  with_config({
    select = function(pick_items, pick_opts, done)
      items, opts = pick_items, pick_opts
      done(nil)
    end,
  }, function()
    require("twt").agents.pick(function(err, result)
      assert(err == nil, err)
      assert(result == nil)
    end)
  end)
  agents_response.agents = saved

  assert(#calls == call_count + 2, "opening the picker must not load a transcript")
  assert(opts and opts.kind == "twt_agent_session", vim.inspect(opts))
  assert(opts.snacks and opts.snacks.preview, "the Agent picker must configure a Snacks preview")
  assert(opts.snacks.layout and opts.snacks.layout.preset == "default", vim.inspect(opts.snacks))
  local first = opts.format_item(items[1])
  local second = opts.format_item(items[2])
  local _, codex_count = first:lower():gsub("codex", "")
  assert(codex_count == 1, first)
  assert(first:find("aaaaaaaa%-1") and second:find("aaaaaaaa%-2"), first .. " / " .. second)
  assert(first:find("discovered", 1, true) and first:find("2026-08-24 18:07 UTC", 1, true), first)
end)

test("rejects duplicate Agent Session IDs before opening the picker", function()
  local saved = agents_response.agents
  local duplicate = vim.deepcopy(saved[1])
  agents_response.agents = { saved[1], duplicate }
  local selected = false
  local received
  with_config({
    select = function() selected = true end,
  }, function()
    require("twt").agents.pick(function(err) received = err end)
  end)
  agents_response.agents = saved
  assert(received == "twt returned duplicate Agent Session IDs", tostring(received))
  assert(not selected, "invalid Agent Sessions reached the picker")
end)

test("reports incomplete Agent Session discovery even when entries exist", function()
  local old_complete, old_diagnostics = agents_response.complete, agents_response.diagnostics
  agents_response.complete = false
  agents_response.diagnostics = { "injected process scan failure" }
  local selected = false
  local received
  with_config({ select = function() selected = true end }, function()
    require("twt").agents.pick(function(err) received = err end)
  end)
  agents_response.complete, agents_response.diagnostics = old_complete, old_diagnostics
  assert(received and received:find("injected process scan failure", 1, true), tostring(received))
  assert(not selected, "an incomplete Agent Session list reached the picker")
end)

test("uses snapshot error codes for the live Agent fallback", function()
  local function pick_with_error(code)
    local received, result
    with_config({
      runner = function(argv, opts, done)
        if table.concat(argv, " "):find(" agents transcript snapshot ", 1, true) then
          done({
            code = 1,
            stdout = "",
            stderr = vim.json.encode({ schemaVersion = 2, error = { code = code, message = "injected snapshot failure" } }),
          })
          return
        end
        runner(argv, opts, done)
      end,
    }, function()
      require("twt").agents.pick(function(err, value)
        received, result = err, value
      end)
    end)
    return received, result
  end

  local internal_error, internal_result = pick_with_error("internal")
  assert(internal_error == "injected snapshot failure", tostring(internal_error))
  assert(internal_result == nil, vim.inspect(internal_result))

  local missing_error, missing_result = pick_with_error("not_found")
  assert(missing_error == nil, tostring(missing_error))
  assert(missing_result and missing_result.agent.id == "agent-1" and missing_result.path == nil, vim.inspect(missing_result))
end)

test("loads only the latest Agent Transcript preview and caches successful results", function()
  local first = {
    id = "preview-agent-a",
    workspaceId = "workspace-1",
    provider = "codex",
    label = "first",
    status = "discovered",
    capabilities = { canResume = true, canSend = false, canFocus = false, canReadTranscript = true },
  }
  local second = vim.deepcopy(first)
  second.id, second.label = "preview-agent-b", "second"
  local missing = vim.deepcopy(first)
  missing.id, missing.label = "preview-agent-missing", "missing"
  missing.capabilities.canReadTranscript = false
  local retry = vim.deepcopy(first)
  retry.id, retry.label = "preview-agent-retry", "retry"
  local saved = agents_response.agents
  agents_response.agents = { first, second, missing, retry }
  local items, opts
  local choose
  local pending = {}
  local ok, test_err = pcall(function() with_config({
    select = function(pick_items, pick_opts, done)
      items, opts = pick_items, pick_opts
      choose = done
    end,
    runner = function(argv, runner_opts, done)
      if table.concat(argv, " "):find(" agents open %-%-preview ") then
        pending[#pending + 1] = { argv = vim.deepcopy(argv), opts = runner_opts, done = done }
      else
        runner(argv, runner_opts, done)
      end
    end,
  }, function()
    require("twt").agents.pick(function(err) assert(err == nil, err) end)

    local rendered = {}
    local title
    local highlights = 0
    local picker = { closed = false }
    local preview = {
      reset = function() rendered = {} end,
      set_title = function(_, value) title = value end,
      set_lines = function(_, lines) rendered = vim.deepcopy(lines) end,
      highlight = function(_, value)
        assert(value.ft == "markdown")
        highlights = highlights + 1
      end,
    }
    local function context(agent)
      return { item = { item = agent }, picker = picker, preview = preview }
    end
    local function text()
      return table.concat(rendered, "\n")
    end
    local function finish(request, agent, markdown)
      request.done({
        code = 0,
        stdout = vim.json.encode({
          schemaVersion = 2,
          workspaceId = "workspace-1",
          agentId = agent.id,
          provider = agent.provider,
          repositoryName = "app",
          updatedAt = "2026-08-24T18:07:05Z",
          untrusted = true,
          markdown = markdown,
        }),
        stderr = "",
      })
    end

    opts.snacks.preview(context(items[1]))
    assert(text():find("Loading Agent Preview", 1, true), text())
    assert(#pending == 0, "the first preview must start asynchronously")
    assert(vim.wait(1000, function() return #pending == 1 end), "the first preview did not start")
    assert(table.concat(pending[1].argv, " ") == "/test/twt agents open --preview preview-agent-a --workspace workspace-1 --output json")
    assert(pending[1].opts.cwd == "/work/app")

    opts.snacks.preview(context(items[2]))
    assert(text():find("Loading Agent Preview", 1, true), text())
    vim.wait(20)
    assert(#pending == 1, "only one transcript process can run")
    finish(pending[1], first, "# First transcript\n\27[31mnot terminal code")
    assert(not text():find("First transcript", 1, true), "a stale transcript replaced the current preview")
    assert(vim.wait(1000, function() return #pending == 2 end), "the latest preview did not start")
    finish(pending[2], second, "# Second transcript\nCurrent work")
    assert(text():find("Second transcript", 1, true), text())
    assert(title and title:find("preview%-agent%-b"), tostring(title))
    assert(highlights > 0)

    local process_count = #pending
    opts.snacks.preview(context(items[1]))
    assert(text():find("First transcript", 1, true), text())
    vim.wait(20)
    assert(#pending == process_count, "a cached transcript started another process")

    opts.snacks.preview(context(items[3]))
    assert(text():find("has no available preview", 1, true), text())
    vim.wait(20)
    assert(#pending == process_count, "a missing transcript started a process")

    opts.snacks.preview(context(items[4]))
    assert(vim.wait(1000, function() return #pending == process_count + 1 end), "the failed preview did not start")
    pending[#pending].done({
      code = 3,
      stdout = "",
      stderr = vim.json.encode({ schemaVersion = 2, error = { message = "injected preview failure" } }),
    })
    assert(text():find("injected preview failure", 1, true), text())
    opts.snacks.preview(context(items[4]))
    assert(vim.wait(1000, function() return #pending == process_count + 2 end), "a failed preview could not retry")
    picker.closed = true
    finish(pending[#pending], retry, "# Late transcript")
    assert(not text():find("Late transcript", 1, true), "a closed picker accepted a late transcript")
    choose(nil)
  end) end)
  agents_response.agents = saved
  if not ok then error(test_err, 0) end
end)

test("keeps the registered ID after a discovered transcript snapshot adopts the session", function()
  local discovered = {
    id = "codex-session-1",
    workspaceId = "workspace-1",
    provider = "codex",
    providerSessionId = "codex-session-1",
    label = "codex",
    status = "discovered",
    capabilities = { canResume = true, canSend = false, canFocus = false, canReadTranscript = true },
  }
  local registered_id = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
  local saved_agents = agents_response.agents
  agents_response.agents = { discovered }
  with_config({
    directory = fixed_directory("/work/app"),
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents transcript snapshot ", 1, true) then
        local path = save_snapshot("workspace-1", registered_id, "# Discovered transcript\n")
        done({
          code = 0,
          stdout = vim.json.encode({
            schemaVersion = 2,
            workspaceId = "workspace-1",
            agentId = registered_id,
            provider = "codex",
            repositoryName = "app",
            updatedAt = "2026-08-20T00:00:00Z",
            status = "applied",
            path = path,
          }),
          stderr = "",
        })
        return
      end
      runner(argv, opts, done)
    end,
  }, function()
    local result
    require("twt").agents.pick(function(err, value)
      assert(err == nil, err)
      result = value
    end)
    assert(result.agent.id == registered_id, result.agent.id)
  end)
  agents_response.agents = saved_agents
  require("twt").agents.pick(function(err) assert(err == nil, err) end)
end)

test("revalidates the selected Agent and sends feedback on standard input", function()
  local ok
  require("twt").agents.send("review text", function(err)
    assert(err == nil)
    ok = true
  end)
  assert(ok)
  local sent = calls[#calls]
  assert(table.concat(sent.argv, " ") == "/test/twt agents send agent-1 --workspace workspace-1 --stdin --output json")
  assert(sent.stdin == "review text")
end)

test("adds and formats one session batch without Git or twt", function()
  local review = require("twt").review
  review.clear()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/one", "p")
  vim.fn.mkdir(root .. "/two", "p")
  local first_path = root .. "/one/transcript.md"
  local first = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(first, first_path)
  vim.api.nvim_buf_set_lines(first, 0, -1, false, { "one", "```", "three" })
  vim.api.nvim_set_current_buf(first)

  local call_count = #calls
  local added
  review.add("change this", 2, 2, function(err, note)
    assert(err == nil, err)
    added = note
  end)
  assert(added and added.workspace_id == nil, vim.inspect(added))
  assert(#calls == call_count, "adding a note must not call twt")
  vim.api.nvim_buf_set_lines(first, 0, 0, false, { "inserted" })
  local renamed_path = root .. "/one/renamed transcript.md"
  vim.api.nvim_buf_set_name(first, renamed_path)
  renamed_path = vim.fs.normalize(vim.api.nvim_buf_get_name(first))

  local second_path = root .. "/two/file.go"
  local second = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(second, second_path)
  vim.api.nvim_buf_set_lines(second, 0, -1, false, { "alpha", "beta" })
  vim.api.nvim_set_current_buf(second)
  review.add("keep this", 1, 1, function(err) assert(err == nil, err) end)
  second_path = vim.fs.normalize(vim.api.nvim_buf_get_name(second))

  local format_err, batch = review.format()
  assert(format_err == nil, format_err)
  assert(#batch.notes == 2, vim.inspect(batch))
  assert(batch.text:find("@" .. renamed_path .. "#L3\n", 1, true), batch.text)
  assert(batch.text:find("@" .. second_path .. "#L1\n", 1, true), batch.text)
  assert(batch.text:find("````\n```\n````", 1, true), batch.text)
  assert(batch.text:find("\ntwo\n", 1, true) == nil, batch.text)
  assert(#calls == call_count, "formatting notes must not call twt")

  review.clear()
  vim.api.nvim_buf_delete(first, { force = true })
  vim.api.nvim_buf_delete(second, { force = true })
end)

test("rejects unnamed and non-file review buffers without calling twt", function()
  local review = require("twt").review
  review.clear()
  local call_count = #calls
  local unnamed = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_set_current_buf(unnamed)
  local unnamed_error
  review.add("note", 1, 1, function(err) unnamed_error = err end)
  assert(unnamed_error and unnamed_error:find("save the file", 1, true), unnamed_error)

  local scratch = vim.api.nvim_create_buf(false, true)
  vim.bo[scratch].buftype = "nofile"
  vim.api.nvim_buf_set_name(scratch, "review-scratch")
  vim.api.nvim_set_current_buf(scratch)
  local scratch_error
  review.add("note", 1, 1, function(err) scratch_error = err end)
  assert(scratch_error and scratch_error:find("regular file", 1, true), scratch_error)
  assert(#calls == call_count, "invalid review buffers must not call twt")
  vim.api.nvim_buf_delete(unnamed, { force = true })
  vim.api.nvim_buf_delete(scratch, { force = true })
end)

test("lists safe tmux pane targets and sends with a private bracketed buffer", function()
  local old_tmux, old_pane = vim.env.TMUX, vim.env.TMUX_PANE
  vim.env.TMUX, vim.env.TMUX_PANE = "/tmp/tmux/default,1,0", "%1"
  local tmux_calls = {}
  with_config({
    tmux_runner = function(argv, opts, done)
      tmux_calls[#tmux_calls + 1] = { argv = vim.deepcopy(argv), stdin = opts.stdin }
      if argv[2] == "list-panes" then
        assert(vim.deep_equal(vim.list_slice(argv, 2, 5), { "list-panes", "-s", "-t", "%1" }), vim.inspect(argv))
        local separator = string.char(31)
        done({ code = 0, stdout = table.concat({
          table.concat({ "%1", "dev:0.0", "zsh", "/work/current", "0" }, separator),
          table.concat({ "%2", "dev:1.0", "codex", "/work/with spaces", "0" }, separator),
          table.concat({ "%3", "other:0.1", "bash", "/work/dead", "1" }, separator),
        }, "\n"), stderr = "" })
      else
        done({ code = 0, stdout = "", stderr = "" })
      end
    end,
  }, function()
    local panes
    require("twt.tmux").list(function(err, value)
      assert(err == nil, err)
      panes = value
    end)
    assert(#panes == 1, vim.inspect(panes))
    assert(panes[1].id == "%2", vim.inspect(panes[1]))
    assert(panes[1].label == "dev:1.0 · %2 · codex · /work/with spaces", panes[1].label)

    local call_count = #tmux_calls
    local current_error
    require("twt.tmux").send("%1", "do not send", function(err) current_error = err end)
    assert(current_error and current_error:find("current Neovim pane", 1, true), current_error)
    assert(#tmux_calls == call_count, "the current pane must not receive tmux commands")

    local first_buffer, second_buffer
    for index = 1, 2 do
      require("twt.tmux").send("%2", "line one\nline two", function(err)
        assert(err == nil, err)
      end)
      local load = tmux_calls[#tmux_calls - 2]
      local paste = tmux_calls[#tmux_calls - 1]
      local enter = tmux_calls[#tmux_calls]
      assert(load.argv[2] == "load-buffer" and load.stdin == "line one\nline two", vim.inspect(load))
      assert(paste.argv[2] == "paste-buffer", vim.inspect(paste.argv))
      assert(vim.tbl_contains(paste.argv, "-d") and vim.tbl_contains(paste.argv, "-p"), vim.inspect(paste.argv))
      assert(enter.argv[2] == "send-keys" and enter.argv[#enter.argv] == "Enter", vim.inspect(enter.argv))
      local name
      for i, value in ipairs(load.argv) do if value == "-b" then name = load.argv[i + 1] end end
      if index == 1 then first_buffer = name else second_buffer = name end
    end
    assert(first_buffer and second_buffer and first_buffer ~= second_buffer, vim.inspect({ first_buffer, second_buffer }))
  end)
  vim.env.TMUX, vim.env.TMUX_PANE = old_tmux, old_pane
end)

test("cleans a tmux buffer when a pane paste fails", function()
  local tmux_calls = {}
  with_config({
    tmux_runner = function(argv, opts, done)
      tmux_calls[#tmux_calls + 1] = { argv = vim.deepcopy(argv), stdin = opts.stdin }
      if argv[2] == "paste-buffer" then
        done({ code = 1, stdout = "", stderr = "pane disappeared" })
      else
        done({ code = 0, stdout = "", stderr = "" })
      end
    end,
  }, function()
    local send_error
    require("twt.tmux").send("%8", "review", function(err) send_error = err end)
    assert(send_error and send_error:find("pane disappeared", 1, true), send_error)
    assert(tmux_calls[#tmux_calls].argv[2] == "delete-buffer", vim.inspect(tmux_calls))
  end)
end)

test("reports a tmux buffer cleanup failure", function()
  with_config({
    tmux_runner = function(argv, _, done)
      if argv[2] == "paste-buffer" then
        done({ code = 1, stdout = "", stderr = "paste failed" })
      elseif argv[2] == "delete-buffer" then
        done({ code = 1, stdout = "", stderr = "cleanup failed" })
      else
        done({ code = 0, stdout = "", stderr = "" })
      end
    end,
  }, function()
    local send_error
    require("twt.tmux").send("%8", "review", function(err) send_error = err end)
    assert(send_error and send_error:find("paste failed", 1, true), send_error)
    assert(send_error:find("cleanup failed", 1, true), send_error)
  end)
end)

test("copies a Review Batch without clearing its notes", function()
  local review = require("twt").review
  review.clear()
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, vim.fn.tempname() .. ".md")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "copy this" })
  vim.api.nvim_set_current_buf(buffer)
  review.add("clipboard note", 1, 1, function(err) assert(err == nil, err) end)
  local copied
  with_config({ clipboard = function(text) copied = text end }, function()
    local result
    review.copy(function(err, value)
      assert(err == nil, err)
      result = value
    end)
    assert(result == "review notes copied to the clipboard", vim.inspect(result))
  end)
  assert(copied and copied:find("clipboard note", 1, true), copied)
  assert(#review.list() == 1, "a clipboard copy must keep the Review Batch")
  review.clear()
  vim.api.nvim_buf_delete(buffer, { force = true })
end)

test("keeps new and revised notes when an older Agent delivery completes", function()
  local review = require("twt").review
  review.clear()
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, vim.fn.tempname() .. ".md")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "first", "second" })
  vim.api.nvim_set_current_buf(buffer)
  local first
  review.add("old comment", 1, 1, function(err, note)
    assert(err == nil, err)
    first = note
  end)

  local pending
  with_config({
    directory = fixed_directory("/work/app"),
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents send ", 1, true) then
        pending = { opts = opts, done = done }
      else
        runner(argv, opts, done)
      end
    end,
  }, function()
    local delivered = false
    review.send(function(err)
      assert(err == nil, err)
      delivered = true
    end)
    assert(pending and pending.opts.stdin:find("old comment", 1, true), vim.inspect(pending))

    assert(review.update(first.id, "new comment") == nil)
    review.add("new note", 2, 2, function(err) assert(err == nil, err) end)
    local duplicate_error
    review.send(function(err) duplicate_error = err end)
    assert(duplicate_error == "a review delivery is already in progress", duplicate_error)

    pending.done({
      code = 0,
      stdout = vim.json.encode({ schemaVersion = 2, status = "sent", agentId = "agent-1" }),
      stderr = "",
    })
    assert(delivered)
  end)
  local left = vim.tbl_map(function(note) return note.comment end, review.list())
  assert(vim.deep_equal(left, { "new comment", "new note" }), vim.inspect(left))
  review.clear()
  vim.api.nvim_buf_delete(buffer, { force = true })
end)

test("routes Review Batch delivery without requiring twt", function()
  local review = require("twt").review
  review.clear()
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, vim.fn.tempname() .. ".md")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "route this" })
  vim.api.nvim_set_current_buf(buffer)
  review.add("route note", 1, 1, function(err) assert(err == nil, err) end)

  local old_tmux, old_pane = vim.env.TMUX, vim.env.TMUX_PANE
  vim.env.TMUX, vim.env.TMUX_PANE = nil, nil
  local copied
  with_config({ clipboard = function(text) copied = text end }, function()
    local result
    review.deliver(function(err, value)
      assert(err == nil, err)
      result = value
    end)
    assert(result == "review notes copied to the clipboard", vim.inspect(result))
  end)
  assert(copied and copied:find("route note", 1, true), copied)
  assert(#review.list() == 1, "clipboard fallback must keep notes")

  vim.env.TMUX, vim.env.TMUX_PANE = "/tmp/tmux/default,1,0", "%1"
  local tmux_calls = {}
  local selected_items
  with_config({
    select = function(items, _, done)
      selected_items = items
      done(items[1])
    end,
    tmux_runner = function(argv, opts, done)
      tmux_calls[#tmux_calls + 1] = { argv = vim.deepcopy(argv), stdin = opts.stdin }
      if argv[2] == "list-panes" then
        local separator = string.char(31)
        done({
          code = 0,
          stdout = table.concat({ "%2", "dev:1.0", "codex", "/work/app", "0" }, separator),
          stderr = "",
        })
      else
        done({ code = 0, stdout = "", stderr = "" })
      end
    end,
  }, function()
    local result
    review.deliver(function(err, value)
      assert(err == nil, err)
      result = value
    end)
    assert(result and result:find("review notes sent to dev:1.0", 1, true), result)
  end)
  assert(#selected_items == 2 and selected_items[1].kind == "pane" and selected_items[2].kind == "clipboard", vim.inspect(selected_items))
  assert(#review.list() == 0, "a confirmed pane send must clear the delivered notes")
  local load
  for _, call in ipairs(tmux_calls) do if call.argv[2] == "load-buffer" then load = call end end
  assert(load and load.stdin:find("route note", 1, true), vim.inspect(tmux_calls))

  vim.env.TMUX, vim.env.TMUX_PANE = old_tmux, old_pane
  vim.api.nvim_buf_delete(buffer, { force = true })
end)

test("cancels the Review Batch destination picker without a success result", function()
  local review = require("twt").review
  review.clear()
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, vim.fn.tempname() .. ".md")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "cancel this" })
  vim.api.nvim_set_current_buf(buffer)
  review.add("keep note", 1, 1, function(err) assert(err == nil, err) end)
  local old_tmux, old_pane = vim.env.TMUX, vim.env.TMUX_PANE
  vim.env.TMUX, vim.env.TMUX_PANE = "/tmp/tmux/default,1,0", "%1"
  with_config({
    select = function(_, _, done) done(nil) end,
    tmux_runner = function(argv, _, done)
      if argv[2] == "list-panes" then
        local separator = string.char(31)
        done({ code = 0, stdout = table.concat({ "%2", "dev:1.0", "codex", "/work/app", "0" }, separator), stderr = "" })
      else
        done({ code = 0, stdout = "", stderr = "" })
      end
    end,
  }, function()
    local result = "not called"
    review.deliver(function(err, value)
      assert(err == nil, err)
      result = value
    end)
    assert(result == nil, vim.inspect(result))
  end)
  assert(#review.list() == 1)
  review.clear()
  vim.env.TMUX, vim.env.TMUX_PANE = old_tmux, old_pane
  vim.api.nvim_buf_delete(buffer, { force = true })
end)

test("allows only one Review Batch route at a time", function()
  local review = require("twt").review
  review.clear()
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, vim.fn.tempname() .. ".md")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one route" })
  vim.api.nvim_set_current_buf(buffer)
  review.add("one delivery", 1, 1, function(err) assert(err == nil, err) end)
  local old_tmux, old_pane = vim.env.TMUX, vim.env.TMUX_PANE
  vim.env.TMUX, vim.env.TMUX_PANE = "/tmp/tmux/default,1,0", "%1"
  local pending_list
  with_config({
    tmux_runner = function(argv, _, done)
      if argv[2] == "list-panes" then pending_list = done end
    end,
  }, function()
    review.deliver(function() end)
    assert(pending_list, "the first route must wait for the pane list")
    local duplicate_error
    review.deliver(function(err) duplicate_error = err end)
    assert(duplicate_error == "a review delivery is already in progress", duplicate_error)
    pending_list({ code = 1, stdout = "", stderr = "injected list failure" })
  end)
  review.clear()
  vim.env.TMUX, vim.env.TMUX_PANE = old_tmux, old_pane
  vim.api.nvim_buf_delete(buffer, { force = true })
end)

test("keeps a review send in its captured Workspace when the current buffer changes", function()
  local review = require("twt").review
  review.clear()
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, vim.fn.tempname() .. ".md")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "review this" })
  vim.api.nvim_set_current_buf(buffer)
  review.add("fix it", 1, 1, function(err) assert(err == nil, err) end)
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
  vim.api.nvim_buf_delete(buffer, { force = true })
end)

test("writes and reopens a private latest transcript for each Workspace", function()
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

  assert(first_path == snapshot_root .. "/workspace-1/agents/agent-1.md")
  assert(second_path == snapshot_root .. "/workspace-2/agents/agent-2.md")
  assert(first_path ~= second_path)
  assert(table.concat(vim.fn.readfile(first_path), "\n") == "# Workspace one transcript")
  assert(table.concat(vim.fn.readfile(second_path), "\n") == "# Workspace two transcript")
  assert(vim.uv.fs_stat(first_path).mode % 512 == 384)
  assert(vim.uv.fs_stat(vim.fs.dirname(first_path)).mode % 512 == 448)

  transcript_by_agent["agent-1"] = "# Workspace one transcript, refreshed\n"
  directory = "/work/app"
  require("twt").agents.pick(function(err) assert(err == nil, err) end)
  assert(table.concat(vim.fn.readfile(first_path), "\n") == "# Workspace one transcript, refreshed")
  assert(table.concat(vim.api.nvim_buf_get_lines(0, 0, -1, false), "\n") == "# Workspace one transcript, refreshed")
  assert(vim.bo.modifiable == false)
  assert(vim.bo.readonly == true)

  require("twt.snapshot").open(first_path, "workspace-1")
  assert(vim.fn.resolve(vim.api.nvim_buf_get_name(0)) == vim.fn.resolve(first_path))
end)

test("serializes transcript snapshots for one Workspace", function()
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
    local agent2_path = save_snapshot("workspace-2", "agent-2", "# First selection\n")
    pending[1].done({
      code = 0,
      stdout = vim.json.encode({
        schemaVersion = 2, workspaceId = "workspace-2", agentId = "agent-2",
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
    local agent3_path = save_snapshot("workspace-2", "agent-3", "# Second selection\n")
    pending[2].done({
      code = 0,
      stdout = vim.json.encode({
        schemaVersion = 2, workspaceId = "workspace-2", agentId = "agent-3",
        provider = "codex", repositoryName = "app", updatedAt = "2026-08-20T00:00:00Z",
        status = "applied", path = agent3_path,
      }),
      stderr = "",
    })
    assert(second_done)
    assert(agent3_path == agent_snapshot_path("workspace-2", "agent-3"))
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
        local open_fails_path = save_snapshot("workspace-1", "agent-open-fails", "# New file\n")
        done({ code = 0, stdout = vim.json.encode({
          schemaVersion = 2,
          workspaceId = "workspace-1",
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

  -- The Workspace still sends to the Agent Session of the last good snapshot.
  local send_argv
  with_config({
    directory = fixed_directory("/work/app"),
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents send ", 1, true) then
        send_argv = table.concat(argv, " ")
        done({ code = 0, stdout = vim.json.encode({ schemaVersion = 2, status = "sent", agentId = "agent-1" }), stderr = "" })
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

test("allows one feedback send per Workspace at the same time", function()
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
    pending[1].done({ code = 0, stdout = vim.json.encode({ schemaVersion = 2, status = "sent", agentId = "agent-1" }), stderr = "" })
    pending[2].done({ code = 0, stdout = vim.json.encode({ schemaVersion = 2, status = "sent", agentId = "agent-3" }), stderr = "" })
    assert(first_done and second_done)
  end)
end)

test("rejects an unsupported JSON schema", function()
  local client = require("twt.client")
  local old = context.schemaVersion
  context.schemaVersion = 3
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
    return require("twt.snapshot").resolve({}, "workspace-1")
  end
  local old_state = vim.env.TWT_STATE_DIR
  local old_xdg = vim.env.XDG_STATE_HOME
  vim.env.TWT_STATE_DIR = "/explicit/state/twt"
  assert(legacy_path() == "/explicit/state/twt/snapshots/projects/workspace-1/latest.md")
  vim.env.TWT_STATE_DIR = nil
  vim.env.XDG_STATE_HOME = "/xdg/state"
  assert(legacy_path() == "/xdg/state/twt/snapshots/projects/workspace-1/latest.md")
  vim.env.TWT_STATE_DIR = old_state
  vim.env.XDG_STATE_HOME = old_xdg
  assert(require("twt.snapshot").resolve({ path = "/reported/path.md" }, "workspace-1") == "/reported/path.md")

  local fallback_opened
  with_config({
    directory = fixed_directory("/work/app"),
    runner = function(argv, opts, done)
      if table.concat(argv, " "):find(" agents transcript snapshot ", 1, true) then
        local fallback = legacy_path()
        vim.fn.mkdir(vim.fs.dirname(fallback), "p")
        assert(vim.fn.writefile({ "# Legacy transcript" }, fallback, "b") == 0)
        done({ code = 0, stdout = vim.json.encode({
          schemaVersion = 2,
          workspaceId = "workspace-1",
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
    "TwtAgents", "TwtNote", "TwtReview", "TwtReviewAgent", "TwtReviewCopy", "TwtReviewPane", "TwtSend",
    "TwtNotes", "TwtNoteDelete", "TwtResume", "TwtFocus", "TwtRefresh", "TwtClear",
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

local function delete_keymap(buffer)
  for _, map in ipairs(vim.api.nvim_buf_get_keymap(buffer, "n")) do
    if map.callback and map.lhs:lower():find("c%-d") then return map.callback end
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

local function with_source_window(line_count, cursor_line, view, body)
  local previous = vim.api.nvim_get_current_buf()
  local buffer = vim.api.nvim_create_buf(false, true)
  local lines = {}
  for index = 1, line_count do
    lines[index] = "    content " .. index
  end
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, lines)
  vim.api.nvim_set_current_buf(buffer)
  vim.api.nvim_win_set_cursor(0, { cursor_line, 0 })
  local viewinfo = vim.fn.winsaveview()
  if view == "zt" then
    viewinfo.topline = cursor_line
  elseif view == "zb" then
    viewinfo.topline = math.max(1, cursor_line - vim.api.nvim_win_get_height(0) + 1)
  end
  vim.fn.winrestview(viewinfo)
  local ok, err = pcall(body)
  vim.api.nvim_set_current_buf(previous)
  vim.api.nvim_buf_delete(buffer, { force = true })
  if not ok then error(err, 0) end
end

local function window_row(line)
  local view = vim.fn.winsaveview()
  vim.api.nvim_win_set_cursor(0, { line, 0 })
  local row = vim.fn.winline()
  vim.fn.winrestview(view)
  return row
end

test("places a review note below a high selection when the viewport has room", function()
  vim.o.lines = 40
  vim.o.columns = 80
  with_source_window(30, 3, "zt", function()
    vim.wo.number = true
    local textoff = vim.fn.getwininfo(vim.api.nvim_get_current_win())[1].textoff
    local place = require("twt.input").placement({ start_line = 3, end_line = 5, height = 5 })
    local block = window_row(5)
    assert(place.relative == "win", vim.inspect(place))
    assert(place.col == textoff + vim.fn.indent(3), vim.inspect({ place = place, textoff = textoff }))
    assert(place.col >= textoff, vim.inspect({ place = place, textoff = textoff }))
    assert(place.width < vim.api.nvim_win_get_width(0) - 4, vim.inspect(place))
    assert(place.row >= block, vim.inspect({ place = place, block = block }))
  end)
end)

test("places a review note above a low selection when the viewport has no room below", function()
  vim.o.lines = 24
  vim.o.columns = 80
  with_source_window(40, 40, "zb", function()
    local start_line = vim.fn.line("w$")
    local place = require("twt.input").placement({
      start_line = start_line, end_line = start_line, height = 5,
    })
    local block = window_row(start_line)
    assert(place.relative == "win", vim.inspect(place))
    assert(place.row + place.height + 2 <= block, vim.inspect({ place = place, block = block }))
  end)
end)

-- A side of a box border is missing when that cell is empty or a space.
local function border_side(border, index)
  if type(border) == "string" then return border end
  local cell = border[index]
  if type(cell) == "table" then cell = cell[1] end
  return cell
end

test("draws the review note as a colorscheme float box", function()
  vim.o.lines = 40
  vim.o.columns = 80
  with_source_window(30, 3, "zt", function()
    vim.wo.number = true
    local textoff = vim.fn.getwininfo(vim.api.nvim_get_current_win())[1].textoff
    local indent = vim.fn.indent(3)
    local _, window = require("twt.input").open({
      title = "Note", start_line = 3, end_line = 3,
    }, function() end)
    local config = vim.api.nvim_win_get_config(window)
    local right = border_side(config.border, 4)
    local left = border_side(config.border, 8)
    assert(right ~= " " and right ~= "", vim.inspect(config.border))
    assert(left ~= " " and left ~= "", vim.inspect(config.border))
    local highlights = vim.wo[window].winhighlight
    assert(highlights:find("TwtFloat"), highlights)
    assert(highlights:find("TwtFloatBorder"), highlights)
    assert(not highlights:find("WinSeparator"), highlights)
    assert(not highlights:find("FloatBorder:FloatBorder"), highlights)
    local fill = vim.api.nvim_get_hl(0, { name = "TwtFloat", link = true })
    local border = vim.api.nvim_get_hl(0, { name = "TwtFloatBorder", link = true })
    assert(fill.link == "Pmenu", vim.inspect(fill))
    assert(border.link == "FloatTitle", vim.inspect(border))
    local parent = config.win
    if type(parent) == "table" then parent = parent.win or parent[1] end
    assert(config.width < vim.api.nvim_win_get_width(parent) - 4, vim.inspect(config))
    assert(config.col == textoff + indent, vim.inspect({ col = config.col, textoff = textoff, indent = indent }))
    assert(config.col >= textoff, vim.inspect({ col = config.col, textoff = textoff }))
    local footer = config.footer
    if type(footer) == "table" then footer = footer[1] and (footer[1][1] or footer[1]) or vim.inspect(footer) end
    assert(tostring(footer):find("C%-s save") and tostring(footer):find("q quit"), vim.inspect(config.footer))
    assert(config.footer_pos == "right", vim.inspect(config.footer_pos))
    vim.api.nvim_win_close(window, true)
  end)
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

test("asks the notes picker for a snacks preview of the highlighted note", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/src", "p")
  vim.fn.mkdir(root .. "/.git", "p")
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, root .. "/src/other.go")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "```", "three" })
  vim.api.nvim_set_current_buf(buffer)
  require("twt.config").get().directory = function() return root .. "/src" end
  local review = require("twt").review
  review.clear()
  review.add("first note", 2, 2, function(err) assert(err == nil, err) end)
  review.add("second note", 3, 3, function(err) assert(err == nil, err) end)

  local select_opts, select_items
  with_config({
    select = function(items, opts, done)
      if opts.snacks then
        select_opts = opts
        select_items = items
      end
      done(nil)
    end,
  }, function()
    review.prompt_notes(function() end)
  end)
  assert(select_opts and select_opts.snacks, "the notes picker must pass snacks preview options")
  assert(select_opts.kind == "twt_review_note", vim.inspect(select_opts.kind))
  local lines
  select_opts.snacks.preview({
    item = { item = select_items[1] },
    preview = {
      reset = function() end,
      set_lines = function(_, value) lines = value end,
      highlight = function() end,
    },
  })
  local preview = table.concat(lines, "\n")
  assert(preview:find("src/other.go:2", 1, true), preview)
  assert(preview:find("first note", 1, true), preview)
  assert(preview:find("````\n```\n````", 1, true), preview)
  review.clear()
end)

test("opens a selected review note and deletes it with Control-D", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/src", "p")
  vim.fn.mkdir(root .. "/.git", "p")
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, root .. "/src/other.go")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "two", "three" })
  vim.api.nvim_set_current_buf(buffer)
  require("twt.config").get().directory = function() return root .. "/src" end
  local review = require("twt").review
  review.clear()
  review.add("first note", 2, 2, function(err) assert(err == nil, err) end)
  review.add("second note", 3, 3, function(err) assert(err == nil, err) end)

  local labels
  local select_count = 0
  local deleted
  with_config({
    select = function(items, opts, done)
      select_count = select_count + 1
      labels = vim.tbl_map(opts.format_item, items)
      done(items[2])
    end,
  }, function()
    vim.api.nvim_win_set_cursor(0, { 1, 0 })
    review.prompt_notes(function(err, result)
      assert(err == nil, err)
      deleted = result
    end)
    assert(select_count == 1, "the note list opened a second picker")
    assert(#labels == 2)
    assert(labels[1]:find("src/other.go:2 · first note", 1, true), labels[1])
    local float = vim.api.nvim_get_current_buf()
    local lines = vim.api.nvim_buf_get_lines(float, 0, -1, false)
    assert(table.concat(lines, "\n") == "second note", vim.inspect(lines))
    local delete = delete_keymap(float)
    assert(delete, "the note window has no delete mapping")
    delete()
  end)
  assert(deleted == "review note deleted", tostring(deleted))
  local left = vim.tbl_map(function(note) return note.comment end, review.list())
  assert(#left == 1 and left[1] == "first note", table.concat(left, ","))
  review.clear()
end)

test("opens an existing review note on the line and saves the edit", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/src", "p")
  vim.fn.mkdir(root .. "/.git", "p")
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, root .. "/src/other.go")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "two", "three" })
  vim.api.nvim_set_current_buf(buffer)
  require("twt.config").get().directory = function() return root .. "/src" end
  local review = require("twt").review
  review.clear()
  review.add("first draft", 2, 2, function(err) assert(err == nil, err) end)
  vim.api.nvim_win_set_cursor(0, { 2, 0 })

  local saved
  review.prompt_add(function(err, note)
    assert(err == nil, err)
    saved = note
  end)
  local float = vim.api.nvim_get_current_buf()
  local lines = vim.api.nvim_buf_get_lines(float, 0, -1, false)
  assert(table.concat(lines, "\n") == "first draft", vim.inspect(lines))
  vim.api.nvim_buf_set_lines(float, 0, -1, false, { "revised note" })
  local save = save_keymap(float)
  assert(save, "the note window has no save mapping")
  save()
  assert(saved and saved.comment == "revised note", vim.inspect(saved))
  local left = vim.tbl_map(function(note) return note.comment end, review.list())
  assert(#left == 1 and left[1] == "revised note", table.concat(left, ","))
  review.clear()
end)

test("deletes an existing review note from the note window", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/src", "p")
  vim.fn.mkdir(root .. "/.git", "p")
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, root .. "/src/other.go")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "two", "three" })
  vim.api.nvim_set_current_buf(buffer)
  require("twt.config").get().directory = function() return root .. "/src" end
  local review = require("twt").review
  review.clear()
  review.add("first draft", 2, 2, function(err) assert(err == nil, err) end)
  vim.api.nvim_win_set_cursor(0, { 2, 0 })

  local result
  review.prompt_add(function(err, value)
    assert(err == nil, err)
    result = value
  end)
  local float = vim.api.nvim_get_current_buf()
  local config = vim.api.nvim_win_get_config(0)
  local footer = config.footer
  if type(footer) == "table" then footer = footer[1] and (footer[1][1] or footer[1]) or vim.inspect(footer) end
  assert(tostring(footer):find("C%-d delete"), vim.inspect(config.footer))
  local delete = delete_keymap(float)
  assert(delete, "the note window has no delete mapping")
  delete()
  assert(result == "review note deleted", vim.inspect(result))
  local left = vim.tbl_map(function(note) return note.comment end, review.list())
  assert(#left == 0, table.concat(left, ","))
  review.clear()
end)

test("deletes an opened review note when the comment is cleared", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/src", "p")
  vim.fn.mkdir(root .. "/.git", "p")
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, root .. "/src/other.go")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "two", "three" })
  vim.api.nvim_set_current_buf(buffer)
  require("twt.config").get().directory = function() return root .. "/src" end
  local review = require("twt").review
  review.clear()
  review.add("first draft", 2, 2, function(err) assert(err == nil, err) end)
  vim.api.nvim_win_set_cursor(0, { 2, 0 })

  local result
  review.prompt_add(function(err, value)
    assert(err == nil, err)
    result = value
  end)
  local float = vim.api.nvim_get_current_buf()
  vim.api.nvim_buf_set_lines(float, 0, -1, false, { "" })
  local save = save_keymap(float)
  assert(save, "the note window has no save mapping")
  save()
  assert(result == "review note deleted", vim.inspect(result))
  local left = vim.tbl_map(function(note) return note.comment end, review.list())
  assert(#left == 0, table.concat(left, ","))
  review.clear()
end)

test("deletes the review note on the current line", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/src", "p")
  vim.fn.mkdir(root .. "/.git", "p")
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, root .. "/src/other.go")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "two", "three" })
  vim.api.nvim_set_current_buf(buffer)
  require("twt.config").get().directory = function() return root .. "/src" end
  local review = require("twt").review
  review.clear()
  review.add("keep me", 2, 2, function(err) assert(err == nil, err) end)
  review.add("drop me", 3, 3, function(err) assert(err == nil, err) end)
  vim.api.nvim_win_set_cursor(0, { 3, 0 })

  local result
  review.prompt_delete(function(err, value)
    assert(err == nil, err)
    result = value
  end)
  assert(result == "review note deleted", vim.inspect(result))
  local left = vim.tbl_map(function(note) return note.comment end, review.list())
  assert(#left == 1 and left[1] == "keep me", table.concat(left, ","))
  review.clear()
end)

test("asks before it clears the session review notes", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/src", "p")
  vim.fn.mkdir(root .. "/.git", "p")
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, root .. "/src/other.go")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "two", "three" })
  vim.api.nvim_set_current_buf(buffer)
  require("twt.config").get().directory = function() return root .. "/src" end
  local review = require("twt").review
  review.clear()
  review.add("keep me", 2, 2, function(err) assert(err == nil, err) end)

  local questions = {}
  local answer = false
  with_config({
    confirm = function(question, done)
      questions[#questions + 1] = question
      done(answer)
    end,
  }, function()
    review.clear_current(function(err) assert(err == nil, err) end)
    assert(#questions == 1 and questions[1]:find("Are you sure", 1, true), vim.inspect(questions))
    local left = vim.tbl_map(function(note) return note.comment end, review.list())
    assert(#left == 1 and left[1] == "keep me", table.concat(left, ","))

    answer = true
    review.clear_current(function(err) assert(err == nil, err) end)
    assert(#questions == 2)
    left = vim.tbl_map(function(note) return note.comment end, review.list())
    assert(#left == 0, table.concat(left, ","))
  end)
  review.clear()
end)

test("opens a review note from the picker and saves the edit", function()
  local root = vim.fn.tempname()
  vim.fn.mkdir(root .. "/src", "p")
  vim.fn.mkdir(root .. "/.git", "p")
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, root .. "/src/other.go")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "one", "two", "three" })
  vim.api.nvim_set_current_buf(buffer)
  require("twt.config").get().directory = function() return root .. "/src" end
  local review = require("twt").review
  review.clear()
  review.add("first note", 2, 2, function(err) assert(err == nil, err) end)
  review.add("second note", 3, 3, function(err) assert(err == nil, err) end)
  vim.api.nvim_win_set_cursor(0, { 1, 0 })

  local select_count = 0
  local saved
  with_config({
    select = function(items, _, done)
      select_count = select_count + 1
      done(items[1])
    end,
  }, function()
    review.prompt_notes(function(err, note)
      assert(err == nil, err)
      saved = note
    end)
    assert(select_count == 1, "the note list opened a second picker")
    local float_win = vim.api.nvim_get_current_win()
    local place = vim.api.nvim_win_get_config(float_win)
    local parent = place.win
    if type(parent) == "table" then parent = parent.win or parent[1] end
    assert(parent and vim.api.nvim_win_get_cursor(parent)[1] == 2, vim.inspect(place))
    local float = vim.api.nvim_get_current_buf()
    local lines = vim.api.nvim_buf_get_lines(float, 0, -1, false)
    assert(table.concat(lines, "\n") == "first note", vim.inspect(lines))
    vim.api.nvim_buf_set_lines(float, 0, -1, false, { "opened from picker" })
    local save = save_keymap(float)
    assert(save, "the note window has no save mapping")
    save()
  end)
  assert(saved and saved.comment == "opened from picker", vim.inspect(saved))
  local left = vim.tbl_map(function(note) return note.comment end, review.list())
  assert(#left == 2, table.concat(left, ","))
  assert(left[1] == "opened from picker" and left[2] == "second note", table.concat(left, ","))
  review.clear()
end)

test("writes a new snapshot without the picker", function()
  require("twt.config").get().directory = function() return "/work/app" end
  require("twt").agents.pick(function(err) assert(err == nil, err) end)
  transcript_by_agent["agent-1"] = "# Workspace one transcript, again\n"
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
  assert(refreshed == agent_snapshot_path("workspace-1", "agent-1"))
  assert(table.concat(vim.fn.readfile(refreshed), "\n") == "# Workspace one transcript, again")
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
        done({ code = 0, stdout = vim.json.encode({ schemaVersion = 2, agentId = agent.id, status = "live" }), stderr = "" })
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
