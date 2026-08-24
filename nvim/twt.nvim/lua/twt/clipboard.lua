local config = require("twt.config")
local M = {}

-- Copies text through the configured Neovim clipboard adapter. The adapter is
-- synchronous so a successful return means that Neovim accepted the text.
function M.copy(text, done)
  done = done or function() end
  local ok, err = pcall(config.get().clipboard, text)
  if not ok then
    done("could not copy review notes: " .. tostring(err))
    return
  end
  done(nil)
end

return M
