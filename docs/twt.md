# twt preview

`twt` is the Go preview of the next tmux-worktree user experience. It uses a
Workspace as the unit of work. A Workspace can contain one or more Git worktrees,
one tmux session, one window for each repository, and a set of Agent Sessions.

The existing `twt` command continues to work. `twt` uses separate config,
state, and data directories.

## Build

```sh
go install ./cmd/twt
```

To stamp the build with a version, set the version variable with `-ldflags`.
The `twt --version` and `twt schema` commands show this value:

```sh
go install -ldflags "-X github.com/jpugliesi/tmux-worktree/internal/version.Version=$(git describe --always)" ./cmd/twt
```

By default, `twt` uses these directories:

- Config: `${XDG_CONFIG_HOME:-$HOME/.config}/twt`
- State: `${XDG_STATE_HOME:-$HOME/.local/state}/twt`
- Data: `${XDG_DATA_HOME:-$HOME/.local/share}/twt`

You can set `TWT_CONFIG_DIR`, `TWT_STATE_DIR`, and `TWT_DATA_DIR` to use
other directories. Tests can set `TWT_TMUX_SOCKET` to use a separate tmux
server.

`twt config` shows every resolved setting, including defaults, and the source
of each value:

```sh
twt config
twt config --output json
```

The source is `env` for an environment variable, `file` for
`$TWT_CONFIG_DIR/config.yaml`, or `default`. The origin names the variable
or the config file path.

## Install shell completion

`twt` completes command names, Workspace Template names, Workspace names, Ticket
slugs, Agent Session IDs, Prepared Environment IDs, and the values of
`--template`, `--workspace`, `--provider`, and `--output`. `twt next` and
`twt tickets start` offer Ticket slugs:

```sh
# After `compinit` in ~/.zshrc. `compdef` exists only then.
eval "$(twt completion zsh)"
```

Or write a file on `fpath` before `compinit`:

```sh
mkdir -p ~/.local/share/zsh/site-functions
twt completion zsh > ~/.local/share/zsh/site-functions/_twt
```

Use `twt completion bash`, `twt completion fish`, or
`twt completion powershell` for the other shells.

## Create the Everysphere Workspace Template

Create an empty YAML file:

```sh
twt templates create everysphere
```

Add the repository clone policy:

```sh
twt templates repos add everysphere everysphere \
  https://origin.cursor.com/anysphere/everysphere.git \
  --depth 1 \
  --remote github=https://github.com/anysphere/everysphere.git \
  --default-branch main \
  --window-name everysphere
```

Set the repository initialization command. `twt` runs this command in the
new worktree:

```sh
twt templates init set everysphere --repo everysphere -- ./init.sh
```

To remove one repository specification, run:

```sh
twt templates repos remove everysphere everysphere
```

You can also edit
`~/.config/twt/templates/everysphere.yaml` directly:

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
agents:
  - label: coder
    provider: claude
    start: [claude, "Create the first plan."]
    resume: [claude]
    prefer_provider_resume: true
```

`pool_depth` is the number of ready Prepared Environments that `twt` keeps
for this Workspace Template. The default depth is 1.

`branch_pattern` sets the default Workspace branch name of the Workspace
Template, for example `branch_pattern: "{prefix}dev/{name}"`. See
[Workspace branch names](#workspace-branch-names).

An Agent declaration can set a separate `resume` command. If it is absent,
twt uses `start` as before. Use `resume` when `start` contains an initial
prompt that must run only one time. `prefer_provider_resume` makes a verified
linked provider session take precedence over the fallback command.

Work with the YAML file through these commands:

```sh
twt templates path everysphere
twt templates edit everysphere
twt templates validate everysphere
twt templates list
twt templates show everysphere --output json
```

`templates path` prints one bare path. `templates edit` starts `$VISUAL` or
`$EDITOR` on the file, then validates the result. An invalid file stays on
disk, and `twt` returns the `unsafe_state` error with the validation cause.

`templates validate` also reports warnings. A warning does not make the
command fail. A Workspace Template with no repositories gives one warning.

To make a Workspace Template from a file or from a pipe, use these flags. The
NAME argument stays required: it sets the template name, and a different
`name` field in the document is an error.

```sh
twt templates create everysphere --from-file ./everysphere.yaml
cat ./everysphere.yaml | twt templates create everysphere --from-stdin
```

Delete a Workspace Template with:

```sh
twt templates remove everysphere
```

Removal deletes only the YAML file. `twt` refuses the removal while a
Workspace record still uses the Workspace Template. Remove those Workspaces first.

Prepare the next environment before you need it:

```sh
twt templates prepare everysphere
```

This command creates one worktree for each repository and runs repository
initialization once on each new physical worktree. A Workspace later claims the
complete Prepared Environment. After each claim, twt prepares one replacement
in the background, up to the pool depth.

Repository initialization runs before a Workspace name exists. It receives
`TWT_ENVIRONMENT_ID`, `TWT_ENVIRONMENT_ROOT`, `TWT_TEMPLATE_NAME`,
`TWT_REPOSITORY_NAME`, and `TWT_REPOSITORY_PATH`. Use Workspace initialization
when setup needs a Workspace ID or name.

To run one command after all repository worktrees exist, set a Workspace
initialization command. You must set its working directory relative to the
Workspace root:

```sh
twt templates init set everysphere \
  --cwd everysphere \
  -- ./scripts/init-workspace.sh
```

`templates init set` sets one initialization command. With `--repo REPO` it
sets repository initialization, and `--cwd` is not valid. Without `--repo` it
sets Workspace initialization, and `--cwd PATH` is required.

Workspace initialization receives these environment variables:

- `TWT_WORKSPACE_ID`
- `TWT_WORKSPACE_NAME`
- `TWT_WORKSPACE_ROOT`
- `TWT_REPOSITORY_<NAME>` for each repository

## Lay out the tmux session

A Workspace Template can declare one session command. `twt` runs it each time it
creates the tmux session of a Workspace: at Workspace creation, and again when
`twt workspaces open` makes the session for an archived Workspace. The command
runs after `twt` makes the session and one window for each repository.

`twt` never runs the command against a session that is already live. A setup
retry on a live session skips it, so the command cannot disturb panes that you
arranged.

```yaml
session:
  command:
    - ./scripts/layout.sh
  cwd: everysphere
```

`command` is an argv list, like an initialization command. `cwd` is optional
and relative to the Workspace root; the default working directory is the Workspace
root.

The session command receives the Workspace initialization variables
(`TWT_WORKSPACE_ID`, `TWT_WORKSPACE_NAME`, `TWT_WORKSPACE_ROOT`, and
`TWT_REPOSITORY_<NAME>`) and these tmux targets:

- `TWT_TMUX_SESSION` is the tmux session ID, for example `$5`.
- `TWT_TMUX_WINDOW_<NAME>` is the tmux window ID of the window of each
  repository, for example `@7`. The name part follows the repository name in
  upper case, with `-` and `.` changed to `_`.
- `TWT_TMUX_SOCKET` is the tmux socket name when `twt` uses a socket that is
  not the default one. It is empty for the default tmux server.

Use the IDs to make your own tmux targets. A pane target is
`<window id>.<pane index>`, and the pane index follows your
`pane-base-index` option. This script makes a three-pane layout in the
`everysphere` window: one pane beside the first pane that takes 34% of the
width, then one pane under the first pane that takes 25% of its height.

```sh
#!/bin/sh
set -e
window="$TWT_TMUX_WINDOW_EVERYSPHERE"
tmux split-window -d -h -l 34% -t "$window" -c "$TWT_REPOSITORY_EVERYSPHERE"
tmux split-window -d -v -l 25% -t "$window.1" -c "$TWT_REPOSITORY_EVERYSPHERE"
```

The example uses `-l 34%`, the modern size flag. The old `-p 34` flag is
deprecated. `-d` keeps the new pane out of the focus. The example targets pane
`1` with `pane-base-index 1`; use `$window.0` with the tmux default.

A session command that fails makes the tmux setup step fail. The Workspace keeps
its record, and `twt workspaces setup retry WORKSPACE` runs the step again.

## Work with Workspaces

Create and open a Workspace:

```sh
twt create fix-auth --template everysphere
```

`twt create` is the short form of `twt workspaces create`.

Without `NAME`, and when standard input and standard output are terminals and
output is text, `twt` asks for a Workspace name. A script, a pipe, and
`--output json` still require `NAME`.

Without `--template`, `twt` selects the only Workspace Template, or the
Workspace Template of the last successful creation. If neither rule applies, the
command lists the available names and stops.

Two flags control the Git start point:

- `--branch NAME` sets a custom Workspace branch name.
- Create fetches `origin/<default-branch>` before it claims the Prepared
  Environment. `--no-fetch` uses the saved base commit and skips that fetch.

### Workspace branch names

`twt` selects the Workspace branch name in this order:

1. The `--branch` flag. It ignores the branch prefix.
2. The `branch_pattern` of the Workspace Template.
3. The default pattern `{prefix}{name}`. Without a configured branch prefix
   this is the plain Workspace name, for example `fix-auth`.

A `branch_pattern` uses these tokens: `{prefix}` is the user branch prefix,
`{name}` is the Workspace name, and `{id8}` is the first 8 characters of the
Workspace ID. The pattern must contain `{name}` and must render a valid Git
branch name. The pattern is presentation only: an edit keeps each ready
Prepared Environment claimable.

Set the user branch prefix in `$TWT_CONFIG_DIR/config.yaml`:

```yaml
branchPrefix: jpugliesi/
```

`TWT_BRANCH_PREFIX` overrides the file. `twt` concatenates the prefix
literally, so include the separator in the prefix value.

Two safety rules apply to the resolved branch name:

- The name must not equal the default branch of a repository. `twt` refuses
  the creation with the `invalid_usage` error.
- When the name already exists in a Repository Cache, `twt` falls back to
  `twt/<name>-<id8>` and reports the fallback.

`twt` opens or attaches the tmux session only when standard output is a
terminal. Use `--no-open` to never open it. `--output json` no longer implies
no-open: a program that pipes the output gets no tmux change.

`twt` names the new tmux session `<template name>-<workspace name>`, for
example `everysphere-fix-auth`. The Workspace Template name comes first, thus
the tmux session picker groups the sessions of one codebase together. If a
session with that name already exists and belongs to
something else, `twt` adds the first 8 characters of the Workspace ID to the
name. The name is presentation only: `twt` finds each session through the
tmux session ID and the `@twt_workspace_id` option, so you can rename a session
and every command still works.

From the Workspace tmux session, create the next Workspace and archive the current
Workspace:

```sh
twt next fix-logout
```

Omit the name to pick an open Ticket. twt uses `fzf` when `fzf` is
installed, or a numbered list. One or more Ticket slugs from one Project
claim those Tickets and link the new Workspace to them. If no Tickets exist,
twt asks for a Workspace name:

```sh
twt next
twt next fix-auth-tokens add-auth-tests
```

The command uses the latest saved version of the same Workspace Template. It
claims a matching Prepared Environment, switches the calling tmux client to
the new Workspace, and archives `fix-auth`. Other tmux clients do not switch.
Preparation of the replacement environment continues in the background.

`twt next` finds the current Workspace from the current directory, the
`TWT_WORKSPACE_ID` value, or the current tmux pane. A real run must be inside
the tmux session of that Workspace because the switch needs `TMUX_PANE`.
Use `twt create` from a plain shell or when the current Workspace must stay
active.

If creation or setup fails, the current Workspace stays active. `twt` keeps a
Workspace that has a setup failure. You can inspect it and run
`twt workspaces setup retry WORKSPACE`. If the tmux switch fails, `twt` keeps
both the new Workspace and the current Workspace active.

Switch the tmux client to a different Workspace:

```sh
twt switch fix-auth
twt switch
```

`twt switch` moves your tmux client to the session of the Workspace. An
archived Workspace opens first. Without WORKSPACE, `twt` shows an interactive
picker: it uses `fzf` when `fzf` is installed, or a numbered list. Inside
tmux the client switches; outside tmux `twt` attaches. The command is
interactive and refuses `--output json`.

The short commands are for a person in tmux. For a script or coding agent, use
the explicit JSON commands:

```sh
twt create fix-logout \
  --template everysphere \
  --no-open \
  --dry-run \
  --output json
```

```sh
twt workspaces list
twt workspaces list --project change-monitor --status active
twt workspaces show fix-auth
twt workspaces current
twt workspaces path fix-auth everysphere
twt workspaces open fix-auth
twt workspaces open --all-active --no-attach
```

`twt workspaces open` repairs a missing tmux session of an active Workspace. It
also claims an unowned session whose name matches the Workspace, so a
tmux-resurrect restore after a reboot does not create a second session.
`--all-active` repairs every active Workspace and attaches no client.

The text list shows an aligned table with the Workspace name, Workspace Template,
status, and age, in that order. The list does not read disk sizes, so it stays
fast; use `twt storage show` for disk space:

```text
NAME      TEMPLATE     STATUS  AGE
fix-auth  everysphere  active  2h
```

Use `--output json` when a program needs the immutable Workspace ID.

Each command that takes a WORKSPACE argument also accepts the literal value
`current`. `twt` then uses the current directory, the `TWT_WORKSPACE_ID`
value, or the current tmux pane:

```sh
twt workspaces show current --output json
twt workspaces archive current
twt workspaces setup retry current
twt done current
```

`twt` saves a snapshot of the Workspace Template before it changes Git or
tmux. Each setup step has a saved status. If setup fails, fix the cause and
retry the incomplete steps:

```sh
twt workspaces setup retry fix-auth
```

The retry uses the saved snapshot. It does not use a later edit to the
Workspace Template. A process stop can occur after an init command starts but
before `twt` saves its result. For this reason, init commands must be safe to
run more than one time.

### Adopt an existing tmux session

A tmux session that you made by hand can become a Workspace:

```sh
twt workspaces adopt
twt workspaces adopt my-session --name fix-auth
```

Without a SESSION argument, `twt` adopts the tmux session of the calling
pane. The default Workspace name is the session name. `twt` records the git
repositories that the panes of the session sit in, and marks the session with
the Workspace ID. After the adopt, `switch`, `context`, and the Agent Session
commands work: a Codex or Claude session that ran inside an adopted
repository appears in `agents list` as a discovered session.

`twt` did not create the directories of an adopted Workspace. Removal never
deletes them: `twt done` and `workspaces remove` delete only the twt state,
show `keep_directory` actions in the plan, and release the session marker. A
session with no git repository in any pane adopts with zero repositories.

## Agent Sessions

Register a resumable Codex Agent Session:

```sh
twt agents register \
  --workspace current \
  --provider codex \
  --label auth-review \
  --session SESSION_ID \
  -- codex resume SESSION_ID
```

For safe feedback delivery, twt accepts a direct Agent process or a verified
Agent process below a normal shell. Live process discovery supports Codex,
Claude Code, Cursor Agent (`cursor-agent` and its verified `agent` alias), and
Grok. A generic program named `agent` is not sufficient proof. twt checks the
Cursor installation path and launcher script before it identifies that alias.
Manual `register --pane` keeps its direct-process rule. For an Agent below a
shell, use the candidate from `agents list` with `agents adopt`.

List, inspect, pick, focus, resume, or send feedback:

```sh
twt agents list --workspace current
twt agents show AGENT_ID --workspace current
twt agents open
twt agents focus AGENT_ID
twt agents resume AGENT_ID
printf '%s\n' 'Please fix the selected review note.' | \
  twt agents send AGENT_ID --workspace current --stdin
```

`agents open` shows an interactive Agent Session picker when AGENT_ID is
absent. It uses `fzf` when `fzf` is installed, or a numbered list. The fzf
preview shows an Agent Preview. It uses a verified transcript when one is
available. Otherwise, it shows a bounded and sanitized view of the visible
screen of a verified live pane. It does not read pane scrollback. A live
selection focuses that pane. A stopped selection starts `codex resume`,
`claude --resume`, or `grok --resume` in the current pane. Preview never
registers a discovered session and never writes a snapshot.

`agents list` finds verified live processes for all four supported coding
agents. It also scans the Codex, Claude, and Grok stores. A provider
session that ran inside a repository of the Workspace, and that no Agent
Session uses, appears with status `discovered` and a provider-qualified,
versioned candidate value as `id`. The raw provider session ID stays in
`providerSessionId`. The newest session comes first. Registered and discovered sessions
share one recency order. Text output is provider, ID, and age. The list
writes nothing.

`adopt` registers a discovered session without another action. `resume`,
`open`, and `send` also adopt before they act. They accept the versioned
candidate reference or a unique prefix. Transcript show and every picker
preview stay read-only. A manually started Codex, Claude Code, Cursor Agent,
or Grok process in an owned Workspace pane needs no manual `register` step.

```sh
twt agents adopt AGENT_ID --workspace current
```

Use `--registered` in a script that must not scan the provider stores. Use
`--live=false` for the cheap statusline path: it does not probe tmux and does
not scan the provider stores.

The JSON list has `complete` and `diagnostics` fields. A provider-store,
process-table, or tmux scan failure sets `complete` to `false` and keeps valid
results from the other sources. NDJSON puts the same fields in its final
summary line. Text output writes a warning to standard error. The Neovim
picker stops and shows the diagnostic, so it does not silently omit live
agents.

`agents show` gives each liveness check with its result. For an adopted
shell-hosted process, twt checks the saved process ID, process start time,
provider, pane marker, and current input target. Focus can work while the
Agent uses a child tool. Send requires the Agent input target to be current.

`send` works only when the Agent Session has a live tmux pane that belongs to
the Workspace and still has the same verified process. `resume` focuses a
live pane. If a saved resume command exists, it can start a stopped session in
a new Workspace window.

To read the discovered sessions alone, or to adopt many sessions at one time,
use discovery:

```sh
twt agents discover --workspace current
twt agents discover --workspace current --adopt --limit 3
```

The newest session comes first. `--adopt` registers each discovered session
with a generated resume command. Discovery supports Codex, Claude, and Grok.

Delete an Agent Session record with:

```sh
twt agents rm AGENT_ID --workspace current
```

Removal keeps the provider transcript and does not stop a live Agent process.
`rm` does not adopt: a reference that names a discovered but unregistered
session is invalid usage.

## Neovim integration

The repository includes the [`twt.nvim`](../nvim/twt.nvim/README.md)
preview plug-in. It uses the versioned JSON commands. It does not read `twt`
state files or store tmux target values.

```sh
twt context --output json
twt context --directory /path/to/current/buffer --output json
twt agents list --workspace current --output json
twt agents resume AGENT_ID --output json
printf '%s' "$REVIEW_TEXT" | \
  twt agents send AGENT_ID --workspace WORKSPACE_ID --stdin --output json
twt agents transcript show AGENT_ID --workspace WORKSPACE_ID --output json
twt agents transcript snapshot AGENT_ID --workspace WORKSPACE_ID --output json
```

An explicit context directory takes priority over tmux and environment
context. This lets one Neovim process edit buffers from different Workspaces.
The JSON context also identifies the current repository in a multi-repository
Workspace.

The plug-in owns the picker, mappings, selected review text, and messages.
`twt` owns the Workspace-owned Transcript Snapshots, Workspace lookup, Agent
Session records, provider transcript reading, resume behavior, and safe
feedback transport. Provider transcript paths and tmux targets do not enter
the JSON interface.

Linked transcript reading supports Codex, Claude, and Grok. twt does not read Cursor
transcripts because the local Cursor records do not give an exact Workspace
directory that twt can verify.

`<leader>arp` shows all verified Agent Sessions. A transcript selection writes
the transcript to
`$TWT_STATE_DIR/snapshots/workspaces/WORKSPACE_ID/agents/AGENT_ID.md`, and writes
`latest.md` in the Workspace directory as a copy of the most recent snapshot. If
`TWT_STATE_DIR` is not set, twt uses the normal XDG state directory.
Different Workspaces use different private files. A live Cursor selection has
no verified transcript, so it selects the pane and opens the Agent Preview in a
scratch buffer. It does not write a Transcript Snapshot. Archive keeps the snapshot files.
`twt workspaces remove WORKSPACE --apply` deletes the matching owned snapshots.
For an older Agent Session, add the provider link:

```sh
twt agents transcript link AGENT_ID \
  --workspace current \
  --session SESSION_ID
```

## Archive, complete, and reopen a Workspace

Archive the current Workspace with the short command:

```sh
twt archive
```

You can also name a Workspace:

```sh
twt archive fix-auth
twt workspaces archive fix-auth
```

Archive stops the owned tmux session and live Agent processes. It keeps the
Workspace record, worktrees, branches, Workspace Template snapshot, repository
caches, and Agent Session records. An Agent Session can start again only if it
has a saved resume command.

To archive a Workspace and remove its data in one step, use `done`:

```sh
twt done
twt done fix-auth
twt done fix-auth --keep
twt done fix-auth --dry-run --output json
```

`done` archives the Workspace, then applies the removal plan. `--keep` stops
after the archive. `--force` removes a branch that has commits which are not
on the remote.

When the Workspace links one open Ticket, an interactive `done` asks `Close
Ticket "<slug>"? [y/N]` before any change; the default is No. On yes, `done`
closes the Ticket after a successful removal. When the Workspace links many
open Tickets, `done` does not close any of them. It prints one close command
for each Ticket. A close failure gives a warning with the `twt tickets close
<slug>` hint and never fails `done`. Without a terminal, with `--output json`,
with `--keep`, or on no, `done` keeps the Ticket open and prints the close
hint. A dry run never asks.

From inside the Workspace tmux session, `done` moves your tmux client to the
most recent other active Workspace, or detaches the client, and a worker window
completes the work. This flow uses text output. For JSON output, run `done`
from a different session. A dry run reports the archive and the complete
removal plan and changes nothing.

Open an archived Workspace to make it active and create its tmux session again.
The same command repairs an active Workspace whose session is missing after a
reboot:

```sh
twt workspaces open fix-auth
twt workspaces open fix-auth --no-attach
twt workspaces open --all-active --dry-run --output json
twt workspaces open --all-active
```

From inside the Workspace tmux session, `archive` behaves like `done --keep`:
it moves your tmux client to the most recent other active Workspace, or
detaches the client, and a worker window completes the archive. This flow
uses text output; for JSON output, run `archive` from a different session.

## Storage and safe removal

Inspect disk use:

```sh
twt storage show
twt storage show --output json
```

`storage show` reports the total size, the shared repository caches, active
Workspace data, archived Workspace data, the number of worktrees, unclaimed
Prepared Environment data with a count for each status, and Transcript
Snapshot data.

`storage clean` finds failed or obsolete unclaimed environments, owned
snapshots whose Workspace record no longer exists, and Agent Session records
whose Workspace record no longer exists. If a Workspace Template YAML file is not
valid, `storage clean` gives a warning and keeps its Prepared Environments.
Preview the cleanup, then apply it:

```sh
twt storage clean
twt storage clean --apply
```

Workspace removal shows a plan by default. Archive the Workspace before you apply
the plan. Removal does not remove data until you use `--apply`:

```sh
twt workspaces archive fix-auth
twt workspaces remove fix-auth
twt workspaces remove fix-auth --apply
```

Removal stops only a tmux session that has the matching Workspace ID. It uses
`git worktree remove`. It removes only a Workspace root that has the matching
twt ownership marker.

A plan that is not safe carries one or more Removal Blockers. Each Removal
Blocker has a stable code, a message, the related paths, and sometimes a hint.
Removal applies no action while one Removal Blocker stays. These codes exist:

| Code | Cause |
|---|---|
| `not_archived` | The Workspace is not archived. |
| `inside_session` | The command runs inside the tmux session of the Workspace. |
| `unsafe_sessions` | Another tmux session claims the Workspace ID. |
| `uncommitted_changes` | A worktree has changes that are not committed. |
| `unpublished_branch` | The Workspace branch has commits that are not on a remote-tracking ref and not on the remote. |
| `unpublished_unknown` | twt cannot prove that the branch is published. |
| `protected_branch` | The record names the default branch, or no branch. |
| `invalid_state` | The Workspace record does not match the layout that twt owns. |
| `unsafe_snapshot` | The Transcript Snapshot directory is not twt-owned. |
| `unexpected_item` | The Workspace root contains an item that twt does not own. |

`--force` accepts the `unpublished_branch` cause. Correct the
other causes, then run the command again.

A Workspace that stops in the middle of removal keeps the `removing` status. To
return it to the archived status, run:

```sh
twt workspaces remove fix-auth --cancel
```

To clean many archived Workspaces, use bulk removal. Apply skips each blocked
Workspace and reports the count:

```sh
twt workspaces remove --all-archived
twt workspaces remove --all-archived --older-than 14d
twt workspaces remove --all-archived --older-than 14d --apply
```

Workspace removal keeps the shared repository cache. This makes later Workspace
creation fast. `storage show` includes these caches. Automatic cache removal
is not in this preview.

Inspect the Prepared Environment pool:

```sh
twt environments list
twt environments list --size
twt environments list --limit 10 --output json
twt environments show ENVIRONMENT_ID --output json
```

The text list groups the Prepared Environments by Workspace Template. Each line
shows the short ID, status, age, and the most useful value for that status: the
base commit, the preparation log of a failed environment, or the Workspace that
claims it. Use `--size` to calculate and show Prepared Environment storage. The
size scan can take time on a large worktree. It does not scan roots that are
reserved for a Workspace. `environments show` accepts a unique ID prefix and
adds the preparation steps.

Full JSON and NDJSON output calculates the size of each Environment in the
selected page. Use `--fields` without `bytes` for a fast metadata-only read.
The `bytes` value is `null` when a Workspace owns or reserves the root.

A ready environment that no longer matches its Workspace Template has status
`obsolete`. The comparison uses the Environment Digest: the hash of the part
of the Workspace Template that changes the physical worktrees. A change to the
template name, a window name, the Workspace initialization, or the pool depth
keeps a prepared set usable.

Check tools, YAML files, Workspace records, and ownership markers:

```sh
twt doctor
twt doctor --output json
```

## Track work with tickets

`twt tickets` is a personal Markdown ticket tracker. Ticket files are the
store, and the CLI owns every mutation; do not edit ticket files by hand.
This tracker is not Linear, GitHub Issues, or Origin issues.

### Configure Tickets home

Set the root directory of ticket Markdown files in
`$TWT_CONFIG_DIR/config.yaml` (default `~/.config/twt/config.yaml`):

```yaml
ticketsHome: /Users/john.pugliesi/Vaults/spacexai/tickets
ticketAgent:
  provider: codex
  effort: large
  instructions: |
    Read the repository design notes first.
```

`TWT_TICKETS_HOME` overrides the file. YAML decoding rejects unknown fields
and more than one document, the same as Workspace Template loading.
`twt config` shows the resolved Tickets home and its source. `twt doctor`
reports whether Tickets home is set, exists, and is writable.

### Commands

```sh
twt tickets init
twt tickets home
twt tickets create [DESCRIPTION] [--project PROJECT] [--title TITLE] [--slug SLUG] [--status STATUS] [--blocked-by SLUG] [--stdin]
twt tickets list [--project PROJECT] [--all-projects] [--status STATUS] [--ready] [--claimed] [--all] [--limit N]
twt tickets queue [--project PROJECT] [--limit N]
twt tickets show TICKET
twt tickets edit TICKET [--stdin]
twt tickets set TICKET [--status STATUS] [--priority N] [--project PROJECT] [--blocked-by SLUG]
twt tickets claim TICKET [--as NAME] [--workspace WORKSPACE]
twt tickets start [TICKET...] [--name NAME] [--template TEMPLATE] [--as NAME] [--with-agent] [--detached]
twt tickets unclaim TICKET [--as NAME]
twt tickets close TICKET [--as NAME]
twt tickets comment TICKET --stdin
twt projects create NAME
twt projects list [--limit N]
twt projects show NAME
```

`twt tickets init` creates Tickets home if it is missing, and writes
`index.md` and `templates/ticket.md` only when those files are missing. It
never overwrites an existing note. `twt tickets home` opens that directory
in `$VISUAL` or `$EDITOR`. It is interactive and has no apply operation.
`twt projects create NAME` creates the Project directory and writes
`index.md` only when that file is missing.

`twt tickets queue` reads one Ticket index snapshot. The Project comes from
`--project`, then `TWT_PROJECT`, then the current Workspace Project. It
returns the complete open Project graph and a deterministic `ready` list.
Each dependency reports its state and Project. `cycles` reports dependency
cycles. `--limit` cuts only `ready`. It does not cut the graph.

### Create a ticket

| Input | Behavior |
|---|---|
| No args, stdout is a terminal, stdin is a terminal | Asks for a title, then a Project (`fzf` or a numbered list, with `(none)` for ungrouped). A new Project name is created only after confirm. Then opens `$VISUAL` or `$EDITOR` on an empty file for the description. The CLI writes YAML frontmatter. |
| No args, not a terminal | Exits 2, with a hint to pass DESCRIPTION, `--title`, or `--stdin`. |
| DESCRIPTION args | Joins the args as the body. Derives `title` from the first line when `--title` is absent, and derives the slug from the title. Never opens the wizard. |
| `--stdin` | Reads the body from standard input. Requires `--title`. Never opens the wizard. |

The default status is `needs-triage`. `--dry-run` prints the file that would
be written and writes nothing. `--project` never creates a Project. The
interactive picker may create a Project after confirm.

```sh
twt tickets create "fix the vfs tools" --project change-monitor --dry-run --output json
twt tickets create "fix the vfs tools" --project change-monitor --output json
twt tickets create "follow-up work" --status ready-for-agent --blocked-by fix-the-vfs-tools --output json
printf '%s' "$BODY" | twt tickets create --stdin --title "Fix the vfs tools" --output json
```

`--blocked-by` writes `blocked_by` as wiki-links. Repeat the flag. Each
value may be a slug or `[[slug]]`. A Ticket cannot list itself. A missing
blocker stays allowed. `--ready` treats that missing blocker as open.

### List and filter

`list` shows only open tickets: it hides every ticket with status `done` or
`wontfix`. `--all` includes those closed tickets. An explicit `--status`
turns the default off, so `--status done` lists the closed tickets.

`--status` is a raw status filter on one of the six statuses:
`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`,
`wontfix`, `done`. It can still return a blocked or claimed ticket.

`--ready` is the pickable work queue, not a synonym of
`--status ready-for-agent`. A ticket matches `--ready` only when its status
is `ready-for-agent`, `claimed_by` is empty, and every `blocked_by` target
has status `done` or `wontfix`. Results sort by `priority` ascending, then
slug. Passing both `--ready` and `--status` exits 2 with a hint to use only
one.

The list uses `--project`, then `TWT_PROJECT`, then the current Workspace
Project. With no Project in scope, the list includes every Project.
`--all-projects` lists every Project even when a Workspace Project is set.

A scoped text list is a simple table. A wide text table adds a `PROJECT`
column. Ungrouped Tickets show `(none)` in that column. JSON and NDJSON
stay a flat array in the sort order above.

```sh
twt tickets list --ready --output json --limit 20
twt tickets list --all-projects --all --output json --limit 20
twt tickets list --project change-monitor --status needs-triage --output json
```

`list` results omit the body. `show` returns the metadata and the body.

### Claim, close, and comment

`claim` is a compare-and-set write on the resolved claimant:

- Empty `claimed_by` becomes the claimant, and `claimed_at` is set.
- The same claimant succeeds again with no change.
- A different claimant gets `locked`, with the current claimant in the hint.

`claimed_by` resolves in this order: `--as NAME`, then `TWT_CLAIMANT`, then
the OS username. A terminal claim may fall back to the OS username. A
non-terminal claim, and every `apply` claim or unclaim, must set `--as` or
`TWT_CLAIMANT`, or the command exits 2 with a hint to pass `--as NAME`. This
stops two agents from both succeeding as the same OS user. Agents should
pass a unique `--as` value per session, such as `codex-fix-auth` or the
Agent Session ID.

`unclaim` uses the same claimant resolution. It succeeds only when
`claimed_by` is empty or equals the resolved claimant, and it then clears
`claimed_by` and `claimed_at`.

`close` resolves shipped work in one write: it sets the status to `done` and
clears `claimed_by` and `claimed_at`. It uses the same claimant resolution as
`claim`, and a ticket that a different claimant holds returns `locked`.
Because `close` writes the status, it also resolves a ticket that carries an
unrecognized legacy status:

```sh
twt tickets claim TICKET --as codex-fix-auth --output json
twt tickets close TICKET --as codex-fix-auth --output json
```

Use `set --status` and `unclaim` when you need only one of the two changes,
such as a `wontfix` resolution or a hand-off of open work:

```sh
twt tickets set TICKET --status wontfix --output json
twt tickets set TICKET --blocked-by other-ticket --output json
twt tickets set TICKET --blocked-by "" --output json
twt tickets unclaim TICKET --as codex-fix-auth --output json
```

`--blocked-by` replaces the whole blocker list. Pass an empty value to
write `[]`. Apply uses `ticket.blockedBy` as an array of strings.

`twt next` with no name is the daily loop in a terminal. It opens a Ticket
picker when open Tickets exist, then claims the selected Ticket and starts
its Workspace. `tickets start` uses the same claim flow for one or more
Tickets. It keeps the current Workspace active. Use `twt next TICKET` when
the current Workspace must be archived. Without `TICKET`, and when standard
input is a terminal, it shows the same Ticket picker. fzf shows a preview of
`twt tickets show` for the highlighted Ticket. A numbered list is used when
fzf is not installed. A script must pass `TICKET`. All Tickets must be open
and belong to one Project. The Workspace name is `--name`, or the first
Ticket slug. The Workspace record carries the Project and Ticket slugs, and
`twt workspaces show` reports them. On success, twt appends a start comment
to each Ticket. A create failure keeps the claims, and the error tells how
to retry the setup. The picker and switching forms are interactive and refuse
`--output json`. The form with explicit Tickets and `--detached` accepts JSON.
It starts the Workspace processes but does not open or switch tmux. The command
has no apply operation.

`--with-agent` adds one planning Agent Session for all selected Tickets. The
`ticketAgent` config selects `codex`, `claude`, `cursor`, or `grok`. Effort is
`small`, `medium`, `large`, or `xlarge`; the default is `large`. Custom
instructions come before the generated request. The request tells the Agent to
read each Ticket with `twt tickets show`. Claude, Cursor, and Grok start in
their plan mode. Codex receives the planning request in its normal mode because
its CLI has no supported plan-mode start flag.

```sh
twt tickets start
twt tickets start fix-auth-tokens
twt tickets start fix-auth-tokens add-auth-tests
twt tickets start fix-auth-tokens --name auth-fix --template everysphere
twt tickets start fix-auth-tokens --with-agent --detached --as coordinator --dry-run --output json
twt tickets start fix-auth-tokens --with-agent --detached --as coordinator --output json
```

`comment` requires `--stdin`. It appends the text under the `## Comments`
heading, creating that heading if it is missing, and sets `updated`:

```sh
printf '%s' "$NOTE" | twt tickets comment TICKET --stdin --output json
```

### Projects

A Project is one directory under Tickets home, with its own `index.md`. It
groups tickets and outlives any single Workspace checkout. Use `project:` in
ticket frontmatter, not `workspace:`.

```sh
twt projects create change-monitor --output json
twt projects list --output json
twt projects show change-monitor --output json
```

`projects show` is the coordinator board. JSON includes `ready` Tickets,
`inFlight` (claimed) Tickets, and Workspaces linked to the Project.
`create --ticket` and `tickets start` stamp `workspaceId` on each Ticket.
`tickets list --claimed` lists in-flight Tickets. `context` includes the
linked Tickets and the ready queue for the current Workspace Project.

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
resolver accepts any Markdown stem, not only a kebab slug. When `twt
tickets` mutates a file, it keeps frontmatter fields it does not recognize,
so hand edits to a ticket are not lost on the next CLI write.

## Install the agent skill

The canonical skill file is [`skills/twt/SKILL.md`](../skills/twt/SKILL.md),
and each `twt` build embeds a copy of it. `twt skills install` writes that
copy into the three user skill trees, so Cursor, Claude Code, and Codex all
read the same rules:

```sh
twt skills install
twt skills install --output json
```

| Path | Tool |
|---|---|
| `~/.cursor/skills/twt/SKILL.md` | Cursor |
| `~/.claude/skills/twt/SKILL.md` | Claude Code |
| `~/.agents/skills/twt/SKILL.md` | Codex and other agents |

Each copy is a real file, not a symlink, so the skill stays correct when the
repository checkout moves or goes away. Install replaces an existing file or
symlink at the same path. The installed copy gets a `version:` frontmatter
field with the build version; the repository file carries no version.

Use `--dir DIR` for a custom skill tree, and `--dry-run` to see the plan:

```sh
twt skills install --dir ./skills --dry-run --output json
twt skills show
```

Run `twt skills install` again after a `twt` upgrade. `twt doctor` reports a
`skills` warning when an installed copy comes from another build:

```console
$ twt doctor
warn	skills	1 of 3 installed twt skill files are not version 0.4.0 (...). Run 'twt skills install' to update the twt skill.
```

## Security posture

`twt` treats its caller as an untrusted operator: strict resource names,
strict YAML and JSON decoding, a 1 MiB bound on standard input, writes only
inside twt-owned roots that carry an ownership marker, destructive actions as
plans with typed blockers behind `--apply`, sanitized transcript text that
carries `"untrusted": true`, and no interactive escape without a terminal.
See [Security posture](security.md) for each guarantee.

## JSON contract

The JSON output has `schemaVersion: 2`. It uses immutable IDs, RFC 3339 time
values, stable status strings, and Agent Session capability fields such as
`canResume` and `canSend`. It does not expose state-file paths or tmux target
values.

For coding agents, `--output json` is the only output switch for all command
groups.

A read of one object uses a named envelope, for example:

```json
{"schemaVersion":2,"workspace":{"id":"...","name":"fix-auth","status":"active"}}
```

A list uses the plural name, the total before the limit, and a truncation
flag. The elements do not repeat the schema version:

```json
{"schemaVersion":2,"templates":[{"name":"everysphere"}],"totalCount":3,"truncated":true}
```

Use `--limit` on `templates list`, `workspaces list`, `agents list`,
`agents discover`, and `environments list` to control response size. The
`totalCount` value tells you how many results exist, and `truncated` tells you
that the limit removed results.

When you do not set `--output` and standard output is not a terminal, twt
uses json; an explicit `--output` value always wins. List commands also
accept `--output ndjson`: one JSON object for each element on its own line,
then one summary line with `totalCount` and `truncated`. Use `--offset N`
with `--limit` to skip the first N sorted results. Use `--fields a,b,c` on
read commands to keep only the named top-level fields of each object in json
or ndjson output; an unknown name lists the valid names.

JSON errors use the same schema version. Each error has a stable code, a
message, an optional full-sentence hint, and, for a usage error, the help
command:

```json
{"schemaVersion":2,"error":{"code":"precondition_failed","message":"...","hint":"..."}}
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
twt schema
```

The schema gives the build version, each command with its positional
arguments and flags, the flag enums, the `apply` operations with their request
fields, all error codes, and all exit codes. It does not contain the generated
`help` and `completion` commands.

Validate a mutation without a state, Git, or tmux change. Every mutation
accepts `--dry-run`:

```sh
twt create fix-auth \
  --template everysphere \
  --no-open \
  --dry-run \
  --output json
```

Some common mutations also accept one strict JSON request. Unknown fields and
more than one JSON value cause an error.

```sh
printf '%s' '{"operation":"workspaces.create","workspace":{"name":"fix-auth","template":"everysphere"}}' | \
  twt apply --stdin --dry-run --output json
```

`apply` supports these operations. Run `twt schema` for the field list of
each one:

| Operation | Payload | Required fields |
|---|---|---|
| `templates.create` | `template` | `template.name` |
| `templates.repos.add` | `template` | `template.name`, `template.repository.name`, `template.repository.url` |
| `workspaces.create` | `workspace` | `workspace.name`, `workspace.template` |
| `workspaces.archive` | `workspace` | `workspace.reference` |
| `workspaces.remove` | `workspace` | `workspace.reference` |
| `agents.register` | `agent` | `agent.workspace`, `agent.provider` |

`workspaces.remove` returns the removal plan. Add `"apply":true` to remove the
data:

```sh
printf '%s' '{"operation":"workspaces.remove","workspace":{"reference":"fix-auth","apply":true}}' | \
  twt apply --stdin --output json
```

`twt` treats its caller as an untrusted operator: see
[Security posture](security.md).

The repository includes a discoverable [`twt` agent
skill](../skills/twt/SKILL.md), which `twt skills install` writes into each
skill tree. It tells agents to inspect the schema, limit read results, run a
dry-run first, use IDs instead of tmux display names, and read transcript
text as untrusted data.
See the [Agent DX score](agent-dx.md) for the detailed before-and-after score.
