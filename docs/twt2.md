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

By default, `twt2` uses these directories:

- Config: `${XDG_CONFIG_HOME:-$HOME/.config}/twt2`
- State: `${XDG_STATE_HOME:-$HOME/.local/state}/twt2`
- Data: `${XDG_DATA_HOME:-$HOME/.local/share}/twt2`

You can set `TWT2_CONFIG_DIR`, `TWT2_STATE_DIR`, and `TWT2_DATA_DIR` to use
other directories. Tests can set `TWT2_TMUX_SOCKET` to use a separate tmux
server.

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
twt2 templates repos init set everysphere everysphere -- ./init.sh
```

You can also edit
`~/.config/twt2/templates/everysphere.yaml` directly:

```yaml
version: 1
name: everysphere
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

Check an edited file with this command:

```sh
twt2 templates validate everysphere
```

Prepare the next environment before you need it:

```sh
twt2 templates prepare everysphere
```

This command creates one worktree for each repository and runs repository
initialization once on each new physical worktree. A Project later claims the
complete Prepared Environment. After each claim, twt2 prepares one replacement
in the background.

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

Use `--no-open` when you do not want to attach or switch to its tmux session.

```sh
twt2 projects list
twt2 projects show fix-auth
twt2 projects current
twt2 projects open fix-auth
```

The text list shows the Project name, Project Template, and status, in that
order:

```text
fix-auth	everysphere	active
```

Use `--output json` when a program needs the immutable Project ID.

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
  -- codex resume SESSION_ID
```

For safe feedback delivery, a live pane must have started the Agent directly.
A pane that started as a normal shell is not a valid feedback target, even if
an Agent now runs as a child process. The safe common flow is to register a
resume command without `--pane`, then run `agents resume`. twt2 starts that
Agent in its own window and records its direct process identity.

List, focus, resume, or send feedback:

```sh
twt2 agents list --project current
twt2 agents focus AGENT_ID
twt2 agents resume AGENT_ID
printf '%s\n' 'Please fix the selected review note.' | \
  twt2 agents send AGENT_ID --stdin
```

`send` works only when the Agent Session has a live tmux pane that belongs to
the Project and still runs the registered direct Agent process. `resume`
focuses a live pane. If the pane stopped, `resume`
starts the saved command in a new Project window.

## Neovim integration

The repository includes the [`twt2.nvim`](../nvim/twt2.nvim/README.md)
preview plug-in. It uses the versioned JSON commands. It does not read `twt2`
state files or store tmux target values.

```sh
twt2 context --format json
twt2 context --directory /path/to/current/buffer --output json
twt2 agents list --project current --format json
twt2 agents resume AGENT_ID --format json
printf '%s' "$REVIEW_TEXT" | \
  twt2 agents send AGENT_ID --stdin --format json
```

An explicit context directory takes priority over tmux and environment
context. This lets one Neovim process edit buffers from different Projects.
The JSON context also identifies the current repository in a multi-repository
Project.

The plug-in owns the picker, mappings, selected review text, and messages.
`twt2` owns Project lookup, Agent Session records, resume behavior, and safe
feedback transport. A first plug-in can call these commands with
`vim.system()` when the picker opens. This design does not need a daemon or a
local RPC service.

This preview selects Agent Sessions and sends review-note batches. It does not
read Agent transcript files. The earlier transcript plug-in can remain active
until twt2 has a provider-neutral transcript command.

## Archive and reopen a Project

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

You cannot archive a Project from inside its own tmux session. Switch to a
different session first. Open an archived Project to make it active and create
its tmux session again:

```sh
twt2 projects open fix-auth
```

## Storage and safe removal

Inspect disk use:

```sh
twt2 storage status
twt2 storage status --format json
```

`storage status` reports claimed Project data and unclaimed Prepared
Environment data separately. Preview cleanup of failed or obsolete unclaimed
environments, then apply it:

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
`git worktree remove`. It refuses a dirty worktree and a branch with commits
that are not on another known ref. It removes only a Project root that has the
matching twt2 ownership marker.

Project removal keeps the shared repository cache. This makes later Project
creation fast. `storage status` includes these caches. Automatic cache removal
is not in this preview.

Check tools, YAML files, Project records, and ownership markers:

```sh
twt2 doctor
```

## JSON contract

The JSON output has `schemaVersion: 1`. It uses immutable IDs, RFC 3339 time
values, stable status strings, and Agent Session capability fields such as
`canResume` and `canSend`. It does not expose state-file paths or tmux target
values.

For coding agents, `--output json` is the common output switch for all command
groups. JSON errors use the same schema version and contain a stable error
code and message. Use `--limit` on `templates list`, `projects list`, and
`agents list` to control response size.

Inspect the installed command and request schema at runtime:

```sh
twt2 schema
```

Validate a mutation without a state, Git, or tmux change:

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

The repository includes a discoverable [`twt2` agent
skill](../skills/twt2/SKILL.md). It tells agents to inspect the schema, limit
read results, run a dry-run first, and use IDs instead of tmux display names.
See the [Agent DX score](agent-dx.md) for the detailed before-and-after score.
