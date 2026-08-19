---
name: twt2
description: Manage twt2 Project Templates, Projects, repository worktrees, tmux windows, and Project Agent Sessions. Use for change-oriented development environments or coding-agent session control through twt2.
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

If setup fails, inspect the Project and retry the saved Template snapshot:

```sh
twt2 projects show PROJECT --output json
twt2 projects setup retry PROJECT --dry-run --output json
twt2 projects setup retry PROJECT --output json
```

Project removal is a plan by default. Read every action in the plan. Use
`--apply` only when the user authorized removal. `twt2` refuses dirty
worktrees and unpublished Project commits.

## Work with Agent Sessions

Register the live pane or a direct resume command. Send feedback through
standard input so shell quoting does not change the text.

```sh
printf '%s' "$REVIEW_TEXT" | \
  twt2 agents send AGENT_ID --stdin --dry-run --output json

printf '%s' "$REVIEW_TEXT" | \
  twt2 agents send AGENT_ID --stdin --output json
```

Feedback is valid only for a live pane that has the matching immutable
Project ID.

## Completion

Read the affected object again with `--output json`. Confirm its immutable
ID, status, repository list, and Agent Session capabilities. Report the IDs
and the applied action. Do not report internal state paths or tmux targets.
