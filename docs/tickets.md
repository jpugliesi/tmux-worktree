# twt2 tickets

Design for a personal Markdown ticket tracker in `twt2`. The files are the
store. The CLI owns every mutation. Agents call `twt2 tickets`. They do not
write ticket files by hand.

This document is the implementation spec. It is not yet shipped.

## Why this exists

A person needs a backlog of future work for coding agents. That backlog must
live as Obsidian notes. It must also have agent-first CLI DX: JSON output,
dry-run, schema introspection, and no `$EDITOR` in a pipe.

The tracker is local. It is not Linear, GitHub Issues, or Origin issues.

## Language

Keep the existing twt2 **Project**. Do not rename it to workspace in this
slice. A Project is a live work environment. A ticket folder is a durable
backlog group. Those objects have different lifetimes. One Board can feed
many Projects over time.

| Term | Meaning | Avoid |
|---|---|---|
| **Tickets home** | Configured root directory of ticket Markdown files. Default personal value: `~/Vaults/spacexai/tickets/`. | Vault, issues dir |
| **Board** | One directory under Tickets home, with `index.md`. Groups tickets. Outlives any checkout. | Project, workspace, epic folder |
| **Ticket** | One Markdown file with YAML frontmatter. | Issue, task file, combined tickets note |
| **Topic note** | An Obsidian wiki-link to a knowledge note, such as `[[Change Monitor Agent]]`. Not a Board. | Project |

Do not put `project:` in ticket frontmatter. Use `board:` for the folder.
Link Topic notes in the ticket body.

Do not merge Ticket objects with Project objects in v1. A Ticket does not
require a live Project. You can file work before any worktree exists.

## File layout

Tickets home is a configured directory. There is no sidecar database.

```
$TICKETS_HOME/
  index.md                 # vault hub, scaffold once
  templates/ticket.md      # create template, scaffold once
  some-ticket.md           # ungrouped ticket
  change-monitor/
    index.md               # board hub, scaffold once
    some-ticket.md
```

Rules:

- One file per Ticket. Never a combined tickets file.
- Filename is a kebab slug from the title, for example
  `reconnect-change-monitor-vfs-tools.md`.
- A slug is unique across the whole Tickets home, so `[[some-ticket]]` is
  unambiguous in Obsidian.
- A Board is one directory segment. No nested Boards in v1.
- `index.md` and files under `templates/` are not Tickets.
- Closed Tickets stay in place so wiki-links stay stable.
- Existing files such as `tkt-cm-001.md` stay valid. The resolver accepts any
  Markdown stem.

`twt2 tickets init` creates Tickets home if it is missing. It writes
`index.md` and `templates/ticket.md` only when those files are missing. It
does not overwrite notes.

`twt2 tickets boards create NAME` creates the Board directory and writes
`index.md` only when that file is missing.

Do not rewrite Bases queries on each ticket write. Obsidian Bases owns the
view. The CLI scaffolds the hub once.

### Root `index.md` views

Recent, Ready, Blocked, Claimed. Filter to Markdown files under Tickets home.
Exclude `index.md` and `templates/`.

### Board `index.md` views

The same views, filtered with `file.folder == this.file.folder`.

## Ticket file format

The file is an Obsidian note. YAML frontmatter first. Then an H1 that matches
`title`.

### Frontmatter (v1)

```yaml
---
title: "Reconnect Change Monitor VFS tools"
aliases:
  - Reconnect Change Monitor VFS tools
tags:
  - tickets
status: needs-triage
priority: 2
board: change-monitor
blocked_by: []
claimed_by:
claimed_at:
created: 2026-08-20
updated: 2026-08-20
---
```

Ungrouped tickets omit `board` or leave it empty.

`blocked_by` holds wiki-links, for example `["[[some-ticket]]"]`. Body links
use the same form. Do not write a bare slug in prose.

`created` and `updated` use `YYYY-MM-DD`. `priority` is `0` (highest) to `4`
(lowest). Default `2`.

Statuses:

- `needs-triage`
- `needs-info`
- `ready-for-agent`
- `ready-for-human`
- `wontfix`
- `done` (shipped work. Extra value. Not a triage role.)

Not in v1: `parent`, `type`, `category`, `twt2ProjectId`, sequential IDs
such as `tkt-cm-001`.

### Body

```markdown
# Title

## What to build

...

## Acceptance criteria

- [ ] ...

## Blocked by

None - can start immediately

## Comments
```

Omit **Parent** unless a later slice adds `parent`. Write **Blocked by** as
`None - can start immediately` when the ticket is unblocked. Append comments
under `## Comments`.

## CLI contract

Match existing twt2 Agent DX in [agent-dx.md](agent-dx.md):

- `--output json` on every command
- `--dry-run` on every mutation
- live `twt2 schema` for the new commands
- `--limit` on every list, with `totalCount` and `truncated`
- named JSON envelopes
- `clierr` codes and the same exit map (0 success, 1 internal, 2 invalid
  usage, 3 failed precondition)
- `twt2 apply --stdin` for typed payloads

Never open `$EDITOR` for an agent. The editor path is TTY-only.

### Commands

```
twt2 tickets init
twt2 tickets create [DESCRIPTION] [--board BOARD] [--title TITLE] [--slug SLUG] [--status STATUS] [--stdin]
twt2 tickets list [--board BOARD] [--status STATUS] [--ready] [--limit N]
twt2 tickets show TICKET
twt2 tickets edit TICKET [--stdin]
twt2 tickets set TICKET [--status STATUS] [--priority N] [--board BOARD]
twt2 tickets claim TICKET
twt2 tickets unclaim TICKET
twt2 tickets comment TICKET --stdin
twt2 tickets boards create NAME
twt2 tickets boards list [--limit N]
twt2 tickets boards show NAME
```

Register the group under Workflows in `internal/cli/root.go`. Declare
`setArguments` for every placeholder. Complete Board names and Ticket slugs
from the store.

`TICKET` resolves in this order:

1. Exact slug
2. Unique prefix
3. `title`
4. `aliases`
5. Wiki-link `[[…]]`
6. Path under Tickets home

An ambiguous prefix is `invalid_usage`. Put the candidate slugs in `hint`.

### `tickets create`

| Input | Behavior |
|---|---|
| No args, stdout is a TTY, stdin is a TTY | Open `$VISUAL` or `$EDITOR` on a temp copy of `templates/ticket.md`. Parse the saved file. An empty save is `invalid_usage`. |
| No args, not a TTY | Exit 2. Hint: pass DESCRIPTION, `--title`, or `--stdin`. |
| DESCRIPTION args | Join as the body. Derive `title` from the first line if `--title` is absent. Derive the slug from the title. |
| `--stdin` | Read the body from stdin. Require `--title`. |

Default status is `needs-triage`. `--status ready-for-agent` is allowed.
`--dry-run` prints the file that would be written and does not write it.

Slug rule: lowercase, hyphenate, strip characters that are not ASCII letters
or digits, cap at 60 characters. If the slug exists anywhere under Tickets
home, return `already_exists` and hint `--slug`.

If `--board` names a missing Board, return `not_found` and hint
`twt2 tickets boards create NAME`. Do not create a Board as a side effect of
ticket create.

### `tickets list --ready`

`--ready` is the pickable work queue. It is not a synonym of
`--status ready-for-agent`.

A Ticket matches `--ready` when all of these hold:

- `status` is `ready-for-agent`
- `claimed_by` is empty
- every `blocked_by` target has `status` `done` or `wontfix`

Sort by `priority` ascending, then slug.

`--status` is a raw status filter. It can still return blocked or claimed
tickets. If both `--ready` and `--status` are set, exit 2 with a hint to use
only one.

List results omit the body. `show` returns metadata plus body.

### `tickets claim`

Claim is the first write of a work session. Compare-and-set:

- Empty `claimed_by` → set `claimed_by` and `claimed_at` (`YYYY-MM-DD`)
- Same claimant → success, no change
- Different claimant → `locked`. Hint names the current claimant.

Take the lock in `$TWT2_STATE_DIR`. Then write the Markdown file with
`store.WriteFileAtomic`. Do not leave lock files in Tickets home. Temp write
files live next to the destination and must not remain after success.

`unclaim` clears `claimed_by` and `claimed_at`.

Resolve shipped work with `twt2 tickets set TICKET --status done` and
`unclaim`.

### `tickets comment`

Require `--stdin`. Append under `## Comments`. Create that heading if it is
missing. Set `updated`.

### `apply` operations

Add:

- `tickets.create`
- `tickets.set`
- `tickets.claim`
- `tickets.unclaim`
- `tickets.comment`
- `tickets.boards.create`

`twt2 schema` must list every new command and these operations. Update
`TestSchemaDescribesCommandsFlagsAndRawApplyOperations`. That test currently
asserts exactly six apply operations.

### JSON envelopes

```json
{"schemaVersion":1,"ticket":{...}}
{"schemaVersion":1,"tickets":[...],"totalCount":0,"truncated":false}
{"schemaVersion":1,"board":{...}}
{"schemaVersion":1,"boards":[...],"totalCount":0,"truncated":false}
```

A ticket object includes `slug`, `title`, `status`, `priority`, `board`,
`path`, `claimedBy`, `blockedBy`, `created`, `updated`. `show` also includes
`body`.

Mutation dry-run uses the existing mutation envelope: `schemaVersion`,
`operation`, `status` (`valid` or `applied`), plus `id` as the slug.

Errors stay the current shape: `code`, `message`, `hint`, `helpCommand`.

## Config

Path: `$TWT2_CONFIG_DIR/config.yaml` (default `~/.config/twt2/config.yaml`)

```yaml
ticketsHome: /Users/john.pugliesi/Vaults/spacexai/tickets
```

`TWT2_TICKETS_HOME` overrides the file. YAML decoding rejects unknown fields
and more than one document, matching Project Template loading.

`twt2 doctor` reports whether Tickets home is set, exists, and is writable.

Tests inject a temp Tickets home through `cli.Options`. They do not touch the
personal vault.

## Skill

The CLI schema is the source of truth. The skill does not paste the
frontmatter schema.

Add a tickets section to `skills/twt2/SKILL.md`. Install that skill into the
three user skill trees so Cursor, Claude Code, and Codex all see it:

- `~/.cursor/skills/twt2/SKILL.md`
- `~/.claude/skills/twt2/SKILL.md`
- `~/.agents/skills/twt2/SKILL.md`

Keep one canonical file in this repo. The install step copies or symlinks it.

Skill rules:

1. Run `twt2 schema` when the installed version is not known.
2. Use `twt2 tickets` for every ticket read and write.
3. Pass `--output json` on every command.
4. Pass `--dry-run` before every mutation.
5. Pass `--limit` on list commands.
6. Create with a DESCRIPTION or `--stdin`. Do not rely on `$EDITOR`.
7. Claim before work. Resolve with `set --status done` and `unclaim`.
8. Link tickets with `[[slug]]`.
9. List pickable work with `twt2 tickets list --ready --output json`.

Description trigger (keep third person):

> Manage personal Markdown tickets through `twt2 tickets`. Use when creating,
> listing, claiming, or updating tickets, boards, or a tickets home in an
> Obsidian vault.

This workflow is the tracker. Do not create Linear, GitHub, or Origin issues
for this user's tickets unless the user asks.

Leave vault `docs/agents/issue-tracker.md` unchanged in this slice. A
follow-up rewrites that file so publish and fetch go through `twt2 tickets`.

## Later work (not this slice)

- `twt2 tickets claim TICKET --project current` stamps `twt2ProjectId`
- `twt2 context` lists `--ready` tickets
- `twt2 projects create` takes `--ticket TICKET` and copies the title
- Nested Boards
- Field masks on `tickets show`

## Implementation

Work in this repository. Follow existing packages, tests, and
[agent-dx.md](agent-dx.md).

1. Add Board, Ticket, and Tickets home to `CONTEXT.md`. Avoid calling a Board
   a Project. Avoid calling a Project a workspace in new text.
2. Add `internal/domain/ticket.go` with status constants and structs.
3. Load `config.yaml` plus `TWT2_TICKETS_HOME`.
4. Add `internal/ticket/service.go`. Walk Tickets home. Parse YAML
   frontmatter. Create Boards and Tickets. Claim and comment. Atomic writes
   via `store.WriteFileAtomic`. State-dir lock for claim.
5. Add `internal/cli/tickets.go`. Wire dry-run and JSON like `projects`.
6. Extend `applyRequest` and `applyOperations()`.
7. Embed the `index.md` Bases block and `templates/ticket.md`.
8. Isolate `$EDITOR` behind `openEditor`. Tests inject a fake editor. The
   default runs only when both stdin and stdout are terminals.
9. Add the tickets section to `skills/twt2/SKILL.md` and document the
   three-path user install.
10. Tests listed below.

Do not edit everysphere. Do not rename `projects` commands. Do not add MCP.

### Tests

- Create writes one Obsidian-valid note
- Duplicate slug returns `already_exists`
- Missing Board returns `not_found`
- Create with no args in a non-TTY returns `invalid_usage`
- Claim by a second claimant returns `locked`
- `--ready` omits blocked, claimed, and non-ready statuses
- `--ready` with `--status` returns `invalid_usage`
- Wiki-link, prefix, and title resolve
- `init` does not overwrite existing notes
- JSON envelopes match the shapes above
- `--dry-run` writes nothing
- Schema lists the new commands and apply operations

### Success criteria

- `twt2 tickets create "fix the vfs tools" --board change-monitor --output json`
  writes one Obsidian-valid note under the configured home.
- `twt2 tickets create` with no args in a terminal opens the editor. The same
  command in a pipe exits 2 with a hint.
- `twt2 tickets list --ready --output json` returns only unblocked,
  unclaimed, `ready-for-agent` tickets.
- `twt2 schema` includes the new commands and apply operations.
- A second `claim` by a different claimant returns `locked`.
- Obsidian can open the new note and resolve `[[slug]]`.
- `git status` in everysphere is unchanged.
