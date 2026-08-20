# twt.nvim preview

This Neovim plug-in connects review notes to the Agent Sessions of the current
`twt` Project. It does not read twt state files or call tmux. All Project,
Agent Session, resume, focus, and feedback work goes through the versioned
`twt` JSON interface.

## Install with lazy.nvim

```lua
{
  dir = vim.fn.expand("~/code/tmux-worktree/nvim/twt.nvim"),
  config = function()
    require("twt").setup()
  end,
}
```

The plug-in needs Neovim 0.10 or later and a `twt` executable in `PATH`.
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
| `:TwtAgents` | Select an Agent Session and open its transcript |
| `:TwtNote` | Add a multi-line review note |
| `:TwtReview` | Send the current Project review batch |
| `:TwtSend` | Write free text in a window and send it |
| `:TwtNotes` | List the review notes of this Project |
| `:TwtResume` | Resume the selected Agent Session |
| `:TwtFocus` | Focus the selected Agent Session |
| `:TwtRefresh` | Write a new transcript snapshot for the selected Agent Session |
| `:TwtClear` | Clear review notes |

Each mapping has a command. The commands and the mappings do the same work and
show the same messages.

`:TwtNotes` shows the notes of the current Project. Select a note, then
select `Delete` or `Go to the line`.

If the selected Agent Session is not live, but it can resume, a send asks you
first: `The Agent Session is not live. Resume and send?`. Answer yes to resume
the Agent Session and to send the same text again. The plug-in keeps the review
notes if the send fails.

The plug-in emits the `TwtRefresh` User event after each successful selection,
send, resume, focus, and refresh. Use it to update your statusline:

```lua
vim.api.nvim_create_autocmd("User", {
  pattern = "TwtRefresh",
  callback = function() vim.cmd("redrawstatus") end,
})
```

## Statusline accessor

```lua
local status = require("twt").agents.status()
-- nil, or { label = "review", live = true }
```

`status()` reads only local memory. It never starts `twt`. It shows the state
of the last interaction with the Agent Session list, so the `live` field can be
old. A selection, a send, a resume, or a refresh makes it current again.

A snapshot buffer sets `autoread`, and the plug-in runs `checktime` on
`FocusGained` and `CursorHold`. A snapshot buffer shows the new text without a
manual reload.

Agent selection, transcript snapshots, and review notes are scoped by immutable Project ID. Each note
also contains the repository name. Extmarks keep note lines current after file
edits. A successful send completes and clears the batch. A failed or uncertain
send keeps the batch. The plug-in does not retry a send.

`twt` reads the linked provider transcript, checks its Project, and writes
the private Markdown snapshot to
`$TWT_STATE_DIR/snapshots/projects/PROJECT_ID/agents/AGENT_ID.md`. The
plug-in opens the exact file that the `path` field of the snapshot response
names, so each Agent Session keeps its own file. If an older `twt` returns no
`path`, the plug-in falls back to the shared
`$TWT_STATE_DIR/snapshots/projects/PROJECT_ID/latest.md` file. Archive keeps
the files. Applied Project removal deletes them. Register a new Agent Session
with `--session SESSION_ID`, or use `twt agents transcript link` for an
existing record.
Transcript loading supports Codex and Claude. Cursor transcript loading stays
off because its local records do not contain a safe, exact Project directory.

Older preview versions used the Neovim state directory. twt cannot reliably
find that path when `NVIM_APPNAME` changes. You can remove those old preview
files manually after you confirm that the new snapshot exists.

## Lua interface

```lua
local twt = require("twt")

twt.agents.pick(done)
twt.agents.prompt_send(done)
twt.agents.refresh(done)
twt.agents.resume(done)
twt.agents.focus(done)
twt.agents.status()
twt.review.prompt_add(done)
twt.review.prompt_notes(done)
twt.review.send(done)
twt.review.list()
twt.review.delete(note_id)
twt.review.jump(note_id)
twt.review.clear()
```

Each `done` is error-first: `done(err)` for a failure, or `done(nil, result)`
for a success. `done` is optional. These modules show no message, so the caller
decides what the user sees.

`pick` and `refresh` give
`done(nil, { agent = agent, path = path })` with the Agent Session and the
snapshot file that they opened. A canceled picker gives `done(nil)` with no
result. A canceled text window gives no answer, so `prompt_add` and
`prompt_send` show nothing after `q`.

`setup` accepts a `confirm` function for the resume question:

```lua
require("twt").setup({
  confirm = function(question, done)
    done(vim.fn.confirm(question, "&Yes\n&No", 2) == 1)
  end,
})
```

Run both headless tests. The second test uses the real `twt` binary and two separate Projects:

```sh
./nvim/twt.nvim/tests/test.sh
```
