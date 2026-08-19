# twt2.nvim preview

This Neovim plug-in connects review notes to the Agent Sessions of the current
`twt2` Project. It does not read twt2 state files or call tmux. All Project,
Agent Session, resume, focus, and feedback work goes through the versioned
`twt2` JSON interface.

## Install with lazy.nvim

```lua
{
  dir = vim.fn.expand("~/code/tmux-worktree/nvim/twt2.nvim"),
  config = function()
    require("twt2").setup()
  end,
}
```

The plug-in needs Neovim 0.10 or later and a `twt2` executable in `PATH`.
It uses core `vim.ui.select`, so a picker provider is optional.

## Main mappings

| Mapping | Action |
| --- | --- |
| `<leader>arp` | Select an Agent Session for the current Project |
| `<leader>an` | Add a multi-line review note |
| `<leader>arr` | Send the current Project review batch |
| `<leader>aru` | Resume the selected Agent Session |
| `<leader>arf` | Focus the selected Agent Session |
| `<leader>arx` | Clear review notes |

Agent selection and review notes are scoped by immutable Project ID. Each note
also contains the repository name. Extmarks keep note lines current after file
edits. A successful send completes and clears the batch. A failed or uncertain
send keeps the batch. The plug-in does not retry a send.

This preview does not read coding-agent transcript files. Keep the old
transcript picker if you need transcript viewing. A later version can add that
feature after `twt2` has a provider-neutral transcript command.

## Lua interface

```lua
local twt2 = require("twt2")

twt2.agents.pick()
twt2.agents.resume()
twt2.agents.focus()
twt2.review.prompt_add()
twt2.review.send()
twt2.review.clear()
```

Run the headless test:

```sh
nvim --headless -u NONE -l nvim/twt2.nvim/tests/run.lua
```
