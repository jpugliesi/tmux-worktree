# twt.nvim preview

This Neovim plug-in collects one Review Batch from regular file buffers in the
current Neovim session. Review Notes do not need a Git repository or a `twt`
Workspace. You can copy the batch or send it directly to a tmux pane.

The Agent Session, transcript, resume, focus, and Agent feedback features are
optional `twt` integrations. They use the versioned `twt` JSON interface and
never read `twt` state files directly.

## Install with lazy.nvim

```lua
{
  dir = vim.fn.expand("~/code/tmux-worktree/nvim/twt.nvim"),
  config = function()
    require("twt").setup()
  end,
}
```

The plug-in needs Neovim 0.10 or later. Direct pane delivery also needs tmux.
Agent Session features need a `twt` executable in `PATH`. Review Note and
clipboard features do not need either executable. The plug-in uses core
`vim.ui.select`, so a picker provider is optional. LazyVim replaces that call
with the Snacks picker. The Agent Session picker and the notes list pass
Snacks options that show a preview.

## Main mappings

| Mapping | Action |
| --- | --- |
| `<leader>arp` | Select an Agent Session and open its Agent Preview |
| `<leader>an` | Add a review note, or open the note on this line |
| `<leader>arn` | Add a review note, or open the note on this line |
| `<leader>ad` | Delete the review note on this line |
| `<leader>al` | List the Review Notes in this Neovim session |
| `<leader>arl` | List the Review Notes in this Neovim session |
| `<leader>arr` | Deliver the Review Batch to a pane in the current tmux session or to the clipboard |
| `<leader>ara` | Send the Review Batch to an Agent Session in the current Workspace |
| `<leader>ary` | Yank the Review Batch to the clipboard |
| `<leader>art` | Send the Review Batch to a tmux pane |
| `<leader>ars` | Write free text in a window and send it |
| `<leader>aru` | Resume the selected Agent Session |
| `<leader>arf` | Focus the selected Agent Session |
| `<leader>arR` | Write a new transcript snapshot for the selected Agent Session |
| `<leader>arx` | Clear review notes |

The note window is a boxed float in the current window. It lines up with
the start of the selected line and stays to the right of the line numbers.
It is narrower than the pane. The fill uses `Pmenu`, and the border and
title use `FloatTitle`, so the box stays readable on a dark editor
background. Override `TwtFloat`, `TwtFloatBorder`, `TwtFloatTitle`, or
`TwtFloatFooter` to pick other colorscheme groups. It sits
below the selected line or visual block when that block is high in the
viewport, and above it when the block is low. The parent window scrolls when
both would not fit. The note window and the message window use the same keys.
The footer shows `C-s save · q quit` on the right. An existing note also
shows `C-d delete`. Press `<C-s>` to accept the text. Press `<C-d>` to
delete the open note. Clear the comment and press `<C-s>` to delete it.
Press `q` to close the window and to keep the note.

Set `default_keymaps = false` to remove these mappings. The commands stay
available:

| Command | Action |
| --- | --- |
| `:TwtAgents` | Select an Agent Session and open its Agent Preview |
| `:TwtNote` | Add a review note, or open the note on this line |
| `:TwtNoteDelete` | Delete the review note on this line |
| `:TwtReview` | Deliver the Review Batch to a tmux pane or the clipboard |
| `:TwtReviewAgent` | Send the Review Batch to an Agent Session in the current Workspace |
| `:TwtReviewCopy` | Copy the Review Batch to the clipboard |
| `:TwtReviewPane` | Send the Review Batch to a tmux pane |
| `:TwtSend` | Write free text in a window and send it |
| `:TwtNotes` | List the Review Notes in this Neovim session |
| `:TwtResume` | Resume the selected Agent Session |
| `:TwtFocus` | Focus the selected Agent Session |
| `:TwtRefresh` | Write a new transcript snapshot for the selected Agent Session |
| `:TwtClear` | Clear review notes |

Each mapping has a command. The commands and the mappings do the same work and
show the same messages.

`<leader>arp` identifies each Agent Session with its label, provider when it
adds information, shortest unique ID prefix, status, and last activity time.
The Snacks picker shows the Agent Preview for the highlighted row. The preview
is a verified transcript or the bounded visible screen of a verified live
pane. It opens the picker before it reads preview text, reads only the latest
requested row, and runs at most one preview read at a time. It keeps a bounded cache
only while that picker is open. A picker without Snacks still shows the full
row identity and can select the Agent Session.

Preview is read-only. It does not register a discovered Agent Session and does
not write a Transcript Snapshot. A transcript row writes and opens its private
Transcript Snapshot. A live-pane row adopts and selects the Agent Session, then
opens the Agent Preview in a scratch buffer. It does not write a Transcript
Snapshot.

`:TwtNotes`, `<leader>al`, and `<leader>arl` show all Review Notes in the
current Neovim session. The Snacks picker preview shows the file, the selected
lines, and the note comment. Select a note, then select `Open`, `Delete`, or
`Go to the line`. `Open` moves to the line and opens the note window with the
current comment.

`<leader>an` and `<leader>arn` on a line that already has a note open that
note. Save updates the comment. Press `<C-d>` in that window to delete the
note. Clear the comment and press `<C-s>` to delete it. `<leader>ad` deletes
the note on this line without opening the window. A line with more than one
note asks which note to open or delete. `<leader>arx` asks `Are you sure you want to
clear all review notes?` before it clears the session batch.

`<leader>arr` uses no `twt` command. In tmux, it lists the live panes in the
current tmux session, except the current pane. It also adds Clipboard as a
destination. Each pane label shows the session and window, pane ID, current
command, and current path. It does not list panes from other tmux sessions.
Outside tmux, it copies the batch immediately. `<leader>art` always asks for a
tmux pane. `<leader>ary` always yanks to the clipboard. A canceled picker keeps
the batch and shows no success message.

Direct pane delivery loads the Review Batch through standard input, requests
a bracketed paste, and submits it with Enter. A confirmed pane or Agent send
clears only the unchanged notes that it sent. Notes added or edited during the
send stay in the batch. A clipboard copy always keeps the notes. A failed or
uncertain send also keeps them. The plug-in does not retry a send.

Select pane targets carefully. If the selected program does not support
bracketed paste, pasted line breaks can become input. A shell pane can run that
input. See [the security guide](../../docs/security.md#direct-neovim-pane-delivery).

`<leader>ara` keeps a selected Agent Session when it can send or resume. If
there is no valid selection, the plug-in uses the only live Agent Session that
can receive feedback. If there is more than one, it shows a Workspace-only
send picker. This picker does not open an Agent Preview or write a Transcript
Snapshot. A canceled picker keeps the Review Batch.

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

Agent selection and transcript snapshots are scoped by immutable Workspace ID.
Review Notes are scoped only to the current Neovim session. Each formatted note
uses the absolute file path. Extmarks keep its line range current after file
edits, and a renamed buffer uses its new path. An unloaded or invalid buffer
stops formatting with an error, so the plug-in does not send a stale location.

`twt` reads the linked provider transcript, checks its Workspace, and writes
the private Markdown snapshot to
`$TWT_STATE_DIR/snapshots/projects/WORKSPACE_ID/agents/AGENT_ID.md`. The
plug-in opens the exact file that the `path` field of the snapshot response
names, so each Agent Session keeps its own file. If an older `twt` returns no
`path`, the plug-in falls back to the shared
`$TWT_STATE_DIR/snapshots/projects/WORKSPACE_ID/latest.md` file. Archive keeps
the files. Applied Workspace removal deletes them. Register a new Agent Session
with `--session SESSION_ID`, or use `twt agents transcript link` for an
existing record.
Transcript loading supports Codex, Claude, and Grok. Cursor transcript loading
stays off because its local records do not contain a safe, exact Workspace
directory. The picker can still preview, select, focus, and send to a verified
live Cursor Agent pane. Confirm opens that Agent Preview in a scratch buffer.

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
twt.review.prompt_delete(done)
twt.review.prompt_notes(done)
twt.review.deliver(done)
twt.review.copy(done)
twt.review.send_pane(done)
twt.review.send(done)
twt.review.list()
twt.review.delete(note_id)
twt.review.update(note_id, comment)
twt.review.jump(note_id)
twt.review.clear()
```

Each `done` is error-first: `done(err)` for a failure, or `done(nil, result)`
for a success. `done` is optional. These modules show no message, so the caller
decides what the user sees.

`pick` gives `done(nil, result)`. A transcript result has `agent` and `path`.
A live-pane result has `agent` and `message`, but no `path`. Confirm opens the
Agent Preview in a scratch buffer. `refresh` requires a transcript and gives
`agent` and `path`. A canceled picker gives `done(nil)` with no result. A
canceled text window gives no answer, so `prompt_add` and `prompt_send` show
nothing after `q`.

`review.deliver` is the `twt`-independent route. `review.send` sends through
`twt`. It selects a live send target when the Workspace has no valid prior
selection.

`setup` accepts a `confirm` function for the resume question and for clearing
review notes. It also accepts clipboard and tmux adapters for custom setups or
tests. `agent_preview_max_bytes` limits one displayed transcript preview, and
`agent_preview_cache_bytes` limits the picker-local preview cache:

```lua
require("twt").setup({
  clipboard = function(text) vim.fn.setreg("+", text, "v") end,
  confirm = function(question, done)
    done(vim.fn.confirm(question, "&Yes\n&No", 2) == 1)
  end,
  tmux_command = "tmux",
  agent_preview_max_bytes = 262144,
  agent_preview_cache_bytes = 524288,
})
```

Run all headless tests. They include a standalone real-tmux review flow and a
real `twt` flow with two separate Workspaces:

```sh
./nvim/twt.nvim/tests/test.sh
```
