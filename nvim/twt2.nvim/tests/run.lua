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
      capabilities = { canResume = true, canSend = true, canFocus = true },
    },
  },
}

local function runner(argv, opts, done)
  calls[#calls + 1] = { argv = vim.deepcopy(argv), stdin = opts.stdin, cwd = opts.cwd }
  local joined = table.concat(argv, " ")
  local value = joined:find(" context ", 1, true) and context
    or joined:find(" agents list ", 1, true) and agents_response
    or { schemaVersion = 1, status = "sent", agentId = "agent-1" }
  done({ code = 0, stdout = vim.json.encode(value), stderr = "" })
end

require("twt2").setup({
  command = "/test/twt2",
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
  assert(table.concat(sent.argv, " ") == "/test/twt2 agents send agent-1 --stdin --output json")
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
  assert(prompt:find("app:src/file.go#L3", 1, true))
  assert(prompt:find("two", 1, true))
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
