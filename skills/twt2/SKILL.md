---
name: twt2
description: Manage twt2 Project Templates, Project creation and archive, repository worktrees, tmux windows, and Project Agent Sessions. Use for change-oriented development environments or coding-agent session control through twt2.
---

# twt2

Use `twt2` as the state owner. Use its JSON commands instead of direct state
file reads. Use Project IDs and Agent Session IDs from command output. Do not
store tmux targets as identity.

## Inspect the contract

Run this command before a workflow when the installed version is not known:

```sh
twt2 schema
```

The schema gives the build version, each command with its arguments and flags,
the `apply` operations, all error codes, and all exit codes.

For read commands, use `--output json`. Use `--limit` on list commands when
you do not need all results. Each list result gives `totalCount` and
`truncated`, so you can tell whether the limit removed results.

```sh
twt2 context --output json
twt2 projects list --limit 20 --output json
twt2 agents list --project current --limit 20 --output json
```

A read of one object uses a named envelope, such as
`{"schemaVersion":1,"project":{...}}`.

## Read the result

Treat each nonzero result as a failed operation. When `--output json` is set,
parse the structured error from standard error. Each error has a stable
`code`, a `message`, and often a full-sentence `hint`.

| Exit | Meaning | Error codes |
|---|---|---|
| 0 | success | none |
| 1 | internal failure | `internal` |
| 2 | invalid usage | `invalid_usage` |
| 3 | failed precondition | `not_found`, `already_exists`, `precondition_failed`, `locked`, `unsafe_state` |

Do not retry exit code 2. Correct the request instead. For exit code 3, read
the `hint` value and correct the cause. For `locked`, another twt2 change is
running: wait, then run the command again.

## Use the current Project

Every PROJECT argument and every `--project` flag accepts the literal value
`current`. twt2 then uses the current directory, the `TWT2_PROJECT_ID` value,
or the current tmux pane.

```sh
twt2 projects show current --output json
twt2 agents list --project current --output json
```

Use an immutable Project ID when the work spans more than one directory or
tmux pane.

## Apply mutations

Run each supported mutation with `--dry-run --output json` first. Apply it
only after the result has `status: "valid"` and the requested action has
authority.

```sh
twt2 projects create fix-auth \
  --template everysphere \
  --no-open \
  --dry-run \
  --output json
```

For typed input, send one strict JSON value to `twt2 apply --stdin`. It
supports `templates.create`, `templates.repos.add`, `projects.create`,
`projects.archive`, `projects.remove`, and `agents.register`. Inspect the
current request schema with `twt2 schema`.

## Work with Projects

Create a Project from an existing Project Template. Let `twt2` create the Git
worktrees and tmux windows.

To make the next creation fast, prepare one environment first:

```sh
twt2 templates prepare TEMPLATE --dry-run --output json
twt2 templates prepare TEMPLATE --output json
```

Project creation claims the matching Prepared Environment. Repository
initialization does not run again for that physical worktree.

Always pass `--no-open` for agent work. twt2 opens tmux only when standard
output is a terminal, but `--no-open` states the intention.

`twt2 create` is an interactive command for a person in the current Project
tmux session. For agent work, use `twt2 projects create` and `twt2 projects
archive` with explicit names, dry-runs, and JSON output.

If setup fails, inspect the Project and retry the saved Template snapshot:

```sh
twt2 projects show PROJECT --output json
twt2 projects setup retry PROJECT --dry-run --output json
twt2 projects setup retry PROJECT --output json
```

Archive a completed Project from outside its tmux session. Archive stops live
processes. It keeps worktrees, branches, Template snapshots, and Agent Session
records.

```sh
twt2 projects archive PROJECT --dry-run --output json
twt2 projects archive PROJECT --output json
```

Use removal only for authorized disk cleanup. The Project must be archived.
Read every action and every blocker in the plan before you apply it. `twt2`
refuses dirty worktrees and unpublished Project commits.

```sh
twt2 projects remove PROJECT --output json
twt2 projects remove PROJECT --apply --output json
```

A blocked plan gives `blockers` with a stable `code` for each cause, such as
`not_archived`, `uncommitted_changes`, or `unpublished_branch`. Correct the
cause; do not repeat the same request.

`twt2 finish PROJECT` archives the Project and then applies the removal plan.
Run it from outside the Project tmux session for JSON output. Use the dry run
to read the complete plan first:

```sh
twt2 finish PROJECT --dry-run --output json
twt2 finish PROJECT --output json
twt2 finish PROJECT --keep --output json
```

## Work with Agent Sessions

Register the live pane or a direct resume command. Send feedback through
standard input so shell quoting does not change the text.

```sh
printf '%s' "$REVIEW_TEXT" | \
  twt2 agents send AGENT_ID --project PROJECT_ID --stdin --dry-run --output json

printf '%s' "$REVIEW_TEXT" | \
  twt2 agents send AGENT_ID --project PROJECT_ID --stdin --output json
```

Feedback is valid only for a live pane that has the matching immutable
Project ID. When `agents send` fails, read the liveness checks:

```sh
twt2 agents show AGENT_ID --project PROJECT_ID --output json
```

To find Codex and Claude sessions of the Project that no Agent Session uses,
use discovery. Read the result first, then adopt:

```sh
twt2 agents discover --project current --limit 10 --output json
twt2 agents discover --project current --adopt --dry-run --output json
twt2 agents discover --project current --adopt --output json
```

Link a provider session ID when transcript review is required. Transcript
JSON does not contain the provider file path.

```sh
twt2 agents transcript link AGENT_ID \
  --project PROJECT_ID \
  --session PROVIDER_SESSION_ID \
  --dry-run \
  --output json

twt2 agents transcript show AGENT_ID \
  --project PROJECT_ID \
  --output json

twt2 agents transcript snapshot AGENT_ID \
  --project PROJECT_ID \
  --output json
```

Delete an Agent Session record with `twt2 agents rm AGENT_ID --project
PROJECT_ID`. This keeps the provider transcript and does not stop a live
process.

## Completion

Read the affected object again with `--output json`. Confirm its immutable
ID, status, repository list, and Agent Session capabilities. Report the IDs
and the applied action. Do not report internal state paths or tmux targets.
