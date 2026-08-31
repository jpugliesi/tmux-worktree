# twt tickets

Design for a personal Markdown ticket tracker in `twt`. The files are the
store. The CLI owns every mutation. Agents call `twt tickets`. They do not
write ticket files by hand.

This document is the implementation specification for the current feature.

## Why this exists

A person needs a backlog of future work for coding agents. That backlog must
live as Obsidian notes. It must also have agent-first CLI DX: JSON output,
dry-run, schema introspection, and no `$EDITOR` in a pipe.

The tracker is local. It is not Linear, GitHub Issues, or Origin issues.

## Language

A **Workspace** is a temporary live work environment. A **Project** is a
durable backlog group. These objects have different lifetimes. One Project
can feed many Workspaces over time.

| Term | Meaning | Avoid |
|---|---|---|
| **Tickets home** | Configured root directory of ticket Markdown files. Default personal value: `~/Vaults/spacexai/tickets/`. | Vault, issues dir |
| **Project** | One directory under Tickets home, with `index.md`. Groups tickets. Outlives any checkout. | Board, workspace, epic folder |
| **Ticket** | One Markdown file with YAML frontmatter. | Issue, task file, combined tickets note |
| **Topic note** | An Obsidian wiki-link to a knowledge note, such as `[[Change Monitor Agent]]`. Not a Project. | Board |

Do not put `workspace:` in ticket frontmatter. Use `project:` for the folder.
Link Topic notes in the ticket body.

Do not merge Ticket objects with Workspace objects in v1. A Ticket does not
require a live Workspace. You can file work before any worktree exists.

## File layout

Tickets home is a configured directory. There is no sidecar database.

```
$TICKETS_HOME/
  index.md                 # vault hub, scaffold once
  templates/ticket.md      # create template, scaffold once
  some-ticket.md           # ungrouped ticket
  change-monitor/
    index.md               # project hub, scaffold once
    some-ticket.md
  closed/
    .twt-closed            # marks the twt-owned closed tree
    old-ticket.md          # closed, ungrouped ticket
    change-monitor/
      old-project-ticket.md
```

Rules:

- One file per Ticket. Never a combined tickets file.
- Filename is a kebab slug from the title, for example
  `reconnect-change-monitor-vfs-tools.md`.
- A slug is unique across the whole Tickets home, so `[[some-ticket]]` is
  unambiguous in Obsidian.
- A Project is one directory segment. In `closed/PROJECT/`, the second
  directory segment defines the Project. No nested Projects exist in v1.
- `index.md` and files under `templates/` are not Tickets.
- `done` and `wontfix` Tickets are closed. They live under `closed/`, which
  is a reserved Project name.
- Bare slugs and `[[slug]]` wiki-links stay stable after a move. Old path
  references do not.
- Existing files such as `tkt-cm-001.md` stay valid. The resolver accepts any
  Markdown stem.

`twt tickets init` creates Tickets home if it is missing. It writes
`index.md` and `templates/ticket.md` only when they are missing. It creates
the marked closed tree when no `closed` path exists. It does not adopt an
unmarked `closed` directory or overwrite notes.

`twt projects create NAME` creates the Project directory and writes
`index.md` only when that file is missing. Project ticket counts include
active and closed Tickets. With no NAME in an interactive terminal, twt
asks for a Project name, then opens `$VISUAL` or `$EDITOR` on an empty file
for the plan. After the plan save, twt shows a Workspace Template picker
when `--template` is absent and more than one Template exists. It then asks
whether to start a Workspace. A pipe or `--output json` requires NAME.

`twt projects close NAME` sets `twt_closed: true` in the Project `index.md`.
The Project keeps its directory, `index.md`, `plan.md`, and Ticket history.
Default Project lists and completion omit a closed Project. New work cannot
use a closed Project.

`twt projects remove NAME` prints a removal plan. `--apply` deletes the
Project directory, `plan.md`, and every Ticket file for that Project,
including `closed/NAME/`. After apply, `twt projects create NAME` succeeds.
A Workspace that still names the Project is a blocker. Close keeps history.
Remove deletes the Project.

When no open Tickets remain, close needs no confirmation. At a text terminal,
twt asks before it changes open Tickets to `wontfix`. A script must pass
`--force`. Close also clears each affected claim and Workspace link.
Close does not stop Workspaces or agents.

Do not rewrite Bases queries on each ticket write. Obsidian Bases owns the
view. The CLI scaffolds the hub once.

### Root `index.md` views

Recent, Ready, Blocked, Claimed. Filter to Markdown files under Tickets home.
Exclude `index.md`, `templates/`, and `closed/`. The new scaffold includes
the `closed/` filter. An existing personalized index is not rewritten; add
the same filter to it manually.

### Project `index.md` views

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
project: change-monitor
blocked_by: []
claimed_by:
claimed_at:
pull_requests: []
twt_workspace_id:
created: 2026-08-20
updated: 2026-08-20
---
```

`pull_requests` holds the HTTPS pull request URLs that shipped the Ticket's
work. `twt tickets complete` and `twt tickets pr add` write it; do not edit
it by hand.

Ungrouped tickets omit `project` or leave it empty.

`blocked_by` holds wiki-links, for example `["[[some-ticket]]"]`. Body links
use the same form. Do not write a bare slug in prose.

`created` and `updated` use `YYYY-MM-DD`. `priority` is `0` (highest) to `4`
(lowest). Default `2`.

Statuses:

- `needs-triage`
- `needs-info`
- `ready-for-agent`
- `ready-for-human`
- `wontfix` (closed)
- `done` (closed shipped work. Extra value. Not a triage role.)

`twt_workspace_id` is the immutable Workspace ID of the active Workspace
that works on the Ticket. Create with `--ticket` and `tickets start` stamp
it. `close` and `unclaim` clear it. `claim --workspace` stamps it.

Not in v1: `parent`, `type`, `category`, sequential IDs such as
`tkt-cm-001`.

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

Match existing twt Agent DX in [agent-dx.md](agent-dx.md):

- `--output json` on every command
- `--dry-run` on every mutation
- live `twt schema` for the new commands
- `--limit` on every list, with `totalCount` and `truncated`
- named JSON envelopes
- `clierr` codes and the same exit map (0 success, 1 internal, 2 invalid
  usage, 3 failed precondition)
- `twt apply -` for typed payloads

Never open `$EDITOR` for an agent. The editor path is TTY-only.

### Commands

```
twt tickets init
twt tickets home
twt tickets create [DESCRIPTION] [--project PROJECT] [--title TITLE] [--slug SLUG] [--status STATUS] [--blocked-by SLUG]
twt tickets list [--project PROJECT] [--all-projects] [--status STATUS] [--ready] [--limit N]
twt tickets queue [--project PROJECT] [--limit N]
twt tickets dispatch TICKET [--plan] [--max-concurrency N]
twt tickets sync --project PROJECT
twt tickets abandon SESSION --force
twt tickets complete TICKET [--as NAME] [--status STATUS] [--pr URL]...
twt tickets get TICKET
twt tickets set TICKET [--status STATUS] [--priority N] [--project PROJECT] [--blocked-by SLUG]
twt tickets claim TICKET [--as NAME]
twt tickets unclaim TICKET [--as NAME]
twt tickets close TICKET [--as NAME]
twt tickets comment TICKET -
twt tickets doctor
twt tickets repair
twt projects create [NAME]
twt projects close NAME [--force]
twt projects remove NAME [--apply]
twt projects set NAME --template TEMPLATE
twt projects list [--limit N]
twt projects get NAME
```

Register the group under Workflows in `internal/cli/root.go`. Declare
`setArguments` for every placeholder. Complete Project names and Ticket slugs
from the store.

`twt tickets home` opens the Tickets home directory in `$VISUAL` or
`$EDITOR`. It is TTY-only and has no apply operation.

`TICKET` resolves in this order:

1. Exact slug
2. Unique prefix
3. `title`
4. `aliases`
5. Wiki-link `[[…]]`
6. Path under Tickets home

An ambiguous prefix is `invalid_usage`. Put the candidate slugs in `hint`.
Path lookup uses the current path. An old path does not follow a moved
Ticket; use its bare slug or `[[slug]]` instead.

### Project queue

`twt tickets queue --project PROJECT` reads one Ticket index snapshot. It
returns the complete open Project graph and a `ready` list. Both lists sort by
priority and then by Ticket slug.

Each graph dependency reports whether its Ticket is open, closed, missing, or
invalid. It also reports the dependency Project. `cycles` lists dependency
cycles that stop affected Tickets from becoming ready.

`--limit` cuts only `ready`. The complete graph and cycle diagnostics stay in
the result. Use `--fields ready,readyTotalCount` when an agent does not need
the graph.

### Dispatch coordinator

A Project can select one Workspace Template. The Template can include a
`local_dispatch` block (provider, effort, instructions, maximum Project
concurrency default 2, optional `stacking`) for implementation Workspaces.
The generic effort is `small`, `medium`, `large`, or `xlarge`; its default is
`large`. Empty `local_dispatch` fields fall back to the machine `ticketAgent`
config, so a shared Template normally leaves the provider unset and each
machine uses an installed provider. A dispatch Session defers to its
Workspace's Template snapshot.

`twt tickets dispatch TICKET` claims the Ticket, creates a Workspace, and
starts one autonomous implementation agent in tmux; a person can attach and
steer at any time.

One coordinator wave first syncs existing Sessions, reads the available
capacity, and then dispatches that many ready Tickets:

```sh
twt tickets sync --project change-monitor --dry-run --output json
twt tickets sync --project change-monitor --output json
twt tickets queue --project change-monitor --limit AVAILABLE --output json
twt tickets dispatch canonical-pr-comment --dry-run --output json
twt tickets dispatch canonical-pr-comment --output json
```

The sync result groups `capacity`, `sessions`, and `diagnostics` under
`backends.local`. If `capacity.known` is false, do not dispatch. Read the
sync diagnostics. If it is true, dispatch ready Tickets up to
`capacity.available`.

The Project dispatch lock makes the capacity reservation atomic. Agent mode
asks the agent to implement, test, and create pull requests for changed
repositories. `--plan` asks for a plan only.

An implementation agent reports its own completion:
`twt tickets complete TICKET --as CLAIMANT --pr URL` records the pull
requests and sets `ready-for-human` in one write. The sync then marks the
Session finished. A stopped agent whose Ticket stays claimed becomes a
`stuck` diagnostic; sync never releases a claim by itself.

If repeated syncs cannot recover one Session, `tickets abandon SESSION
--force` stops its recovery. It releases the Ticket only when the saved
claimant still owns it. It never stops the agent: the Workspace keeps
running until `twt done WORKSPACE`. Run `--dry-run` before you apply it.

### Syncing the Tickets home between machines

Set `ticketsSync` in `~/.config/twt/config.yaml` (or `TWT_TICKETS_SYNC` and
`TWT_TICKETS_SYNC_REMOTE`) when two machines share one Tickets home through a
git remote:

```yaml
ticketsSync:
  mode: git      # off (default) or git
  remote: origin # optional
```

The Tickets home must live inside a clone of the shared repository. Every twt
write then runs one sync round: commit stray manual edits, pull, apply the
write, commit only the Tickets home path, push. Claim-class writes (`claim`,
`start`, `dispatch`, `complete`, `unclaim`, `close`) require the push: it is
the cross-machine compare-and-swap. A rejected push pulls and replays the
write against the fresh state, so a lost race returns the normal `locked`
error, and an unreachable remote returns `precondition_failed`. Other writes
commit locally and push best-effort with a warning. Reads never touch git.

`twt tickets sync` always runs one explicit store round first: it commits manual edits,
pulls, rebases local commits, and pushes everything the remote lacks. Run it
after offline work, or when `twt tickets doctor` reports `sync_unpushed`,
`sync_dirty`, or a diverged tree. `twt tickets init` writes a `.gitignore`
that keeps atomic-write temp files out of every commit.

### `tickets create`

| Input | Behavior |
|---|---|
| No args, stdout is a TTY, stdin is a TTY | Ask `Title: `. Then pick a Project with fzf when `fzf` is installed, or a numbered list. The first row is `(none)` for an ungrouped Ticket. A typed name that is not a Project asks `Project "NAME" does not exist. Create it? [Y/n]`. Enter means yes. Then open `$VISUAL` or `$EDITOR` on an empty file for the description. The CLI writes YAML frontmatter. An empty title or an empty save is `invalid_usage`. `--title` skips the title prompt. `--project` skips the picker. |
| No args, not a TTY | Exit 2. Hint: pass DESCRIPTION, `--title`, or `-`. |
| DESCRIPTION args | Join as the body. Derive `title` from the first line if `--title` is absent. Derive the slug from the title. Never open the wizard, even on a TTY. |
| `-` | Read the body from stdin. Require `--title`. Never open the wizard. |

Default status is `needs-triage`. `--status ready-for-agent` is allowed.
`--blocked-by` writes `blocked_by` as wiki-links. Repeat the flag. Each
value may be a slug or `[[slug]]`. An empty list is the default. A Ticket
cannot list itself. A missing blocker stays allowed. `--ready` treats a
missing blocker as open.

`--dry-run` prints the file that would be written and does not write it.
When the wizard would create a Project, text dry-run prints that Project first,
then the Ticket file. JSON dry-run stays one `tickets.create` envelope.
Create puts an initial `done` or `wontfix` Ticket in the closed tree.

Slug rule: lowercase, hyphenate, strip characters that are not ASCII letters
or digits, cap at 60 characters. If the slug exists anywhere under Tickets
home, return `already_exists` and hint `--slug`.

If `--project` names a missing Project, return `not_found` and hint
`twt projects create NAME`. `--project` never creates a Project. The
interactive picker may create a Project after confirm. Apply `tickets.create`
still requires an existing Project.

### `tickets set`

`set` changes `status`, `priority`, `project`, or `blocked_by`. Pass at
least one flag.

`--blocked-by` replaces the whole blocker list. Pass an empty value to
write `[]`. Apply uses `ticket.blockedBy` as an array of strings. An empty
array clears the list.

### Ticket moves

Status and Project directory define the correct path. `create`, `close`,
and `set --status` or `set --project` move a Ticket when necessary. A
closed Project Ticket moves to `closed/PROJECT/SLUG.md`. A closed ungrouped
Ticket moves to `closed/SLUG.md`. Setting a closed Ticket to an open status
reopens it and moves it back to the active tree.

The directory remains the source of truth for Project. The `closed` segment
is storage, not a Project.

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

The list uses `--project`, then `TWT_PROJECT`, then the current Workspace
Project. With no Project in scope, the list includes every Project.
`--all-projects` lists every Project even when a Workspace Project is set.

A scoped text list is a simple table. A wide text table adds a `PROJECT`
column. JSON and NDJSON stay a flat array in the sort order above.

List results omit the body. `show` returns metadata plus body.

### Claimant identity

`claimed_by` is a short name string. Resolve it in this order:

1. `--as NAME`
2. `TWT_CLAIMANT`
3. The OS username (`user.Current().Username`, then `$USER`)

A TTY claim may use the OS username. A non-TTY claim, and every `apply`
claim or unclaim, must set `--as` or `TWT_CLAIMANT`. If both are missing,
exit 2. Hint: pass `--as NAME`. This stops two agents from both succeeding
as the same OS user under the "same claimant" branch.

`--as` and `TWT_CLAIMANT` use the same resource-name rules as other twt
IDs: no path separators, no `..`, no percent signs, no query characters, no
control characters.

Agents pass a unique `--as` value per session, for example
`codex-fix-auth` or the Agent Session ID. Do not add a `claimant:` config
field in v1.

### `tickets claim`

Claim is the first write of a work session. Compare-and-set on the resolved
claimant:

- Empty `claimed_by` → write the claimant into `claimed_by` and set
  `claimed_at` (`YYYY-MM-DD`)
- Same claimant → success, no change
- Different claimant → `locked`. Hint names the current claimant.

Take the lock in `$TWT_STATE_DIR`. Then write the Markdown file with
`store.WriteFileAtomic`. Do not leave lock files in Tickets home. Temp write
files live next to the destination and must not remain after success.

`unclaim` uses the same claimant resolution. It succeeds only when
`claimed_by` is empty or equals the resolved claimant. A different claimant
gets `locked`. It then clears `claimed_by` and `claimed_at`.

Resolve shipped work with `twt tickets close TICKET`. It sets `done` and
removes the claim.

### `tickets comment`

Require `-`. Append under `## Comments`. Create that heading if it is
missing. Set `updated`.

### `tickets doctor` and `tickets repair`

`twt tickets doctor` is read-only. It reports invalid files, duplicate slugs,
closed-tree conflicts, and Tickets outside their correct location.

Run repair as a dry-run first. Repair applies no move when the doctor report
has a blocker:

```sh
twt tickets doctor --output json
twt tickets repair --dry-run --output json
twt tickets repair --output json
```

### `apply` operations

Ticket apply operations:

- `tickets.init` (no payload)
- `tickets.create`
- `tickets.edit`
- `tickets.set`
- `tickets.claim`
- `tickets.unclaim`
- `tickets.close`
- `tickets.comment`
- `tickets.repair` (no payload)
- `projects.create`
- `projects.remove`

`twt schema` must list every new command and these operations. Update
`TestSchemaDescribesCommandsFlagsAndRawApplyOperations` with the new
operation.

`tickets.claim` and `tickets.unclaim` require `ticket.as`. Apply is never a
TTY path, so it has no OS-username default.

`tickets.create` and `tickets.set` accept `ticket.blockedBy` as an array of
slugs or wiki-links. An empty array on `tickets.set` clears the list.

### JSON envelopes

```json
{"schemaVersion":2,"ticket":{...}}
{"schemaVersion":2,"tickets":[...],"totalCount":0,"truncated":false}
{"schemaVersion":2,"project":{...}}
{"schemaVersion":2,"projects":[...],"totalCount":0,"truncated":false}
```

A ticket object includes `slug`, `title`, `status`, `priority`, `project`,
`path`, `claimedBy`, `blockedBy`, `workspaceId`, `created`, `updated`.
`workspaceId` is omitted when empty. `show` also includes `body`.

Mutation dry-run uses the existing mutation envelope: `schemaVersion`,
`operation`, `status` (`valid` or `applied`), plus `id` as the slug.

Errors stay the current shape: `code`, `message`, `hint`, `helpCommand`.

## Config

Path: `$TWT_CONFIG_DIR/config.yaml` (default `~/.config/twt/config.yaml`)

```yaml
ticketsHome: /Users/john.pugliesi/Vaults/spacexai/tickets
```

`TWT_TICKETS_HOME` overrides the file. YAML decoding rejects unknown fields
and more than one document, matching Workspace Template loading.

`twt config` shows the resolved Tickets home and its source. `twt doctor`
reports whether Tickets home is set, exists, and is writable.

Tests inject a temp Tickets home through `cli.Options`. They do not touch the
personal vault.

## Skill

The CLI schema is the source of truth. The skill does not paste the
frontmatter schema.

Add a tickets section to `skills/twt/SKILL.md`. Install that skill into the
three user skill trees so Cursor, Claude Code, and Codex all see it:

- `~/.cursor/skills/twt/SKILL.md`
- `~/.claude/skills/twt/SKILL.md`
- `~/.agents/skills/twt/SKILL.md`

Keep one source file in this repo. The install step copies or symlinks it.

Skill rules:

1. Run `twt schema` when the installed version is not known.
2. Use `twt tickets` for every ticket read and write.
3. Pass `--output json` on every command.
4. Pass `--dry-run` before every mutation.
5. Pass `--limit` on list commands.
6. Create with a DESCRIPTION or a lone `-` with `--title`. Do not rely on `$EDITOR`.
   `--project` does not create a Project.
7. Claim before work with `--as NAME`. Resolve with `set --status done`
   and `unclaim --as NAME`.
8. Link tickets with `[[slug]]`.
9. List pickable work with `twt tickets list --ready --output json`.

Description trigger (keep third person):

> Manage personal Markdown tickets through `twt tickets`. Use when creating,
> listing, claiming, or updating tickets, projects, or a tickets home in an
> Obsidian vault.

This workflow is the tracker. Do not create Linear, GitHub, or Origin issues
for this user's tickets unless the user asks.

Leave vault `docs/agents/issue-tracker.md` unchanged in this slice. A
follow-up rewrites that file so publish and fetch go through `twt tickets`.

## Later work (not this slice)

- Nested Projects
- Field masks on `tickets get`

## Implementation

Work in this repository. Follow existing packages, tests, and
[agent-dx.md](agent-dx.md).

1. Add Project, Ticket, and Tickets home to `CONTEXT.md`. Keep Project and
   Workspace as separate terms.
2. Add `internal/domain/ticket.go` with status constants and structs.
3. Load `config.yaml` plus `TWT_TICKETS_HOME`.
4. Add `internal/ticket/service.go`. Walk Tickets home. Parse YAML
   frontmatter. Create Projects and Tickets. Claim and comment. Atomic writes
   via `store.WriteFileAtomic`. State-dir lock for claim.
5. Add `internal/cli/tickets.go`. Wire dry-run and JSON like `workspaces`.
6. Extend `applyRequest` and `applyOperations()`.
7. Embed the `index.md` Bases block and `templates/ticket.md`.
8. Isolate `$EDITOR` behind `openEditor`. Tests inject a fake editor. The
   default runs only when both stdin and stdout are terminals.
9. Add the tickets section to `skills/twt/SKILL.md` and document the
   three-path user install.
10. Tests listed below.

Do not edit everysphere. Do not rename `workspaces` commands. Do not add MCP.

### Tests

- Create writes one Obsidian-valid note
- Duplicate slug returns `already_exists`
- Missing Project returns `not_found`
- Create with no args in a non-TTY returns `invalid_usage`
- Claim by a second claimant returns `locked`
- Non-TTY claim without `--as` or `TWT_CLAIMANT` returns `invalid_usage`
- TTY claim without `--as` writes the OS username into `claimed_by`
- `--ready` omits blocked, claimed, and non-ready statuses
- `--ready` with `--status` returns `invalid_usage`
- `create --blocked-by` and `set --blocked-by` write wiki-links
- apply `ticket.blockedBy` creates or replaces the same list
- Wiki-link, prefix, and title resolve
- `init` does not overwrite existing notes
- JSON envelopes match the shapes above
- `--dry-run` writes nothing
- Schema lists the new commands and apply operations

### Success criteria

- `twt tickets create "fix the vfs tools" --project change-monitor --output json`
  writes one Obsidian-valid note under the configured home.
- `twt tickets create` with no args in a terminal asks for a title and a
  Project, then opens the editor on an empty description. The same command in
  a pipe exits 2 with a hint.
- `twt projects create` with no args in a terminal asks for a Project name,
  opens the editor on an empty plan file, and asks whether to start a Workspace.
  The same command in a pipe exits 2 with a hint.
- `twt tickets list --ready --output json` returns only unblocked,
  unclaimed, `ready-for-agent` tickets.
- `twt schema` includes the new commands and apply operations.
- A second `claim` by a different claimant returns `locked`.
- Obsidian can open the new note and resolve `[[slug]]`.
- `git status` in everysphere is unchanged.
