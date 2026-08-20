local config = require("twt2.config")
local M = {}

local function valid_project_id(project_id)
  return type(project_id) == "string" and project_id:match("^[A-Za-z0-9_-]+$") ~= nil
end

local function state_dir()
  if vim.env.TWT2_STATE_DIR and vim.env.TWT2_STATE_DIR ~= "" then
    return vim.env.TWT2_STATE_DIR
  end
  local state_home = vim.env.XDG_STATE_HOME
  if not state_home or state_home == "" then
    state_home = vim.fn.expand("~/.local/state")
  end
  return state_home .. "/twt2"
end

local function root()
  return state_dir() .. "/snapshots/projects"
end

-- Fallback only: derives the shared per-Project latest.md path the same way
-- an older twt2 binary lays it out. A current twt2 returns the per-agent
-- snapshot path directly in the `agents transcript snapshot` JSON response,
-- so callers should prefer that path and reach for this only when the
-- response has no `path` field.
function M.fallback_path(project_id)
  if not valid_project_id(project_id) then
    return nil, "twt2 returned an invalid Project ID"
  end
  return root() .. "/" .. project_id .. "/latest.md"
end

-- Opens (or reloads) the snapshot at `path`. If that file is already visible
-- in a window, that window is reused instead of opening another split.
function M.open(path, project_id, project_directory)
  if not path or path == "" then
    return nil, "twt2 returned no transcript snapshot path"
  end
  if vim.fn.filereadable(path) ~= 1 then
    return nil, "this Project has no transcript snapshot"
  end
  local bufnr = vim.fn.bufnr(path)
  local wins = bufnr ~= -1 and vim.fn.win_findbuf(bufnr) or {}
  if wins[1] then
    vim.api.nvim_set_current_win(wins[1])
  else
    vim.cmd(config.get().snapshot_split .. " " .. vim.fn.fnameescape(path))
  end
  vim.bo.modifiable = true
  vim.bo.readonly = false
  vim.cmd("edit!")
  vim.bo.filetype = "markdown"
  vim.bo.modifiable = false
  vim.bo.readonly = true
  vim.bo.autoread = true
  vim.b.twt2_project_id = project_id
  vim.b.twt2_project_directory = project_directory
  vim.cmd("normal! G")
  return path
end

return M
