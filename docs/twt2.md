# twt2 preview

`twt2` is the Go preview of the next tmux-worktree user experience. It uses a
Project as the unit of work. A Project can contain one or more Git worktrees,
one tmux session, one window for each repository, and a set of Agent Sessions.

The existing `twt` command continues to work. `twt2` uses separate config,
state, and data directories.

## Build

```sh
go build -o ./bin/twt2 ./cmd/twt2
```

To stamp the build with a version, set the version variable with `-ldflags`.
The `twt2 --version` and `twt2 schema` commands show this value:

```sh
go build -ldflags "-X github.com/jpugliesi/tmux-worktree/internal/version.Version=$(git describe --always)" -o ./bin/twt2 ./cmd/twt2
```

By default, `twt2` uses these directories:

- Config: `${XDG_CONFIG_HOME:-$HOME/.config}/twt2`
- State: `${XDG_STATE_HOME:-$HOME/.local/state}/twt2`
- Data: `${XDG_DATA_HOME:-$HOME/.local/share}/twt2`

You can set `TWT2_CONFIG_DIR`, `TWT2_STATE_DIR`, and `TWT2_DATA_DIR` to use
other directories. Tests can set `TWT2_TMUX_SOCKET` to use a separate tmux
server.

## Install shell completion

`twt2` completes command names, Project Template names, Project names, Agent
Session IDs, Prepared Environment IDs, and the values of `--template`,
`--project`, `--provider`, and `--output`:

```sh
twt2 completion zsh > "${fpath[1]}/_twt2"
```

Use `twt2 completion bash`, `twt2 completion fish`, or
`twt2 completion powershell` for the other shells.

## Create the Everysphere Project Template

Create an empty YAML file:

```sh
twt2 templates create everysphere
```

Add the repository clone policy:

```sh
twt2 templates repos add everysphere everysphere \
  https://origin.cursor.com/anysphere/everysphere.git \
  --depth 1 \
  --remote github=https://github.com/anysphere/everysphere.git \
  --default-branch main \
  --window-name everysphere
```

Set the repository initialization command. `twt2` runs this command in the
new worktree:

```sh
twt2 templates init set everysphere --repo everysphere -- ./init.sh
```

To remove one repository specification, run:

```sh
twt2 templates repos remove everysphere everysphere
```

You can also edit
`~/.config/twt2/templates/everysphere.yaml` directly:

```yaml
version: 1
name: everysphere
pool_depth: 1
repositories:
  - name: everysphere
    clone:
      url: https://origin.cursor.com/anysphere/everysphere.git
      depth: 1
    remotes:
      github: https://github.com/anysphere/everysphere.git
    default_branch: main
    window_name: everysphere
    initialize:
      command:
        - ./init.sh
```

`pool_depth` is the number of ready Prepared Environments that `twt2` keeps
for this Project Template. The default depth is 1.

Work with the YAML file through these commands:

```sh
twt2 templates path everysphere
twt2 templates edit everysphere
twt2 templates validate everysphere
twt2 templates list
twt2 templates show everysphere --output json
```

`templates path` prints one bare path. `templates edit` starts `$VISUAL` or
`$EDITOR` on the file, then validates the result. An invalid file stays on
disk, and `twt2` returns the `unsafe_state` error with the validation cause.

`templates validate` also reports warnings. A warning does not make the
command fail. A Project Template with no repositories gives one warning.

To make a Project Template from a file or from a pipe, use these flags. The
NAME argument stays required: it sets the template name, and a different
`name` field in the document is an error.

```sh
twt2 templates create everysphere --from-file ./everysphere.yaml
cat ./everysphere.yaml | twt2 templates create everysphere --from-stdin
```

Delete a Project Template with:

```sh
twt2 templates remove everysphere
```

Removal deletes only the YAML file. `twt2` refuses the removal while a
Project record still uses the Project Template. Remove those Projects first.

Prepare the next environment before you need it:

```sh
twt2 templates prepare everysphere
```

This command creates one worktree for each repository and runs repository
initialization once on each new physical worktree. A Project later claims the
complete Prepared Environment. After each claim, twt2 prepares one replacement
in the background, up to the pool depth.

Repository initialization runs before a Project name exists. It receives
`TWT2_ENVIRONMENT_ID`, `TWT2_ENVIRONMENT_ROOT`, `TWT2_TEMPLATE_NAME`,
`TWT2_REPOSITORY_NAME`, and `TWT2_REPOSITORY_PATH`. Use Project initialization
when setup needs a Project ID or name.

To run one command after all repository worktrees exist, set a Project
initialization command. You must set its working directory relative to the
Project root:

```sh
twt2 templates init set everysphere \
  --cwd everysphere \
  -- ./scripts/init-project.sh
```

`templates init set` sets one initialization command. With `--repo REPO` it
sets repository initialization, and `--cwd` is not valid. Without `--repo` it
sets Project initialization, and `--cwd PATH` is required.

Project initialization receives these environment variables:

- `TWT2_PROJECT_ID`
- `TWT2_PROJECT_NAME`
- `TWT2_PROJECT_ROOT`
- `TWT2_REPOSITORY_<NAME>` for each repository

## Lay out the tmux session

A Project Template can declare one session command. `twt2` runs it each time it
creates the tmux session of a Project: at Project creation, and again when
`twt2 projects open` makes the session for an archived Project. The command
runs after `twt2` makes the session and one window for each repository.

`twt2` never runs the command against a session that is already live. A setup
retry on a live session skips it, so the command cannot disturb panes that you
arranged.

```yaml
session:
  command:
    - ./scripts/layout.sh
  cwd: everysphere
```

`command` is an argv list, like an initialization command. `cwd` is optional
and relative to the Project root; the default working directory is the Project
root.

The session command receives the Project initialization variables
(`TWT2_PROJECT_ID`, `TWT2_PROJECT_NAME`, `TWT2_PROJECT_ROOT`, and
`TWT2_REPOSITORY_<NAME>`) and these tmux targets:

- `TWT2_TMUX_SESSION` is the tmux session ID, for example `$5`.
- `TWT2_TMUX_WINDOW_<NAME>` is the tmux window ID of the window of each
  repository, for example `@7`. The name part follows the repository name in
  upper case, with `-` and `.` changed to `_`.
- `TWT2_TMUX_SOCKET` is the tmux socket name when `twt2` uses a socket that is
  not the default one. It is empty for the default tmux server.

Use the IDs to make your own tmux targets. A pane target is
`<window id>.<pane index>`, and the pane index follows your
`pane-base-index` option. This script makes a three-pane layout in the
`everysphere` window: one pane beside the first pane that takes 34% of the
width, then one pane under the first pane that takes 25% of its height.

```sh
#!/bin/sh
set -e
window="$TWT2_TMUX_WINDOW_EVERYSPHERE"
tmux split-window -d -h -l 34% -t "$window" -c "$TWT2_REPOSITORY_EVERYSPHERE"
tmux split-window -d -v -l 25% -t "$window.1" -c "$TWT2_REPOSITORY_EVERYSPHERE"
```

The example uses `-l 34%`, the modern size flag. The old `-p 34` flag is
deprecated. `-d` keeps the new pane out of the focus. The example targets pane
`1` with `pane-base-index 1`; use `$window.0` with the tmux default.

A session command that fails makes the tmux setup step fail. The Project keeps
its record, and `twt2 projects setup retry PROJECT` runs the step again.

## Work with Projects

Create and open a Project:

```sh
twt2 projects create fix-auth --template everysphere
```

Without `--template`, `twt2` selects the only Project Template, or the
Project Template of the last successful creation. If neither rule applies, the
command lists the available names and stops.

Two flags control the Git start point:

- `--branch NAME` sets a custom Project branch name.
- `--no-fetch` uses the base commit of the Prepared Environment and does not
  refresh the default branch first.

`twt2` opens or attaches the tmux session only when standard output is a
terminal. Use `--no-open` to never open it. `--output json` no longer implies
no-open: a program that pipes the output gets no tmux change.

`twt2` names the new tmux session `twt2-<project name>`, for example
`twt2-fix-auth`. The prefix makes the sessions of `twt2` clear in the tmux
session picker. If a session with that name already exists and belongs to
something else, `twt2` adds the first 8 characters of the Project ID to the
name. The name is presentation only: `twt2` finds each session through the
tmux session ID and the `@twt2_project_id` option, so you can rename a session
and every command still works.

From the Project tmux session, create the next Project and archive the current
Project:

```sh
twt2 new fix-logout
```

For an interactive prompt, omit the name:

```sh
twt2 new
Project name: fix-logout
```

The command uses the latest saved version of the same Project Template. It
claims a matching Prepared Environment, switches the calling tmux client to
the new Project, and archives `fix-auth`. Other tmux clients do not switch.
Preparation of the replacement environment continues in the background.

`twt2 new` finds the current Project from the current directory, the
`TWT2_PROJECT_ID` value, or the current tmux pane. The tmux client switch
needs `TMUX_PANE`. From a plain shell inside a worktree, `twt2 new` uses
the Project Template of the current Project, creates the new Project, attaches
its session, and keeps the current Project active.

If creation or setup fails, the current Project stays active. `twt2` keeps a
Project that has a setup failure. You can inspect it and run
`twt2 projects setup retry PROJECT`. If the tmux switch fails, `twt2` archives
the new Project and keeps the current Project active.

Switch the tmux client to a different Project:

```sh
twt2 switch fix-auth
twt2 switch
```

`twt2 switch` moves your tmux client to the session of the Project. An
archived Project opens first. Without PROJECT, `twt2` shows an interactive
picker: it uses `fzf` when `fzf` is installed, or a numbered list. Inside
tmux the client switches; outside tmux `twt2` attaches. The command is
interactive and refuses `--output json`.

The short commands are for a person in tmux. For a script or coding agent, use
the explicit JSON commands:

```sh
twt2 projects create fix-logout \
  --template everysphere \
  --no-open \
  --dry-run \
  --output json
```

```sh
twt2 projects list
twt2 projects show fix-auth
twt2 projects current
twt2 projects path fix-auth everysphere
twt2 projects open fix-auth
```

The text list shows an aligned table with the Project name, Project Template,
status, and age, in that order. The list does not read disk sizes, so it stays
fast; use `twt2 storage show` for disk space:

```text
NAME      TEMPLATE     STATUS  AGE
fix-auth  everysphere  active  2h
```

Use `--output json` when a program needs the immutable Project ID.

Each command that takes a PROJECT argument also accepts the literal value
`current`. `twt2` then uses the current directory, the `TWT2_PROJECT_ID`
value, or the current tmux pane:

```sh
twt2 projects show current --output json
twt2 projects archive current
twt2 projects setup retry current
twt2 done current
```

`twt2` saves a snapshot of the Project Template before it changes Git or
tmux. Each setup step has a saved status. If setup fails, fix the cause and
retry the incomplete steps:

```sh
twt2 projects setup retry fix-auth
```

The retry uses the saved snapshot. It does not use a later edit to the
Project Template. A process stop can occur after an init command starts but
before `twt2` saves its result. For this reason, init commands must be safe to
run more than one time.

## Agent Sessions

Register a resumable Codex Agent Session:

```sh
twt2 agents register \
  --project current \
  --provider codex \
  --label auth-review \
  --session SESSION_ID \
  -- codex resume SESSION_ID
```

For safe feedback delivery, a live pane must have started the Agent directly.
A pane that started as a normal shell is not a valid feedback target, even if
an Agent now runs as a child process. The safe common flow is to register a
resume command without `--pane`, then run `agents resume`. twt2 starts that
Agent in its own window and records its direct process identity.

List, inspect, focus, resume, or send feedback:

```sh
twt2 agents list --project current
twt2 agents show AGENT_ID --project current
twt2 agents focus AGENT_ID
twt2 agents resume AGENT_ID
printf '%s\n' 'Please fix the selected review note.' | \
  twt2 agents send AGENT_ID --project current --stdin
```

`agents show` gives each liveness check with its result. A failed check tells
you why `twt2` does not send feedback. The current command of the pane is an
advisory check only.

`send` works only when the Agent Session has a live tmux pane that belongs to
the Project and still runs the registered direct Agent process. `resume`
focuses a live pane. If the pane stopped, `resume` starts the saved command in
a new Project window.

Find the provider sessions that ran inside a repository of the Project and
that no Agent Session uses:

```sh
twt2 agents discover --project current
twt2 agents discover --project current --adopt --limit 3
```

The newest session comes first. `--adopt` registers each discovered session
with a generated resume command. Discovery supports Codex and Claude.

Delete an Agent Session record with:

```sh
twt2 agents rm AGENT_ID --project current
```

Removal keeps the provider transcript and does not stop a live Agent process.

## Neovim integration

The repository includes the [`twt2.nvim`](../nvim/twt2.nvim/README.md)
preview plug-in. It uses the versioned JSON commands. It does not read `twt2`
state files or store tmux target values.

```sh
twt2 context --output json
twt2 context --directory /path/to/current/buffer --output json
twt2 agents list --project current --output json
twt2 agents resume AGENT_ID --output json
printf '%s' "$REVIEW_TEXT" | \
  twt2 agents send AGENT_ID --project PROJECT_ID --stdin --output json
twt2 agents transcript show AGENT_ID --project PROJECT_ID --output json
twt2 agents transcript snapshot AGENT_ID --project PROJECT_ID --output json
```

An explicit context directory takes priority over tmux and environment
context. This lets one Neovim process edit buffers from different Projects.
The JSON context also identifies the current repository in a multi-repository
Project.

The plug-in owns the picker, mappings, selected review text, and messages.
`twt2` owns the Project-owned Transcript Snapshots, Project lookup, Agent
Session records, provider transcript reading, resume behavior, and safe
feedback transport. Provider transcript paths and tmux targets do not enter
the JSON interface.

Linked transcript reading supports Codex and Claude. twt2 does not read Cursor
transcripts because the local Cursor records do not give an exact Project
directory that twt2 can verify.

`<leader>arp` selects a linked Agent Session. twt2 writes the transcript of
that Agent Session to
`$TWT2_STATE_DIR/snapshots/projects/PROJECT_ID/agents/AGENT_ID.md`, and writes
`latest.md` in the Project directory as a copy of the most recent snapshot. If
`TWT2_STATE_DIR` is not set, twt2 uses the normal XDG state directory.
Different Projects use different private files. Archive keeps these files.
`twt2 projects remove PROJECT --apply` deletes the matching owned snapshots.
For an older Agent Session, add the provider link:

```sh
twt2 agents transcript link AGENT_ID \
  --project current \
  --session SESSION_ID
```

## Archive, complete, and reopen a Project

Archive the current Project with the short command:

```sh
twt2 archive
```

You can also name a Project:

```sh
twt2 archive fix-auth
twt2 projects archive fix-auth
```

Archive stops the owned tmux session and live Agent processes. It keeps the
Project record, worktrees, branches, Project Template snapshot, repository
caches, and Agent Session records. An Agent Session can start again only if it
has a saved resume command.

To archive a Project and remove its data in one step, use `done`:

```sh
twt2 done
twt2 done fix-auth
twt2 done fix-auth --keep
twt2 done fix-auth --dry-run --output json
```

`done` archives the Project, then applies the removal plan. `--keep` stops
after the archive. `--allow-unpublished` removes a branch that has commits
which are not on another known ref.

From inside the Project tmux session, `done` moves your tmux client to the
most recent other active Project, or detaches the client, and a worker window
completes the work. This flow uses text output. For JSON output, run `done`
from a different session. A dry run reports the archive and the complete
removal plan and changes nothing.

Open an archived Project to make it active and create its tmux session again:

```sh
twt2 projects open fix-auth
twt2 projects open fix-auth --no-attach
```

From inside the Project tmux session, `archive` behaves like `done --keep`:
it moves your tmux client to the most recent other active Project, or
detaches the client, and a worker window completes the archive. This flow
uses text output; for JSON output, run `archive` from a different session.

## Storage and safe removal

Inspect disk use:

```sh
twt2 storage show
twt2 storage show --output json
```

`storage show` reports the total size, the shared repository caches, active
Project data, archived Project data, the number of worktrees, unclaimed
Prepared Environment data with a count for each status, and Transcript
Snapshot data.

`storage clean` finds failed or obsolete unclaimed environments, owned
snapshots whose Project record no longer exists, and Agent Session records
whose Project record no longer exists. If a Project Template YAML file is not
valid, `storage clean` gives a warning and keeps its Prepared Environments.
Preview the cleanup, then apply it:

```sh
twt2 storage clean
twt2 storage clean --apply
```

Project removal shows a plan by default. Archive the Project before you apply
the plan. Removal does not remove data until you use `--apply`:

```sh
twt2 projects archive fix-auth
twt2 projects remove fix-auth
twt2 projects remove fix-auth --apply
```

Removal stops only a tmux session that has the matching Project ID. It uses
`git worktree remove`. It removes only a Project root that has the matching
twt2 ownership marker.

A plan that is not safe carries one or more Removal Blockers. Each Removal
Blocker has a stable code, a message, the related paths, and sometimes a hint.
Removal applies no action while one Removal Blocker stays. These codes exist:

| Code | Cause |
|---|---|
| `not_archived` | The Project is not archived. |
| `inside_session` | The command runs inside the tmux session of the Project. |
| `unsafe_sessions` | Another tmux session claims the Project ID. |
| `uncommitted_changes` | A worktree has changes that are not committed. |
| `unpublished_branch` | The Project branch has commits that are not on another known ref. |
| `unpublished_unknown` | twt2 cannot prove that the branch is published. |
| `protected_branch` | The record names the default branch, or no branch. |
| `invalid_state` | The Project record does not match the layout that twt2 owns. |
| `unsafe_snapshot` | The Transcript Snapshot directory is not twt2-owned. |
| `unexpected_item` | The Project root contains an item that twt2 does not own. |

`--allow-unpublished` accepts the `unpublished_branch` cause. Correct the
other causes, then run the command again.

A Project that stops in the middle of removal keeps the `removing` status. To
return it to the archived status, run:

```sh
twt2 projects remove fix-auth --cancel
```

To clean many archived Projects, use bulk removal. Apply skips each blocked
Project and reports the count:

```sh
twt2 projects remove --all-archived
twt2 projects remove --all-archived --older-than 14d
twt2 projects remove --all-archived --older-than 14d --apply
```

Project removal keeps the shared repository cache. This makes later Project
creation fast. `storage show` includes these caches. Automatic cache removal
is not in this preview.

Inspect the Prepared Environment pool:

```sh
twt2 environments list
twt2 environments list --limit 10 --output json
twt2 environments show ENVIRONMENT_ID --output json
```

The text list groups the Prepared Environments by Project Template. Each line
shows the short ID, status, age, size, and the most useful value for that
status: the base commit, the preparation log of a failed environment, or the
Project that claims it. `environments show` accepts a unique ID prefix and
adds the preparation steps.

A ready environment that no longer matches its Project Template has status
`obsolete`. The comparison uses the Environment Digest: the hash of the part
of the Project Template that changes the physical worktrees. A change to the
template name, a window name, the Project initialization, or the pool depth
keeps a prepared set usable.

Check tools, YAML files, Project records, and ownership markers:

```sh
twt2 doctor
twt2 doctor --output json
```

## Track work with tickets

`twt2 tickets` is a personal Markdown ticket tracker. Ticket files are the
store, and the CLI owns every mutation; do not edit ticket files by hand.
This tracker is not Linear, GitHub Issues, or Origin issues.

### Configure Tickets home

Set the root directory of ticket Markdown files in
`$TWT2_CONFIG_DIR/config.yaml` (default `~/.config/twt2/config.yaml`):

```yaml
ticketsHome: /Users/john.pugliesi/Vaults/spacexai/tickets
```

`TWT2_TICKETS_HOME` overrides the file. YAML decoding rejects unknown fields
and more than one document, the same as Project Template loading.
`twt2 doctor` reports whether Tickets home is set, exists, and is writable.

### Commands

```sh
twt2 tickets init
twt2 tickets create [DESCRIPTION] [--board BOARD] [--title TITLE] [--slug SLUG] [--status STATUS] [--stdin]
twt2 tickets list [--board BOARD] [--status STATUS] [--ready] [--limit N]
twt2 tickets show TICKET
twt2 tickets edit TICKET [--stdin]
twt2 tickets set TICKET [--status STATUS] [--priority N] [--board BOARD]
twt2 tickets claim TICKET [--as NAME]
twt2 tickets unclaim TICKET [--as NAME]
twt2 tickets comment TICKET --stdin
twt2 tickets boards create NAME
twt2 tickets boards list [--limit N]
twt2 tickets boards show NAME
```

`twt2 tickets init` creates Tickets home if it is missing, and writes
`index.md` and `templates/ticket.md` only when those files are missing. It
never overwrites an existing note. `twt2 tickets boards create NAME` creates
the Board directory and writes `index.md` only when that file is missing.

### Create a ticket

| Input | Behavior |
|---|---|
| No args, stdout is a terminal, stdin is a terminal | Opens `$VISUAL` or `$EDITOR` on a temp copy of `templates/ticket.md`, then parses the saved file. An empty save is `invalid_usage`. |
| No args, not a terminal | Exits 2, with a hint to pass DESCRIPTION, `--title`, or `--stdin`. |
| DESCRIPTION args | Joins the args as the body. Derives `title` from the first line when `--title` is absent, and derives the slug from the title. |
| `--stdin` | Reads the body from standard input. Requires `--title`. |

The default status is `needs-triage`. `--dry-run` prints the file that would
be written and writes nothing.

```sh
twt2 tickets create "fix the vfs tools" --board change-monitor --dry-run --output json
twt2 tickets create "fix the vfs tools" --board change-monitor --output json
printf '%s' "$BODY" | twt2 tickets create --stdin --title "Fix the vfs tools" --output json
```

### List and filter

`--status` is a raw status filter on one of the six statuses:
`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`,
`wontfix`, `done`. It can still return a blocked or claimed ticket.

`--ready` is the pickable work queue, not a synonym of
`--status ready-for-agent`. A ticket matches `--ready` only when its status
is `ready-for-agent`, `claimed_by` is empty, and every `blocked_by` target
has status `done` or `wontfix`. Results sort by `priority` ascending, then
slug. Passing both `--ready` and `--status` exits 2 with a hint to use only
one.

```sh
twt2 tickets list --ready --output json --limit 20
twt2 tickets list --board change-monitor --status needs-triage --output json
```

`list` results omit the body. `show` returns the metadata and the body.

### Claim, unclaim, and comment

`claim` is a compare-and-set write on the resolved claimant:

- Empty `claimed_by` becomes the claimant, and `claimed_at` is set.
- The same claimant succeeds again with no change.
- A different claimant gets `locked`, with the current claimant in the hint.

`claimed_by` resolves in this order: `--as NAME`, then `TWT2_CLAIMANT`, then
the OS username. A terminal claim may fall back to the OS username. A
non-terminal claim, and every `apply` claim or unclaim, must set `--as` or
`TWT2_CLAIMANT`, or the command exits 2 with a hint to pass `--as NAME`. This
stops two agents from both succeeding as the same OS user. Agents should
pass a unique `--as` value per session, such as `codex-fix-auth` or the
Agent Session ID.

`unclaim` uses the same claimant resolution. It succeeds only when
`claimed_by` is empty or equals the resolved claimant, and it then clears
`claimed_by` and `claimed_at`. Resolve shipped work with
`twt2 tickets set TICKET --status done`, then `unclaim`:

```sh
twt2 tickets claim TICKET --as codex-fix-auth --output json
twt2 tickets set TICKET --status done --output json
twt2 tickets unclaim TICKET --as codex-fix-auth --output json
```

`comment` requires `--stdin`. It appends the text under the `## Comments`
heading, creating that heading if it is missing, and sets `updated`:

```sh
printf '%s' "$NOTE" | twt2 tickets comment TICKET --stdin --output json
```

### Boards

A Board is one directory under Tickets home, with its own `index.md`. It
groups tickets and outlives any single Project checkout. Use `board:` in
ticket frontmatter, not `project:`.

```sh
twt2 tickets boards create change-monitor --output json
twt2 tickets boards list --output json
twt2 tickets boards show change-monitor --output json
```

### Resolve a TICKET argument

A `TICKET` argument resolves in this order:

1. Exact slug
2. Unique prefix
3. `title`
4. `aliases`
5. Wiki-link form `[[…]]`
6. Path under Tickets home

An ambiguous prefix returns `invalid_usage`, with the candidate slugs in
`hint`.

Existing legacy ticket files, such as `tkt-cm-001.md`, stay valid: the
resolver accepts any Markdown stem, not only a kebab slug. When `twt2
tickets` mutates a file, it keeps frontmatter fields it does not recognize,
so hand edits to a ticket are not lost on the next CLI write.

### Install the skill in three trees

Keep one canonical skill file in this repository at
[`skills/twt2/SKILL.md`](../skills/twt2/SKILL.md). Symlink it into each user
skill tree so Cursor, Claude Code, and Codex all see the same rules:

```sh
mkdir -p ~/.cursor/skills/twt2 ~/.claude/skills/twt2 ~/.agents/skills/twt2
ln -sf "$(pwd)/skills/twt2/SKILL.md" ~/.cursor/skills/twt2/SKILL.md
ln -sf "$(pwd)/skills/twt2/SKILL.md" ~/.claude/skills/twt2/SKILL.md
ln -sf "$(pwd)/skills/twt2/SKILL.md" ~/.agents/skills/twt2/SKILL.md
```

## JSON contract

The JSON output has `schemaVersion: 1`. It uses immutable IDs, RFC 3339 time
values, stable status strings, and Agent Session capability fields such as
`canResume` and `canSend`. It does not expose state-file paths or tmux target
values.

For coding agents, `--output json` is the only output switch for all command
groups.

A read of one object uses a named envelope, for example:

```json
{"schemaVersion":1,"project":{"id":"...","name":"fix-auth","status":"active"}}
```

A list uses the plural name, the total before the limit, and a truncation
flag. The elements do not repeat the schema version:

```json
{"schemaVersion":1,"templates":[{"name":"everysphere"}],"totalCount":3,"truncated":true}
```

Use `--limit` on `templates list`, `projects list`, `agents list`,
`agents discover`, and `environments list` to control response size. The
`totalCount` value tells you how many results exist, and `truncated` tells you
that the limit removed results.

JSON errors use the same schema version. Each error has a stable code, a
message, an optional full-sentence hint, and, for a usage error, the help
command:

```json
{"schemaVersion":1,"error":{"code":"precondition_failed","message":"...","hint":"..."}}
```

These error codes exist: `already_exists`, `internal`, `invalid_usage`,
`locked`, `not_found`, `precondition_failed`, and `unsafe_state`.

The process exit code groups the error codes:

| Exit | Meaning | Error codes |
|---|---|---|
| 0 | success | none |
| 1 | internal | `internal` |
| 2 | invalid usage | `invalid_usage` |
| 3 | failed precondition | `not_found`, `already_exists`, `precondition_failed`, `locked`, `unsafe_state` |

Inspect the installed command and request schema at runtime:

```sh
twt2 schema
```

The schema gives the build version, each command with its positional
arguments and flags, the flag enums, the `apply` operations with their request
fields, all error codes, and all exit codes. It does not contain the generated
`help` and `completion` commands.

Validate a mutation without a state, Git, or tmux change. Every mutation
accepts `--dry-run`:

```sh
twt2 projects create fix-auth \
  --template everysphere \
  --no-open \
  --dry-run \
  --output json
```

Some common mutations also accept one strict JSON request. Unknown fields and
more than one JSON value cause an error.

```sh
printf '%s' '{"operation":"projects.create","project":{"name":"fix-auth","template":"everysphere"}}' | \
  twt2 apply --stdin --dry-run --output json
```

`apply` supports these operations. Run `twt2 schema` for the field list of
each one:

| Operation | Payload | Required fields |
|---|---|---|
| `templates.create` | `template` | `template.name` |
| `templates.repos.add` | `template` | `template.name`, `template.repository.name`, `template.repository.url` |
| `projects.create` | `project` | `project.name`, `project.template` |
| `projects.archive` | `project` | `project.reference` |
| `projects.remove` | `project` | `project.reference` |
| `agents.register` | `agent` | `agent.project`, `agent.provider` |

`projects.remove` returns the removal plan. Add `"apply":true` to remove the
data:

```sh
printf '%s' '{"operation":"projects.remove","project":{"reference":"fix-auth","apply":true}}' | \
  twt2 apply --stdin --output json
```

`twt2` treats its caller as an untrusted operator. It validates every resource
name, rejects unknown YAML and JSON fields, keeps initialization paths inside
the Project root, and changes only paths that carry a twt2 ownership marker.

The repository includes a discoverable [`twt2` agent
skill](../skills/twt2/SKILL.md). It tells agents to inspect the schema, limit
read results, run a dry-run first, and use IDs instead of tmux display names.
See the [Agent DX score](agent-dx.md) for the detailed before-and-after score.
