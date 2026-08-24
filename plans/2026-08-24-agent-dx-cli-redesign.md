# twt CLI Contract and Agent DX Redesign

Date: 2026-08-24

Status: Planned

Tracking: This is a local implementation plan. No external issue was requested or created.

## Goal

Make `twt` an excellent CLI for people and coding agents.

A person must keep short commands, text output, shell completion, safe plans,
TTY pickers, and bare path output for shell pipelines. An agent must get an
exact machine contract, complete raw mutations, bounded reads, typed plans,
safe retry behavior, and errors that it can act on.

This is a breaking redesign. It does not preserve version 1 command or JSON
contracts. It must update all consumers in this repository in the same change.

## Expected impact

- Expected caller errors will no longer report `internal`.
- Each non-interactive mutation will have one typed raw operation.
- Flag commands and raw operations will use the same action implementation.
- `twt schema` will return small, focused JSON Schema documents.
- Dry runs will return the normalized request and a typed plan.
- Structured list output will have a safe default limit.
- Retries with an idempotency key will not repeat an action after an unknown
  outcome.
- Human help will show only flags and output modes that the command accepts.
- The embedded agent skill will become a small package with reference files.
- The Neovim client will use the new contract.

The target strict Agent DX score is 20/21. Semantic prompt filtering stays out
of scope, so Safety Rails stays at 2 under the strict scale.

## Assumptions and open questions

- The user accepts all CLI and JSON contract breaks in this plan.
- Existing persisted user data must stay safe and readable.
- Go 1.23, Git, tmux, and Neovim 0.10 remain the supported local tools.
- The repository will release the CLI and Neovim consumer changes together.
- No open design question blocks implementation. If implementation evidence
  disproves an assumption, stop that slice and update this plan before work
  continues.

## Fixed decisions

1. Do not keep the version 1 CLI contract.
2. Use `contractVersion: 2` for the twt JSON contract. Use the JSON Schema
   `$schema` field for the JSON Schema draft. Do not call both values a schema
   version.
3. Keep persisted Workspaces, Workspace Templates, Prepared Environments,
   Agent Sessions, Tickets, and Projects unchanged. The breaking change applies
   to the CLI interface, not user data.
4. Keep human help text and live completion close to each Cobra command. Do not
   generate all human help from a central table.
5. Use a command capability catalog for the machine contract, global flag
   validation, and coverage tests.
6. Model capabilities as independent effects. A command variant can read,
   write, use a terminal, use tmux, move a tmux client, or replace a process.
   Do not force each command into one `read`, `mutation`, or `interactive`
   value.
7. Give raw input semantic parity. Adapter-only flags such as `--stdin`,
   `--no-open`, `--no-attach`, and `--apply` do not belong in a raw request.
8. Use 50 as the default result limit for structured, non-TTY list output.
   `--limit N` selects another positive limit. `--all` explicitly requests all
   results. TTY text lists continue to show all results by default.
9. Keep offset pagination. Do not add cursors until a real changing-list use
   case needs them.
10. Keep bare text as the automatic output for scalar pipeline commands such
    as `workspaces path` and `templates path`. Explicit `--output json` returns a
    typed JSON object.
11. Require a plan digest when an agent applies a destructive plan. Simple
    mutations do not need a plan digest.
12. Idempotency does not promise exactly-once execution. A call that can have
    changed an external process before a crash returns `outcome_unknown`. It
    does not run again automatically.
13. Do not add a semantic prompt filter or a model dependency. Keep structural
    transcript sanitization, trust markers, and agent guidance.
14. Do not add MCP in this change. MCP is a separate follow-up after the CLI
    action interface is stable.
15. Use `twt create` to create a Workspace without archiving another Workspace.
    Use `twt next` to create the next Workspace, switch to it, and archive the
    current Workspace. `twt start` is not part of the new command contract.

## New public contract

### Raw request

All raw operations use one envelope:

```json
{
  "contractVersion": 2,
  "operation": "tickets.create",
  "idempotencyKey": "job-123",
  "expectedPlanDigest": "sha256:optional-for-destructive-actions",
  "input": {}
}
```

Rules:

- `operation` selects one typed action.
- `input` has the exact input schema for that action.
- `idempotencyKey` is optional. It is valid only on a real apply.
- `expectedPlanDigest` is required for a destructive apply.
- `twt apply --stdin --dry-run` builds a plan. A real `twt apply --stdin`
  applies the action.
- Remove nested booleans such as `workspace.apply` and `storage.apply`.

### Mutation result

```json
{
  "contractVersion": 2,
  "operation": "tickets.create",
  "status": "planned",
  "planDigest": "sha256:...",
  "plan": {}
}
```

Applied and replayed results use `status: "applied"` or `status: "replayed"`
and a typed `result` value. An applied result can include a short plan summary,
but it must not repeat a large plan by default.

### Error result

```json
{
  "contractVersion": 2,
  "error": {
    "code": "invalid_usage",
    "message": "ticket.as is required",
    "retryable": false,
    "details": {
      "path": "ticket.as",
      "reason": "required"
    },
    "helpCommand": "twt schema operation tickets.claim"
  }
}
```

Keep the current specific codes. Add typed details for invalid fields, allowed
values, ambiguous references, lock retry advice, blockers, partial results,
idempotency conflicts, and unknown outcomes.

Use this classification:

| Condition | Code and exit |
| --- | --- |
| Bad JSON, unknown field, missing field, bad enum, bad name, or bad flag | `invalid_usage`, 2 |
| Missing resource | `not_found`, 3 |
| Existing resource conflict | `already_exists`, 3 |
| State does not permit the action | `precondition_failed`, 3 |
| Concurrent owner or mutation | `locked`, 3 |
| Unsafe stored state or unknown outcome | `unsafe_state` or `outcome_unknown`, 3 |
| I/O failure, failed child process, or program defect | `internal`, 1 |

Do not classify every returned `error` at the root. Classify caller input at
the CLI and raw adapters. Classify corrupt stored state at the store seam.
This prevents a bad state file from looking like a bad user flag.

### Read result and NDJSON

Object reads keep a named JSON envelope. List JSON keeps a named array and a
`page` object with `offset`, `limit`, `totalCount`, and `truncated`.

NDJSON uses typed lines:

```json
{"contractVersion":2,"type":"item","item":{}}
{"contractVersion":2,"type":"summary","page":{"offset":0,"limit":50,"totalCount":80,"truncated":true}}
```

`--fields` applies inside `item`. It never removes `contractVersion`, `type`,
or the summary. Add nested field paths such as
`repositories.name` where the output has nested objects or arrays.

### Focused schema queries

Use these forms:

```sh
twt schema
twt schema command workspaces.create
twt schema operation workspaces.create
twt schema output workspaces.list
twt schema errors
```

The plain `twt schema` result is a compact index. A focused query returns the
command capabilities and JSON Schema documents for its input, plan, result,
read output, or error details. The schema must include:

- Canonical command and operation IDs
- Positional arguments and flag constraints
- Effects and their conditions
- Interaction and tmux requirements
- Output kind and allowed output modes
- Valid `--fields` paths
- Default list limit
- Raw operation support or a deliberate exclusion reason
- Input, plan, result, and error JSON Schema references

## Deep module design

Create a typed action module before full raw parity. Its interface is the test
surface. Cobra flag commands and `apply` are adapters at this seam.

Each action has:

- A typed input
- A pure `Plan` operation
- A typed plan with stable action and blocker types
- An `Execute` operation
- A typed result
- Input, plan, and result schemas
- Idempotency and destructive-plan policy

The action implementation must not accept `*cobra.Command` and must not write
output. It can use the existing Workspace, Ticket, Agent Session, maintenance,
and store modules. It receives dependencies that vary, such as the clock, ID
source, filesystem, Git runner, tmux runner, editor, provider store, and worker
launcher. Do not add a port when production and tests do not need different
adapters.

Use a generic registration helper to keep typed action code small. The raw
executor can use an erased action interface after registration. Flag adapters
can keep typed access to the same action. This gives one implementation for
planning and execution without exposing raw JSON to domain modules.

The command capability module must be deep. It owns capability declaration,
machine-schema generation, output-mode validation, default list limits, and
coverage checks. It does not own human prose or live resource completion.

## Raw operation scope

Create a checked inclusion and exclusion matrix before migration. The first
matrix must include these cases.

### Required raw operations

- Workspace Templates: create from a full structured Workspace Template, remove,
  prepare, set initialization, add a Repository Specification, and remove a
  Repository Specification.
- Workspaces: create with branch and fetch policy, adopt an explicit tmux
  session, ensure a session without attaching, retry setup, archive, cancel
  removal, plan and apply one removal, and plan and apply bulk removal.
- Workspace completion: plan and execute the non-terminal core of `done`. The
  request must state the Ticket-close choice and all safety choices.
- Tickets: initialize Tickets home, create, edit, set, claim, unclaim, close,
  comment, create a Project, and start a Workspace from an explicit Ticket without
  moving a tmux client.
- Agent Sessions: register with an explicit pane or resume command, adopt
  discovered sessions, send, resume in an owned pane, remove, link a
  transcript, and write a Transcript Snapshot.
- Storage: plan and apply cleanup.
- Skills: install to known user skill roots or to validated relative
  directories below the current directory.

### Deliberate raw exclusions

- TTY pickers and numbered prompts
- `$VISUAL` or `$EDITOR`
- `switch` and other tmux client moves
- `agents focus`
- `agents open` and process replacement
- Implicit `current` pane values when a stable ID can be required
- Arbitrary absolute skill installation paths
- Human confirmation prompts

Split `agents discover --adopt` into a pure read and an explicit write command.
A read such as `agents show` or transcript show must not register an Agent
Session as a hidden side effect. Mutations can include adoption in their typed
plan when the action needs a stored Agent Session.

## Idempotency and retry safety

Add a shared raw-executor ledger under the state directory. Use a digest of
the idempotency key for the filename. Each record has mode `in_progress`,
`completed`, or `outcome_unknown`.

Rules:

1. Validate and canonicalize the request before the ledger changes.
2. Store a hash of the canonical request. Do not store the full request body.
3. Lock one idempotency key before its record is read or changed.
4. Reject the same key with a different request hash.
5. Write `in_progress` before the first effect.
6. Write the small typed result and `completed` after success.
7. Return the stored result for a completed repeat.
8. If a process stops after an effect can have occurred, mark or infer
   `outcome_unknown`. Do not repeat a non-repeatable effect.
9. Add operation-specific reconciliation only where stored state proves the
   result.
10. Use mode `0600`, bound record and result size, and do not store transcript
    or message text.
11. Keep records for 30 days. Add expired-record cleanup to `storage clean`
    and a health check to `doctor`.
12. Dry runs do not create ledger records.

This design prevents unsafe automatic repeats. It does not claim exactly-once
behavior across files, Git, tmux, or provider processes.

## Dry-run plan rules

- Planning must not change files, Git configuration, tmux state, provider
  state, skill trees, or the idempotency ledger.
- Planning must not start a background worker or an editor.
- Plans use stable action kinds, blocker codes, warning codes, and redaction
  rules.
- Plans do not expose provider transcript paths, internal provider paths,
  credentials, or raw message text.
- Bulk plans have one entry for each target and explicit partial-result rules.
- A destructive plan has a digest over its canonical action and blocker data.
- A destructive apply with an old digest fails with a typed stale-plan error.
- Execution checks safety conditions again under the correct mutation lock.

Audit current planners for hidden writes. In particular, verify every Git
helper that a removal plan calls.

## Implementation plan

Use red-green-refactor for each vertical slice. Do not write all tests first
and all implementation later.

### 1. Inventory commands and make the new contract fail

RED:

- Add a table-driven inventory test for every runnable Cobra command and each
  conditional variant.
- Record command ID, arguments, effects, interaction, output kind, output
  modes, fields, list policy, raw-operation policy, and exclusion reason.
- Add failing tests for the known contract defects: invalid JSON reported as
  `internal`, missing `--fields` values, false NDJSON support, ignored dry-run
  on reads, missing raw mutations, and incomplete `workspaces.create` input.
- Add the scalar commands and the Neovim client to the consumer inventory.

GREEN:

- Add only the capability types and declarations needed to make the inventory
  compile. Keep the new behavior failing for later slices.

Refactor:

- Remove duplicate hand lists where the inventory is now the source of truth.

### 2. Correct caller-error classification

RED:

- Add built-binary tests for malformed JSON, an unknown top-level field, an
  unknown action field, a missing field, an invalid name, an invalid enum, a
  missing resource, a held lock, unsafe stored state, and a real internal
  failure.
- Assert stdout, stderr JSON, error details, and process exit code together.
- Add fuzz tests for strict raw decoding. A malformed request must not panic
  and must not return `internal` unless the injected reader fails.

GREEN:

- Add typed caller-input errors and structured details in `internal/clierr`.
- Classify strict decode and validation failures at the correct adapter seam.
- Keep I/O, child-process, and unexpected failures as `internal`.

Refactor:

- Replace repeated decode and required-field checks with one bounded strict
  decoder that reports a JSON path and reason.

### 3. Build one typed action slice with `tickets.create`

RED:

- Test typed input normalization, dry-run purity, the typed plan, execution,
  and the typed result through the action interface.
- Test that the flag adapter and raw adapter produce the same plan and result
  after adapter-only fields are removed.
- Test `--priority` on `tickets create`.

GREEN:

- Add the action interface and the first typed action.
- Make the flag command and raw executor adapters over that action.
- Add the version 2 request, plan, result, and error envelopes for this slice.

Refactor:

- Move no Ticket domain logic into Cobra or raw JSON code.
- Keep the action interface small enough for tests and both adapters.

### 4. Add the capability catalog and focused schema

RED:

- Test the compact schema index and each focused query.
- Test valid fields, allowed output modes, scalar output, effects, exclusions,
  and action schema references.
- Validate emitted JSON Schema documents against their meta-schema.
- Validate all embedded request and response examples against the emitted
  schemas.

GREEN:

- Implement command declarations and focused queries.
- Generate input, plan, result, and output schemas from typed Go values and
  explicit schema tags. Use one maintained JSON Schema generator dependency;
  wrap it behind the contract module.
- Remove the old 60 KB all-in-one schema response.

Refactor:

- Keep long help and live completion in the command files.
- Add agreement tests between help flags, completion enums, and capabilities.

### 5. Replace output and context controls

RED:

- Add tests for typed NDJSON item and summary records.
- Add tests that field masks cannot remove discriminators or page data.
- Add nested field-mask tests for objects and arrays.
- Add TTY and non-TTY list tests for the 50-item default, `--limit`, `--all`,
  `totalCount`, and `truncated`.
- Add pipeline and command-substitution tests for scalar path commands.
- Test explicit JSON for scalar commands.

GREEN:

- Replace the reflection-and-map output masking with a typed projection module.
- Add per-command output flags. Do not show NDJSON on non-list commands.
- Add dry-run only to command variants that can write.
- Preserve automatic text for scalar pipelines and automatic JSON for object
  and list reads outside a terminal.

Refactor:

- Delete old output helpers after all migrated callers use the new module.

### 6. Migrate all actions by domain

Migrate in this order:

1. Tickets and Projects
2. Workspace Templates and Repository Specifications
3. Agent Sessions and transcripts
4. Workspaces and Workspace completion
5. Storage cleanup
6. Skill installation

For each action, repeat this vertical slice:

1. RED: Add action tests for normalize, plan, execute, and no-side-effect
   dry-run behavior.
2. RED: Add flag-versus-raw semantic parity tests.
3. RED: Add schema and capability coverage tests.
4. GREEN: Move orchestration behind the action interface.
5. GREEN: Add the flag and raw adapters.
6. REFACTOR: Remove the old command-bound helper and duplicate request type.

Add explicit tests for bulk and partial operations. Split hidden read/write
modes where the inventory requires it. Do not let a read adopt an Agent
Session.

### 7. Complete typed plans and destructive plan digests

RED:

- Snapshot the filesystem, Git configuration, tmux state, provider state,
  workers, and skill roots before each plan. Assert that they are unchanged.
- Test stable action kinds, blocker codes, warning codes, redaction, bulk
  entries, and plan digests.
- Test that a changed destructive plan rejects an old digest.

GREEN:

- Add complete typed plans to each action.
- Move side-effect-free inspection behind injected adapters where tests need a
  replacement.
- Require a matching digest for destructive raw applies.

Refactor:

- Remove simple `status: valid` mutation output after all actions return plans.

### 8. Add idempotency to the shared raw executor

RED:

- Test two concurrent calls with one key.
- Test one key with two request hashes.
- Test completed replay.
- Inject stops before the effect, after the effect, and before result storage.
- Test `outcome_unknown`, safe reconciliation, permissions, size limits,
  30-day retention, cleanup, and `doctor` output.
- Run these tests with the race detector.

GREEN:

- Add the ledger store, per-key lock, state transitions, and replay result.
- Connect cleanup to `storage clean` and health checks to `doctor`.

Refactor:

- Keep idempotency policy in the raw executor. Do not add ledger code to each
  action.

### 9. Finish human DX

RED:

- Add help tests that reject irrelevant flags and show valid output modes.
- Add completion tests for enums and live resource values.
- Add TTY tests for pickers, editors, Workspace opening, and current shortcuts.
- Add error-message tests with one clear recovery action.

GREEN:

- Keep short human workflows and aliases.
- Keep text tables and scalar paths.
- Add `tickets create --priority`.
- Remove or split mixed read/write command modes.
- Improve error text from the same typed error details that agents receive.

Refactor:

- Keep human prose local. Do not make the capability catalog a second help
  system.

### 10. Convert the embedded skill to a safe package

RED:

- Test directory embedding, reference validation, package digest, full
  installation, partial-install detection, symlink replacement, and dry-run.
- Test that unknown user files are not removed.
- Test that an unowned target directory is not replaced or deleted.
- Test raw path policy for user roots and current-directory relative roots.

GREEN:

- Split `skills/twt/SKILL.md` into a short main file and focused references for
  the contract, Workspaces, Projects, Agent Sessions, Tickets, and safety.
- Embed the complete package.
- Install known files through a package manifest and ownership marker.
- Make `skills show` and `doctor` report the package version and digest.

Refactor:

- Generate repeated command examples from tested contract fixtures where this
  prevents drift.

### 11. Update every in-repository consumer

- Update `nvim/twt.nvim/lua/twt/client.lua` to require `contractVersion: 2`.
- Update Neovim fixtures and integration tests for new envelopes and errors.
- Update README files, `docs/twt.md`, `docs/security.md`,
  `docs/agent-dx.md`, and `docs/tickets.md`.
- Remove version 1 examples and the false statement that the current raw
  surface covers every non-interactive mutation.
- Update the score with live binary evidence.
- Add release notes that name the breaking command, output, list-limit, and
  Agent Session adoption changes.
- Make the installed skill tell agents to use focused schema queries, explicit
  limits, dry-run plans, destructive plan digests, and idempotency keys.

No `CONTEXT.md` change is required. The existing Workspace, Workspace Template,
Prepared Environment, Agent Session, Ticket, and Project meanings do not change.

### 12. End-to-end verification

There is no localdev stack for this local CLI. Use the built binary, temporary
XDG directories, local Git repositories, an isolated tmux socket, and a PTY
test helper.

Verify these complete workflows:

- Person: create a Ticket in a TTY, set its priority, claim it, create the next
  Workspace, use a picker, and finish the Workspace.
- Agent: inspect focused schemas, list bounded work, plan a Ticket claim,
  apply with an idempotency key, create a Workspace with raw input, and read the
  result.
- Safety: plan a destructive removal, change the state, reject the stale plan,
  plan again, and apply the new digest.
- Retry: stop after a non-repeatable effect and confirm that the next call
  returns `outcome_unknown` without a second effect.
- Scalar shell: use `$(twt workspaces path ...)` and explicit JSON.
- Neovim: open Agent Session data and send a review through the version 2
  contract.

Run:

```sh
gofmt -w <changed-go-files>
go vet ./...
go test ./...
go test -race ./...
nvim/twt.nvim/tests/test.sh
```

Also run a built-binary contract test that checks real exit codes and stderr.
The legacy shell tests under `tests/` exercise `twt-legacy` and are not a gate
for this redesign unless shared code changes.

### 13. Review and land

1. Run the `thermo-nuclear-code-quality-review` skill.
2. Fix all material findings. Pay special attention to a giant capability
   registry, repeated action adapters, and condition growth.
3. Re-run all verification commands.
4. Create one breaking-change PR with small, reviewable commits for each
   vertical slice.
5. In the PR, include the command inventory, JSON examples, contract test
   results, Agent DX score, and release notes.
6. After review comments arrive, use the repository PR-feedback workflow and
   the `fix-pr-comments` skill. Resolve each comment in its own focused commit.
7. Land the PR only after the Go, race, Neovim, and built-binary checks pass.

## Safe parallel work

Do not parallelize the first typed action or the action interface. Stabilize
that seam first.

After it is stable, these work streams can run in parallel:

- Error classification and built-binary tests
- Focused schema and JSON Schema generation
- Output projection, NDJSON, and list limits
- Domain action migrations that change different command files
- Skill package installation
- Neovim and documentation updates after fixtures are fixed

Merge the action migrations before idempotency work. The shared raw executor
must have one stable action interface before it adds retry state.

## Code areas

| Area | Likely changes |
| --- | --- |
| CLI contract | `internal/cli/root.go`, `schema.go`, `output.go`, `usage.go`, `help.go`, `completion.go`, and new contract files or package |
| Raw actions | `internal/cli/apply.go`, command files, and a new typed action package |
| Error taxonomy | `internal/clierr/` and validation adapters in `internal/cli/` |
| Plans | Workspace, Project, Ticket, Agent Session, Template, maintenance, storage, and skill action code |
| Retry ledger | New store files under `internal/store/`, `storage clean`, and `doctor` |
| Skills | `skills/twt/`, `skills/skills.go`, `internal/cli/skills.go`, and maintenance checks |
| Neovim | `nvim/twt.nvim/lua/twt/client.lua` and `nvim/twt.nvim/tests/` |
| Tests | Contract, output, apply, command, store, built-binary, PTY, and Neovim tests |
| Documentation | `README.md`, `docs/twt.md`, `docs/security.md`, `docs/agent-dx.md`, and `docs/tickets.md` |

## Data, UI, and infrastructure implications

- Data: Add versioned idempotency records below the state directory. No
  backfill is required. Cleanup removes expired records only.
- Data safety: Do not migrate or delete existing Workspace, Workspace Template,
  Project, Ticket, or Agent Session records.
- UI: TTY pickers, text tables, and Neovim remain. Neovim must move to contract
  version 2 in the same PR.
- Background work: Planning must not start existing preparation or completion
  workers. Applying can start them through typed actions.
- Infrastructure: No server, database, network service, or authentication
  change is required.
- MCP: No MCP implementation is in this PR.

## Main risks and controls

| Risk | Control |
| --- | --- |
| A central registry becomes a giant shallow module | Store only machine capabilities. Keep action logic, human help, and live completion outside it. Run a strict quality review. |
| Flag and raw behavior drift again | Use the same typed action and add semantic parity tests for every raw-supported action. |
| A dry run changes state | Snapshot all side-effect surfaces and inject adapters for Git, tmux, providers, editors, and workers. |
| An idempotency key gives false exactly-once confidence | Define `outcome_unknown`, record before effects, and never repeat an uncertain action automatically. |
| List limits hide data | Always return `totalCount` and `truncated`. Require `--all` for an unbounded structured read. |
| Scalar JSON changes break shell pipelines | Keep automatic scalar output as bare text. Require explicit JSON for a JSON scalar envelope. |
| Version 2 breaks Neovim | Update the client and all fixtures in the same PR. |
| A multi-file skill install removes user work | Use an owned manifest. Replace known files only. Never delete an unowned directory or unknown file. |
| Raw skill install writes anywhere | Permit known user roots and validated current-directory relative roots only. |
| Destructive state changes after planning | Require a matching plan digest and recheck safety under the mutation lock. |

## Adversarial review notes

The adversarial review changed the initial plan as follows:

- It replaced one exclusive command kind with independent effect dimensions
  and conditional variants.
- It kept human help and live completion near each command instead of making
  the capability catalog own them.
- It changed flag parity to semantic parity and removed adapter-only flags from
  raw input.
- It put the typed action seam before schema, raw parity, dry-run, idempotency,
  and MCP work.
- It replaced an exactly-once claim with completed replay and a typed unknown
  outcome.
- It added plan purity, redaction, partial-result, and stale-plan rules.
- It added a bounded non-TTY list default and typed NDJSON discriminators.
- It added structured error details and built-binary exit tests.
- It found the version 1 check in the Neovim client and added it to the same
  breaking change.
- It protected scalar shell pipelines.
- It expanded the multi-file skill work to include manifests, ownership,
  partial-install checks, and raw path policy.
- It moved MCP to a later, separate phase.

## Completion criteria

The change is complete when:

- Every runnable command and conditional variant has a checked capability
  declaration.
- Every required non-interactive action has one typed raw operation.
- Flag and raw adapters pass semantic parity tests.
- No tested caller error returns `internal`.
- Every dry run passes no-side-effect tests and returns a typed plan.
- Destructive raw actions reject stale plan digests.
- Idempotent repeats replay completed results or return `outcome_unknown`
  without a second unsafe effect.
- Structured lists are bounded by default and state truncation clearly.
- NDJSON lines are self-describing.
- Scalar shell commands remain safe for command substitution.
- The embedded skill package installs and verifies all owned files.
- Neovim and all repository documentation use contract version 2.
- Go, race, built-binary, PTY, and Neovim tests pass.
- The strict quality review has no unresolved material finding.
- The breaking-change PR is reviewed and landed.
