local config = require("twt2.config")
local M = {}

local function message(result)
  for _, value in ipairs({ result.stderr, result.stdout }) do
    if value and value ~= "" then
      local ok, decoded = pcall(vim.json.decode, value)
      if ok and decoded.error and decoded.error.message then
        return decoded.error.message
      end
      return vim.trim(value)
    end
  end
  return "twt2 returned no error details"
end

function M.request(args, opts, done)
  opts = opts or {}
  local cfg = config.get()
  local argv = { cfg.command }
  vim.list_extend(argv, args)
  vim.list_extend(argv, { "--output", "json" })
  local function finish(result)
    if result.code ~= 0 then
      done(message(result))
      return
    end
    if not result.stdout or result.stdout == "" then
      done("twt2 returned empty JSON output")
      return
    end
    local ok, value = pcall(vim.json.decode, result.stdout)
    if not ok or type(value) ~= "table" then
      done("twt2 returned invalid JSON")
      return
    end
    if value.schemaVersion ~= 1 then
      done("twt2 JSON schema version is not supported")
      return
    end
    done(nil, value)
  end
  if cfg.runner then
    cfg.runner(argv, { cwd = opts.cwd, stdin = opts.stdin }, finish)
    return
  end
  if not vim.system then
    done("twt2.nvim needs Neovim 0.10 or later")
    return
  end
  vim.system(argv, { cwd = opts.cwd, stdin = opts.stdin, text = true }, vim.schedule_wrap(finish))
end

function M.context(directory, done)
  M.request({ "context", "--directory", directory }, { cwd = directory }, done)
end

return M
