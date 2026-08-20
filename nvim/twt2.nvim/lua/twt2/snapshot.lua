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

function M.path(project_id)
  if not valid_project_id(project_id) then
    return nil, "twt2 returned an invalid Project ID"
  end
  return root() .. "/" .. project_id .. "/latest.md"
end

function M.open(project_id, project_directory)
  local path, err = M.path(project_id)
  if not path then return nil, err end
  if vim.fn.filereadable(path) ~= 1 then
    return nil, "this Project has no transcript snapshot"
  end
  vim.cmd(config.get().snapshot_split .. " " .. vim.fn.fnameescape(path))
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
