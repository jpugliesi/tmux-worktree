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
| `<leader>arp` | Select an Agent Session and open its transcript |
| `<leader>an` | Add a multi-line review note |
| `<leader>arr` | Send the current Project review batch |
| `<leader>aru` | Resume the selected Agent Session |
| `<leader>arf` | Focus the selected Agent Session |
| `<leader>arx` | Clear review notes |

Agent selection, transcript snapshots, and review notes are scoped by immutable Project ID. Each note
also contains the repository name. Extmarks keep note lines current after file
edits. A successful send completes and clears the batch. A failed or uncertain
send keeps the batch. The plug-in does not retry a send.

`twt2` reads the linked provider transcript, checks its Project, and writes
the private Markdown snapshot to
`$TWT2_STATE_DIR/snapshots/projects/PROJECT_ID/latest.md`. The plug-in only
opens that file. It uses a different file for each Project. Archive keeps the
file. Applied Project removal deletes it. Register a new Agent Session with `--session
SESSION_ID`, or use `twt2 agents transcript link` for an existing record.
Transcript loading supports Codex and Claude. Cursor transcript loading stays
off because its local records do not contain a safe, exact Project directory.

Older preview versions used the Neovim state directory. twt2 cannot reliably
find that path when `NVIM_APPNAME` changes. You can remove those old preview
files manually after you confirm that the new snapshot exists.

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

Run both headless tests. The second test uses the real `twt2` binary and two separate Projects:

```sh
./nvim/twt2.nvim/tests/test.sh
```
