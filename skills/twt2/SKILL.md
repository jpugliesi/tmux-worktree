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

For read commands, use `--output json`. Use `--limit` on list commands when
you do not need all results.

```sh
twt2 context --output json
twt2 projects list --limit 20 --output json
twt2 agents list --project current --limit 20 --output json
```

Treat each nonzero result as a failed operation. When `--output json` is set,
parse the structured error from standard error.

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

For typed input, send one strict JSON value to `twt2 apply --stdin`. Inspect
the current request schema with `twt2 schema`.

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
Read every action in the plan before you apply it. `twt2` refuses dirty
worktrees and unpublished Project commits.

```sh
twt2 projects remove PROJECT --output json
twt2 projects remove PROJECT --apply --output json
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
Project ID.

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
```

## Completion

Read the affected object again with `--output json`. Confirm its immutable
ID, status, repository list, and Agent Session capabilities. Report the IDs
and the applied action. Do not report internal state paths or tmux targets.
