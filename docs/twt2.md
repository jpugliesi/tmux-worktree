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

From the Project tmux session, create the next Project and archive the current
Project:

```sh
twt2 create fix-logout
```

For an interactive prompt, omit the name:

```sh
twt2 create
Project name: fix-logout
```

The command uses the latest saved version of the same Project Template. It
claims a matching Prepared Environment, switches the calling tmux client to
the new Project, and archives `fix-auth`. Other tmux clients do not switch.
Preparation of the replacement environment continues in the background.

`twt2 create` finds the current Project from the current directory, the
`TWT2_PROJECT_ID` value, or the current tmux pane. The tmux client switch
needs `TMUX_PANE`. From a plain shell inside a worktree, `twt2 create` uses
the Project Template of the current Project, creates the new Project, attaches
its session, and keeps the current Project active.

If creation or setup fails, the current Project stays active. `twt2` keeps a
Project that has a setup failure. You can inspect it and run
`twt2 projects setup retry PROJECT`. If the tmux switch fails, `twt2` archives
the new Project and keeps the current Project active.

The short command is for a person in tmux. For a script or coding agent, use
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

The text list shows the Project name, Project Template, status, age, and size,
in that order:

```text
fix-auth	everysphere	active	2h	1.2 GiB
```

Use `--output json` when a program needs the immutable Project ID.

Each command that takes a PROJECT argument also accepts the literal value
`current`. `twt2` then uses the current directory, the `TWT2_PROJECT_ID`
value, or the current tmux pane:

```sh
twt2 projects show current --output json
twt2 projects archive current
twt2 projects setup retry current
twt2 finish current
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

## Archive, finish, and reopen a Project

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

To archive a Project and remove its data in one step, use `finish`:

```sh
twt2 finish
twt2 finish fix-auth
twt2 finish fix-auth --keep
twt2 finish fix-auth --dry-run --output json
```

`finish` archives the Project, then applies the removal plan. `--keep` stops
after the archive. `--allow-unpublished` removes a branch that has commits
which are not on another known ref.

From inside the Project tmux session, `finish` moves your tmux client to the
most recent other active Project, or detaches the client, and a worker window
completes the work. This flow uses text output. For JSON output, run `finish`
from a different session. A dry run reports the archive and the complete
removal plan and changes nothing.

Open an archived Project to make it active and create its tmux session again:

```sh
twt2 projects open fix-auth
twt2 projects open fix-auth --no-attach
```

You cannot archive a Project from inside its own tmux session with
`projects archive`. Switch to a different session first, or use `finish`.

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
