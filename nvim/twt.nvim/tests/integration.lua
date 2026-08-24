local plugin = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(plugin)

local binary = assert(vim.env.TWT_TEST_BINARY, "TWT_TEST_BINARY is required")
local root = vim.fn.tempname()
local state = root .. "/state"
local data = root .. "/data"
local config = root .. "/config"
local home = root .. "/home"
local snapshots = state .. "/snapshots/projects"

for _, directory in ipairs({ state .. "/projects", state .. "/agents", data, config, home }) do
  assert(vim.fn.mkdir(directory, "p") == 1 or vim.fn.isdirectory(directory) == 1)
end

vim.env.HOME = home
vim.env.TWT_STATE_DIR = state
vim.env.TWT_DATA_DIR = data
vim.env.TWT_CONFIG_DIR = config
local tmux_socket = "twt-nvim-test-" .. tostring(vim.fn.getpid())
vim.env.TWT_TMUX_SOCKET = tmux_socket

local function tmux(arguments)
  local command = { "tmux", "-L", tmux_socket, "-f", "/dev/null" }
  vim.list_extend(command, arguments)
  local result = vim.system(command, { text = true }):wait()
  assert(result.code == 0, result.stderr)
  return vim.trim(result.stdout)
end

local function start_agent_pane(workspace_id, session_name, agent_id)
  local pane = tmux({ "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session_name, "--", "cat" })
  tmux({ "set-option", "-t", session_name, "@twt_workspace_id", workspace_id })
  tmux({ "set-option", "-p", "-t", pane, "@twt_agent_id", agent_id })
  local process = vim.split(tmux({ "display-message", "-p", "-t", pane, "#{pane_current_command}\t#{pane_start_command}" }), "\t", { plain = true })
  assert(#process == 2)
  return pane, process[1], process[2]
end

local function write_json(path, value)
  assert(vim.fn.writefile({ vim.json.encode(value) }, path) == 0)
end

local function make_workspace(id, name, repository)
  assert(vim.fn.mkdir(repository, "p") == 1 or vim.fn.isdirectory(repository) == 1)
  write_json(state .. "/projects/" .. id .. ".json", {
    version = 1,
    id = id,
    name = name,
    templateName = "example",
    status = "active",
    root = vim.fs.dirname(repository),
    tmuxSession = name,
    repositories = { { name = "app", path = repository, windowName = "app" } },
    steps = {},
    createdAt = "2026-08-20T00:00:00Z",
    updatedAt = "2026-08-20T00:00:00Z",
  })
end

local function make_agent(id, workspace_id, session_id, pane, pane_command, pane_start)
  write_json(state .. "/agents/" .. id .. ".json", {
    version = 1,
    id = id,
    workspaceId = workspace_id,
    provider = "codex",
    label = workspace_id .. " review",
    providerSessionId = session_id,
    resumeCommand = { "codex", "resume", session_id },
    tmuxPane = pane,
    paneCommand = pane_command,
    paneStart = pane_start,
    createdAt = "2026-08-20T00:00:00Z",
    updatedAt = "2026-08-20T00:00:00Z",
  })
end

local function make_transcript(session_id, repository, text)
  local directory = home .. "/.codex/sessions/2026/08/20"
  assert(vim.fn.mkdir(directory, "p") == 1 or vim.fn.isdirectory(directory) == 1)
  local lines = {
    vim.json.encode({ type = "session_meta", payload = { id = session_id, cwd = repository } }),
    vim.json.encode({
      type = "response_item",
      payload = { role = "user", content = { { type = "input_text", text = text } } },
    }),
  }
  assert(vim.fn.writefile(lines, directory .. "/rollout-" .. session_id .. ".jsonl") == 0)
end

local repository_one = root .. "/workspace-one/app"
local repository_two = root .. "/workspace-two/app"
make_workspace("workspace-one", "workspace-one", repository_one)
make_workspace("workspace-two", "workspace-two", repository_two)
local pane_one, command_one, start_one = start_agent_pane("workspace-one", "workspace-one", "a1a1a1a1b2b2c3c3d4d4e5e5")
local pane_two, command_two, start_two = start_agent_pane("workspace-two", "workspace-two", "f6f6f6f6a7a7b8b8c9c9d0d0")
make_agent("a1a1a1a1b2b2c3c3d4d4e5e5", "workspace-one", "session-one", pane_one, command_one, start_one)
make_agent("f6f6f6f6a7a7b8b8c9c9d0d0", "workspace-two", "session-two", pane_two, command_two, start_two)
make_transcript("session-one", repository_one, "Workspace one transcript")
make_transcript("session-discovered", repository_one, "Discovered Workspace one transcript")
make_transcript("session-two", repository_two, "Workspace two transcript")

local directory = repository_one
local select_action = "pick"
local selected_agent_id
local preview_agent_id
local preview_expected
local preview_text
require("twt").setup({
  command = binary,
  default_keymaps = false,
  directory = function() return directory end,
  select = function(items, opts, done)
    local selected
    for _, item in ipairs(items) do
      if item.id == (select_action == "preview" and preview_agent_id or selected_agent_id) then selected = item end
    end
    assert(selected, "the expected Agent Session is not in the picker")
    if select_action == "pick" then
      done(selected)
      return
    end
    assert(opts.kind == "twt_agent_session")
    assert(opts.snacks and opts.snacks.preview and opts.snacks.layout.preset == "default")
    local completed = false
    opts.snacks.preview({
      item = { item = selected },
      picker = { closed = false },
      preview = {
        reset = function() preview_text = "" end,
        set_title = function() end,
        set_lines = function(_, lines)
          preview_text = table.concat(lines, "\n")
          if not completed and preview_text:find(preview_expected, 1, true) then
            completed = true
            vim.schedule(function() done(nil) end)
          end
        end,
        highlight = function(_, value) assert(value.ft == "markdown") end,
      },
    })
  end,
})

local function pick(expected_agent)
  select_action = "pick"
  selected_agent_id = expected_agent
  local finished = false
  local result_error
  require("twt").agents.pick(function(err, result)
    result_error = err
    if not err then assert(result.agent.id == expected_agent) end
    finished = true
  end)
  assert(vim.wait(5000, function() return finished end), "Agent Session picker timed out")
  assert(result_error == nil, result_error)
end

local function preview(expected_agent, expected_text)
  select_action = "preview"
  preview_agent_id = expected_agent
  preview_expected = expected_text
  preview_text = ""
  local finished = false
  local result_error
  require("twt").agents.pick(function(err, result)
    result_error = err
    assert(result == nil)
    finished = true
  end)
  assert(vim.wait(5000, function() return finished end), "Agent Transcript preview timed out")
  assert(result_error == nil, result_error)
  assert(preview_text:find(expected_text, 1, true), preview_text)
end

local function registered_count(workspace_id)
  local result = vim.system({ binary, "agents", "list", "--workspace", workspace_id, "--registered", "--output", "json" }, { text = true }):wait()
  assert(result.code == 0, result.stderr)
  return #(vim.json.decode(result.stdout).agents or {})
end

local before_preview = registered_count("workspace-one")
preview("a1a1a1a1b2b2c3c3d4d4e5e5", "Workspace one transcript")
preview("session-discovered", "Discovered Workspace one transcript")
assert(registered_count("workspace-one") == before_preview, "preview adopted the discovered Agent Session")

pick("a1a1a1a1b2b2c3c3d4d4e5e5")
directory = repository_two
pick("f6f6f6f6a7a7b8b8c9c9d0d0")

local first = snapshots .. "/workspace-one/latest.md"
local second = snapshots .. "/workspace-two/latest.md"
assert(table.concat(vim.fn.readfile(first), "\n"):find("Workspace one transcript", 1, true))
assert(table.concat(vim.fn.readfile(second), "\n"):find("Workspace two transcript", 1, true))
assert(not table.concat(vim.fn.readfile(first), "\n"):find("Workspace two transcript", 1, true))
assert(vim.uv.fs_stat(first).mode % 512 == 384)
assert(vim.uv.fs_stat(vim.fs.dirname(first)).mode % 512 == 448)

local function send_feedback(workspace_directory, text)
  directory = workspace_directory
  local finished = false
  local result_error
  require("twt").agents.send(text, function(err)
    result_error = err
    finished = true
  end)
  assert(vim.wait(5000, function() return finished end), "Agent Session feedback timed out")
  assert(result_error == nil, result_error)
end

send_feedback(repository_one, "Feedback for Workspace one")
send_feedback(repository_two, "Feedback for Workspace two")
assert(tmux({ "capture-pane", "-p", "-t", pane_one }):find("Feedback for Workspace one", 1, true))
assert(not tmux({ "capture-pane", "-p", "-t", pane_one }):find("Feedback for Workspace two", 1, true))
assert(tmux({ "capture-pane", "-p", "-t", pane_two }):find("Feedback for Workspace two", 1, true))

tmux({ "kill-server" })

print("twt.nvim two-Workspace integration: ok")
