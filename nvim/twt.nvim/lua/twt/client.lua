local config = require("twt.config")
local M = {}

local function request_error(result)
  for _, value in ipairs({ result.stderr, result.stdout }) do
    if value and value ~= "" then
      local ok, decoded = pcall(vim.json.decode, value)
      if ok and type(decoded) == "table" and type(decoded.error) == "table" and decoded.error.message then
        return decoded.error.message, decoded.error.code
      end
      return vim.trim(value), nil
    end
  end
  return "twt returned no error details", nil
end

function M.request(args, opts, done)
  opts = opts or {}
  local cfg = config.get()
  local argv = { cfg.command }
  vim.list_extend(argv, args)
  vim.list_extend(argv, { "--output", "json" })
  local function finish(result)
    if result.code ~= 0 then
      local err, code = request_error(result)
      done(err, nil, code)
      return
    end
    if not result.stdout or result.stdout == "" then
      done("twt returned empty JSON output")
      return
    end
    local ok, value = pcall(vim.json.decode, result.stdout)
    if not ok or type(value) ~= "table" then
      done("twt returned invalid JSON")
      return
    end
    if value.schemaVersion ~= 2 then
      done("twt JSON schema version is not supported")
      return
    end
    done(nil, value)
  end
  if cfg.runner then
    cfg.runner(argv, { cwd = opts.cwd, stdin = opts.stdin }, finish)
    return
  end
  if not vim.system then
    done("twt.nvim needs Neovim 0.10 or later")
    return
  end
  vim.system(argv, { cwd = opts.cwd, stdin = opts.stdin, text = true }, vim.schedule_wrap(finish))
end

function M.context(directory, done)
  M.request({ "context", "--directory", directory }, { cwd = directory }, done)
end

-- Reads the Workspace context of `fixed_directory`, or of the directory that the
-- current buffer selects. It answers `done(err)`, or
-- `done(nil, context, directory)` with the directory that it used.
function M.workspace_context(done, fixed_directory)
  local directory = fixed_directory or config.get().directory()
  M.context(directory, function(err, context)
    if err then
      done(err)
      return
    end
    done(nil, context, directory)
  end)
end

return M
