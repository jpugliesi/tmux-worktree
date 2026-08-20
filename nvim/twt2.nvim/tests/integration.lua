local plugin = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(plugin)

local binary = assert(vim.env.TWT2_TEST_BINARY, "TWT2_TEST_BINARY is required")
local root = vim.fn.tempname()
local state = root .. "/state"
local data = root .. "/data"
local config = root .. "/config"
local home = root .. "/home"
local snapshots = root .. "/snapshots"

for _, directory in ipairs({ state .. "/projects", state .. "/agents", data, config, home }) do
  assert(vim.fn.mkdir(directory, "p") == 1 or vim.fn.isdirectory(directory) == 1)
end

vim.env.HOME = home
vim.env.TWT2_STATE_DIR = state
vim.env.TWT2_DATA_DIR = data
vim.env.TWT2_CONFIG_DIR = config
local tmux_socket = "twt2-nvim-test-" .. tostring(vim.fn.getpid())
vim.env.TWT2_TMUX_SOCKET = tmux_socket

local function tmux(arguments)
  local command = { "tmux", "-L", tmux_socket, "-f", "/dev/null" }
  vim.list_extend(command, arguments)
  local result = vim.system(command, { text = true }):wait()
  assert(result.code == 0, result.stderr)
  return vim.trim(result.stdout)
end

local function start_agent_pane(project_id, session_name, agent_id)
  local pane = tmux({ "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", session_name, "--", "cat" })
  tmux({ "set-option", "-t", session_name, "@twt2_project_id", project_id })
  tmux({ "set-option", "-p", "-t", pane, "@twt2_agent_id", agent_id })
  local process = vim.split(tmux({ "display-message", "-p", "-t", pane, "#{pane_current_command}\t#{pane_start_command}" }), "\t", { plain = true })
  assert(#process == 2)
  return pane, process[1], process[2]
end

local function write_json(path, value)
  assert(vim.fn.writefile({ vim.json.encode(value) }, path) == 0)
end

local function make_project(id, name, repository)
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

local function make_agent(id, project_id, session_id, pane, pane_command, pane_start)
  write_json(state .. "/agents/" .. id .. ".json", {
    version = 1,
    id = id,
    projectId = project_id,
    provider = "codex",
    label = project_id .. " review",
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

local repository_one = root .. "/project-one/app"
local repository_two = root .. "/project-two/app"
make_project("project-one", "project-one", repository_one)
make_project("project-two", "project-two", repository_two)
local pane_one, command_one, start_one = start_agent_pane("project-one", "project-one", "agent-one")
local pane_two, command_two, start_two = start_agent_pane("project-two", "project-two", "agent-two")
make_agent("agent-one", "project-one", "session-one", pane_one, command_one, start_one)
make_agent("agent-two", "project-two", "session-two", pane_two, command_two, start_two)
make_transcript("session-one", repository_one, "Project one transcript")
make_transcript("session-two", repository_two, "Project two transcript")

local directory = repository_one
require("twt2").setup({
  command = binary,
  default_keymaps = false,
  snapshot_root = snapshots,
  directory = function() return directory end,
  select = function(items, _, done) done(items[1]) end,
})

local function pick(expected_agent)
  local finished = false
  local result_error
  require("twt2").agents.pick(function(agent, err)
    result_error = err
    assert(agent and agent.id == expected_agent)
    finished = true
  end)
  assert(vim.wait(5000, function() return finished end), "Agent Session picker timed out")
  assert(result_error == nil, result_error)
end

pick("agent-one")
directory = repository_two
pick("agent-two")

local first = snapshots .. "/project-one/latest.md"
local second = snapshots .. "/project-two/latest.md"
assert(table.concat(vim.fn.readfile(first), "\n"):find("Project one transcript", 1, true))
assert(table.concat(vim.fn.readfile(second), "\n"):find("Project two transcript", 1, true))
assert(not table.concat(vim.fn.readfile(first), "\n"):find("Project two transcript", 1, true))
assert(vim.uv.fs_stat(first).mode % 512 == 384)
assert(vim.uv.fs_stat(vim.fs.dirname(first)).mode % 512 == 448)

local function send_feedback(project_directory, text)
  directory = project_directory
  local finished = false
  local result_error
  require("twt2").agents.send(text, function(err)
    result_error = err
    finished = true
  end)
  assert(vim.wait(5000, function() return finished end), "Agent Session feedback timed out")
  assert(result_error == nil, result_error)
end

send_feedback(repository_one, "Feedback for Project one")
send_feedback(repository_two, "Feedback for Project two")
assert(tmux({ "capture-pane", "-p", "-t", pane_one }):find("Feedback for Project one", 1, true))
assert(not tmux({ "capture-pane", "-p", "-t", pane_one }):find("Feedback for Project two", 1, true))
assert(tmux({ "capture-pane", "-p", "-t", pane_two }):find("Feedback for Project two", 1, true))

tmux({ "kill-server" })

print("twt2.nvim two-Project integration: ok")
