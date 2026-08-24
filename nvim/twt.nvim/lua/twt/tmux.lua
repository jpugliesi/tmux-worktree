local config = require("twt.config")
local M = {}

local separator = string.char(31)
local buffer_sequence = 0

local function message(result)
  for _, value in ipairs({ result.stderr, result.stdout }) do
    if value and value ~= "" then return vim.trim(value) end
  end
  return "tmux returned no error details"
end

local function request(args, opts, done)
  opts = opts or {}
  local cfg = config.get()
  local argv = { cfg.tmux_command }
  vim.list_extend(argv, args)
  local function finish(result)
    if result.code ~= 0 then
      done(message(result))
      return
    end
    done(nil, result.stdout or "")
  end
  if cfg.tmux_runner then
    cfg.tmux_runner(argv, { stdin = opts.stdin }, finish)
    return
  end
  if not vim.system then
    done("twt.nvim needs Neovim 0.10 or later")
    return
  end
  vim.system(argv, { stdin = opts.stdin, text = true }, vim.schedule_wrap(finish))
end

local function clean_label(value)
  return vim.trim((value or ""):gsub("[%c]", " "):gsub(" +", " "))
end

function M.available()
  return vim.env.TMUX ~= nil and vim.env.TMUX ~= ""
end

-- Lists live panes other than the pane that owns this Neovim process. Only
-- the validated pane ID is used as a later tmux target; labels are display
-- text only.
function M.list(done)
  done = done or function() end
  if not M.available() then
    done("Neovim is not running in tmux")
    return
  end
  if not vim.env.TMUX_PANE or not vim.env.TMUX_PANE:match("^%%%d+$") then
    done("tmux did not identify the current Neovim pane")
    return
  end
  local format = table.concat({
    "#{pane_id}",
    "#{session_name}:#{window_index}.#{pane_index}",
    "#{pane_current_command}",
    "#{pane_current_path}",
    "#{pane_dead}",
  }, separator)
  request({ "list-panes", "-a", "-F", format }, nil, function(err, output)
    if err then
      done("could not list tmux panes: " .. err)
      return
    end
    local panes = {}
    for _, row in ipairs(vim.split(output, "\n", { plain = true, trimempty = true })) do
      local fields = vim.split(row, separator, { plain = true })
      local id = fields[1]
      if #fields == 5 and id and id:match("^%%%d+$") and id ~= vim.env.TMUX_PANE and fields[5] == "0" then
        local target = clean_label(fields[2])
        local command = clean_label(fields[3])
        local path = clean_label(fields[4])
        panes[#panes + 1] = {
          id = id,
          target = target,
          command = command,
          path = path,
          label = string.format("%s · %s · %s · %s", target, id, command, path),
        }
      end
    end
    done(nil, panes)
  end)
end

local function private_buffer_name()
  buffer_sequence = buffer_sequence + 1
  return string.format("twt-nvim-review-%d-%s-%d", vim.fn.getpid(), tostring(vim.uv.hrtime()), buffer_sequence)
end

local function delete_buffer(name, done)
  request({ "delete-buffer", "-b", name }, nil, done)
end

-- Sends text to one validated pane through a private tmux buffer. -p requests
-- bracketed paste, and -d removes the buffer after a successful paste.
function M.send(pane, text, done)
  done = done or function() end
  if type(pane) ~= "string" or not pane:match("^%%%d+$") then
    done("tmux returned an invalid pane ID")
    return
  end
  if pane == vim.env.TMUX_PANE then
    done("cannot send review text to the current Neovim pane")
    return
  end
  if not text or text == "" then
    done("review text is empty")
    return
  end
  local buffer = private_buffer_name()
  local function fail(err)
    delete_buffer(buffer, function(cleanup_err)
      if cleanup_err then
        err = err .. "; could not delete the private tmux buffer: " .. cleanup_err
      end
      done(err)
    end)
  end
  request({ "load-buffer", "-b", buffer, "-" }, { stdin = text }, function(load_err)
    if load_err then
      fail("could not load review text into tmux: " .. load_err)
      return
    end
    request({ "paste-buffer", "-d", "-p", "-b", buffer, "-t", pane }, nil, function(paste_err)
      if paste_err then
        fail("could not paste review text into pane " .. pane .. ": " .. paste_err)
        return
      end
      request({ "send-keys", "-t", pane, "Enter" }, nil, function(submit_err)
        if submit_err then
          done("review text was pasted into pane " .. pane .. " but tmux could not submit it: " .. submit_err)
          return
        end
        done(nil, pane)
      end)
    end)
  end)
end

function M.pick(done)
  done = done or function() end
  M.list(function(err, panes)
    if err then done(err); return end
    if #panes == 0 then
      done("no other live tmux panes are available")
      return
    end
    config.get().select(panes, {
      prompt = "Select a tmux pane",
      format_item = function(pane) return pane.label end,
    }, function(pane)
      done(nil, pane)
    end)
  end)
end

return M
