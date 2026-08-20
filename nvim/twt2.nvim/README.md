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
| `<leader>ars` | Write free text in a window and send it |
| `<leader>aru` | Resume the selected Agent Session |
| `<leader>arf` | Focus the selected Agent Session |
| `<leader>arR` | Write a new transcript snapshot for the selected Agent Session |
| `<leader>arx` | Clear review notes |

The note window and the message window use the same keys. Press `<C-s>` to
accept the text. Press `q` to close the window and to keep no text.

Set `default_keymaps = false` to remove these mappings. The commands stay
available:

| Command | Action |
| --- | --- |
| `:Twt2Agents` | Select an Agent Session and open its transcript |
| `:Twt2Send` | Write free text in a window and send it |
| `:Twt2Notes` | List the review notes of this Project |
| `:Twt2Resume` | Resume the selected Agent Session |
| `:Twt2Refresh` | Write a new transcript snapshot for the selected Agent Session |

`:Twt2Notes` shows the notes of the current Project. Select a note, then
select `Delete` or `Go to the line`.

If the selected Agent Session is not live, but it can resume, a send asks you
first: `The Agent Session is not live. Resume and send?`. Answer yes to resume
the Agent Session and to send the same text again. The plug-in keeps the review
notes if the send fails.

The plug-in emits the `Twt2Refresh` User event after each successful selection,
send, resume, focus, and refresh. Use it to update your statusline:

```lua
vim.api.nvim_create_autocmd("User", {
  pattern = "Twt2Refresh",
  callback = function() vim.cmd("redrawstatus") end,
})
```

## Statusline accessor

```lua
local status = require("twt2").agents.status()
-- nil, or { label = "review", live = true }
```

`status()` reads only local memory. It never starts `twt2`. It shows the state
of the last interaction with the Agent Session list, so the `live` field can be
old. A selection, a send, a resume, or a refresh makes it current again.

A snapshot buffer sets `autoread`, and the plug-in runs `checktime` on
`FocusGained` and `CursorHold`. A snapshot buffer shows the new text without a
manual reload.

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
twt2.agents.prompt_send()
twt2.agents.refresh()
twt2.agents.resume()
twt2.agents.focus()
twt2.agents.status()
twt2.review.prompt_add()
twt2.review.prompt_notes()
twt2.review.send()
twt2.review.list()
twt2.review.delete(note_id)
twt2.review.jump(note_id)
twt2.review.clear()
```

`setup` accepts a `confirm` function for the resume question:

```lua
require("twt2").setup({
  confirm = function(question, done)
    done(vim.fn.confirm(question, "&Yes\n&No", 2) == 1)
  end,
})
```

Run both headless tests. The second test uses the real `twt2` binary and two separate Projects:

```sh
./nvim/twt2.nvim/tests/test.sh
```
