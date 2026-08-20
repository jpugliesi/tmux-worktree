local config = require("twt2.config")
local M = {}

local function valid_project_id(project_id)
  return type(project_id) == "string" and project_id:match("^[A-Za-z0-9_-]+$") ~= nil
end

local function root()
  return config.get().snapshot_root or (vim.fn.stdpath("state") .. "/twt2/projects")
end

function M.path(project_id)
  if not valid_project_id(project_id) then
    return nil, "twt2 returned an invalid Project ID"
  end
  return root() .. "/" .. project_id .. "/latest.md"
end

function M.write(project_id, markdown)
  local path, err = M.path(project_id)
  if not path then return nil, err end
  if type(markdown) ~= "string" or markdown == "" then
    return nil, "twt2 returned an empty Agent Session transcript"
  end
  local directory = vim.fs.dirname(path)
  if vim.fn.mkdir(directory, "p", 448) == 0 and vim.fn.isdirectory(directory) ~= 1 then
    return nil, "could not create the Project transcript directory"
  end
  if not vim.uv.fs_chmod(directory, 448) then
    return nil, "could not protect the Project transcript directory"
  end
  local temporary = path .. "." .. tostring(vim.uv.hrtime()) .. ".tmp"
  local ok, result = pcall(vim.fn.writefile, vim.split(markdown, "\n", { plain = true }), temporary, "b")
  if not ok or result ~= 0 then return nil, "could not write the Project transcript file" end
  if not vim.uv.fs_chmod(temporary, 384) then
    os.remove(temporary)
    return nil, "could not protect the Project transcript file"
  end
  local renamed, rename_err = vim.uv.fs_rename(temporary, path)
  if not renamed then
    os.remove(temporary)
    return nil, tostring(rename_err)
  end
  return path
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
  vim.b.twt2_project_id = project_id
  vim.b.twt2_project_directory = project_directory
  vim.cmd("normal! G")
  return path
end

return M
