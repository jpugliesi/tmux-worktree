local plugin = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(plugin)

local failures = {}
local function test(name, body)
  local ok, err = pcall(body)
  if not ok then failures[#failures + 1] = name .. ": " .. tostring(err) end
end

local function with_config(overrides, body)
  local config = require("twt.config").get()
  local saved = {}
  for key in pairs(overrides) do saved[key] = config[key] end
  for key, value in pairs(overrides) do config[key] = value end
  local ok, err = pcall(body)
  for key in pairs(overrides) do config[key] = saved[key] end
  if not ok then error(err, 0) end
end

local context = {
  schemaVersion = 2,
  workspace = { id = "workspace-1", name = "change-one" },
  repositoryName = "app",
}
local agents_response = {
  schemaVersion = 2,
  workspaceId = "workspace-1",
  complete = true,
  totalCount = 1,
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
local calls = {}
local function runner(argv, opts, done)
  calls[#calls + 1] = { argv = vim.deepcopy(argv), stdin = opts.stdin, cwd = opts.cwd }
  local joined = table.concat(argv, " ")
  local value = joined:find(" context ", 1, true) and context
    or joined:find(" agents list ", 1, true) and agents_response
    or { schemaVersion = 2, status = "sent", agentId = argv[4] }
  done({ code = 0, stdout = vim.json.encode(value), stderr = "" })
end

require("twt").setup({
  command = "/test/twt",
  default_keymaps = false,
  directory = function() return "/work/app" end,
  runner = runner,
})

test("sends through the only live Agent Session without a prior selection", function()
  local picker_opened = false
  with_config({ select = function() picker_opened = true end }, function()
    require("twt").agents.send("first review", function(err) assert(err == nil, err) end)
  end)
  assert(not picker_opened, "one live Agent Session must not open a picker")
  local listed, sent = calls[#calls - 1], calls[#calls]
  assert(table.concat(listed.argv, " "):find("agents list %-%-workspace workspace%-1 %-%-limit 0"), vim.inspect(listed.argv))
  assert(sent.stdin == "first review")
  assert(table.concat(sent.argv, " "):find("agents send agent%-1"), vim.inspect(sent.argv))
end)

test("picks a live send target without opening an Agent Preview", function()
  local send_context = {
    schemaVersion = 2,
    workspace = { id = "workspace-send", name = "send" },
    repositoryName = "app",
  }
  local candidates = {
    vim.tbl_deep_extend("force", vim.deepcopy(agents_response.agents[1]), {
      id = "pane-v1-first", workspaceId = "workspace-send", provider = "claude", label = "first",
      registration = "discovered", runtime = "live", capabilities = { canResume = true, canSend = true },
    }),
    vim.tbl_deep_extend("force", vim.deepcopy(agents_response.agents[1]), {
      id = "pane-v1-second", workspaceId = "workspace-send", label = "second",
      registration = "discovered", runtime = "live", capabilities = { canResume = true, canSend = true },
    }),
  }
  local selected_opts
  local sent_ids = {}
  local preview_or_adopt = false
  with_config({
    directory = function() return "/work/send" end,
    select = function(items, opts, done) selected_opts = opts; done(items[2]) end,
    runner = function(argv, _, done)
      local joined = table.concat(argv, " ")
      if joined:find(" context ", 1, true) then
        done({ code = 0, stdout = vim.json.encode(send_context), stderr = "" })
      elseif joined:find(" agents list ", 1, true) then
        done({ code = 0, stdout = vim.json.encode({
          schemaVersion = 2, workspaceId = "workspace-send", agents = candidates,
          complete = true, totalCount = #candidates,
        }), stderr = "" })
      elseif joined:find(" agents send ", 1, true) then
        sent_ids[#sent_ids + 1] = argv[4]
        if argv[4] == "pane-v1-second" then
          candidates = { vim.tbl_deep_extend("force", vim.deepcopy(candidates[2]), {
            id = "registered-second", registration = "registered",
          }) }
        end
        done({ code = 0, stdout = vim.json.encode({
          schemaVersion = 2, status = "sent", agentId = "registered-second",
        }), stderr = "" })
      else
        preview_or_adopt = true
        done({ code = 1, stdout = "", stderr = "unexpected Agent action" })
      end
    end,
  }, function()
    require("twt").agents.send("pick one", function(err) assert(err == nil, err) end)
    require("twt").agents.send("use remembered ID", function(err) assert(err == nil, err) end)
  end)
  assert(selected_opts and selected_opts.kind == "twt_agent_send", vim.inspect(selected_opts))
  assert(not selected_opts.snacks, "the send picker must not configure an Agent Preview")
  assert(not preview_or_adopt, "the plugin must not preview or adopt before the send")
  assert(vim.deep_equal(sent_ids, { "pane-v1-second", "registered-second" }), vim.inspect(sent_ids))
end)

test("reports when no live Agent Session can receive feedback", function()
  local saved = agents_response.agents
  agents_response.agents = {
    vim.tbl_deep_extend("force", vim.deepcopy(saved[1]), {
      status = "stopped",
      capabilities = { canResume = false, canSend = false, canFocus = false, canReadTranscript = true },
    }),
  }
  local received
  require("twt").agents.send("review", function(err) received = err end)
  agents_response.agents = saved
  assert(received == "this Workspace has no live Agent Session that can receive feedback", tostring(received))
end)

test("keeps Review Notes when the Agent send picker is canceled", function()
  local review = require("twt").review
  local buffer = vim.api.nvim_create_buf(true, false)
  vim.api.nvim_buf_set_name(buffer, vim.fn.tempname() .. ".md")
  vim.api.nvim_buf_set_lines(buffer, 0, -1, false, { "keep this" })
  vim.api.nvim_set_current_buf(buffer)
  review.add("canceled Agent send", 1, 1, function(err) assert(err == nil, err) end)
  local sent = false
  local result
  with_config({
    directory = function() return "/work/cancel" end,
    select = function(_, opts, done) assert(opts.kind == "twt_agent_send"); done(nil) end,
    runner = function(argv, _, done)
      local joined = table.concat(argv, " ")
      if joined:find(" context ", 1, true) then
        done({ code = 0, stdout = vim.json.encode({
          schemaVersion = 2,
          workspace = { id = "workspace-cancel", name = "cancel" },
          repositoryName = "app",
        }), stderr = "" })
      elseif joined:find(" agents list ", 1, true) then
        local first = vim.tbl_deep_extend("force", vim.deepcopy(agents_response.agents[1]), {
          id = "cancel-first", workspaceId = "workspace-cancel",
        })
        local second = vim.tbl_deep_extend("force", vim.deepcopy(first), { id = "cancel-second" })
        done({ code = 0, stdout = vim.json.encode({
          schemaVersion = 2, workspaceId = "workspace-cancel", agents = { first, second },
          complete = true, totalCount = 2,
        }), stderr = "" })
      else
        sent = true
        done({ code = 1, stdout = "", stderr = "unexpected send" })
      end
    end,
  }, function()
    review.send(function(err, value) assert(err == nil, err); result = value end)
  end)
  assert(result and result.canceled, vim.inspect(result))
  assert(not sent, "a canceled picker must not send")
  assert(#review.list() == 1, "a canceled picker must keep the Review Batch")
end)

if #failures > 0 then error(table.concat(failures, "\n")) end
print("twt.nvim Agent send tests: ok")
