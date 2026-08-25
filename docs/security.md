# Security posture

The agent is not a trusted operator. `twt` gives a coding agent enough power
to create worktrees, tmux sessions, and files, so every command assumes that
its caller can be confused, prompt-injected, or simply wrong. These are the
guarantees that the CLI keeps.

## Strict input

- **Resource names.** A Workspace Template name, a Workspace name, a Project name,
  and a Ticket slug must match `^[A-Za-z0-9][A-Za-z0-9._-]*$`, and `.` and
  `..` are invalid. A name never carries a path separator, so a name cannot
  walk out of a twt directory.
- **Strict decoding.** Every YAML and JSON document that `twt` reads uses
  strict decoding. An unknown field is an error, and `twt apply` also rejects
  a second JSON value on standard input. A typo fails loudly instead of
  changing a different field.
- **Bounded standard input.** Every command that reads standard input reads
  at most 1 MiB: `twt apply`, `twt templates create --from-stdin`,
  `twt tickets create --stdin`, `twt tickets edit --stdin`,
  `twt tickets comment --stdin`, and `twt agents send --stdin`.
- **Provider session IDs.** A provider session ID must be 3 to 256
  characters, and it may not hold a control character, a path separator, or
  `..`. `twt` never returns the provider file path.

## Bounded writes

`twt` writes only inside the directories that it owns: the config directory,
the state directory, the data directory, the Tickets home, and the skill
trees that `twt skills install` names. A Workspace root carries an ownership
marker, `.twt-owned.json`, and a Prepared Environment root carries
`.twt-environment.json`. Before `twt` deletes a Workspace root, it validates
that marker and the Workspace ID inside it, so a stale record can never delete
a directory that another tool owns. `twt doctor` reports a Workspace or a
Prepared Environment whose marker is missing.

Initialization commands from a Workspace Template run with a working directory
inside the Workspace root. `twt` starts a command directly; it never starts a
shell, so it never expands text from a template into a shell.

## Destructive actions are plans

A destructive command builds a plan and shows it. `twt workspaces remove`,
`twt done`, and `twt storage clean` change nothing without `--apply`. A plan
that cannot run safely returns typed blockers, each with a stable `code`,
such as `not_archived`, `uncommitted_changes`, or `unpublished_branch`. An
agent must read the blockers and correct the cause; it must not repeat the
same request. Every mutation also accepts `--dry-run`, which validates the
request and reports `status: "valid"` without a state, Git, or tmux change.

## Cursor Cloud boundary

`twt tickets dispatch` sends the Ticket body, configured prompt instructions,
repository URLs, and starting refs to Cursor Cloud. The operator must use it
only for content and repositories that Cursor can receive. The command does
not put credentials in its JSON request. The compiled Cursor SDK harness uses
the Cursor authentication that is already available to that process.

The Go CLI starts the harness directly, without a shell. It sends one strict,
versioned JSON value on standard input and limits both standard output and
standard error. The harness also limits its input and rejects unknown fields.
Cloud Session state has mode `0600`. It contains the frozen Ticket prompt and
idempotency keys. Normal CLI output omits those private state fields.

`TWT_CURSOR_CLOUD_HARNESS` can select a different executable. This is an
operator trust decision because the process receives the private dispatch
request and the Cursor authentication environment. Without that setting,
`twt` checks for `twt-cursor-cloud` next to its own executable and then on
`PATH`.

Dispatch saves local state and claims the Ticket before it calls Cursor. An
uncertain network result keeps that claim. This rule prevents an automatic
retry from creating a second Cloud Agent. `cloud-sync` can recover the remote
Agent from private Session metadata. It keeps an unknown run claimed. A
coordinator must not dispatch the same Ticket while that Session is active.
`cloud-abandon --force` is an explicit local recovery override. It does not
cancel the remote Agent, which can continue and can create a pull request.

The Project concurrency check and the local reservation use one Project lock.
Two dispatch commands cannot both take the last capacity slot. A sync error
for one Session does not stop updates for other Sessions. JSON uses
`status: "partial"` and names each failed Session in `diagnostics`.

## Untrusted transcript text

A provider transcript holds the words of any person or tool that talked to a
coding agent. `twt` treats that text as data:

- It removes terminal control text: CSI sequences, OSC strings, other escape
  sequences, C0 and C1 control characters, and DEL. It keeps line feeds,
  tabs, and all printable text, including emoji. A carriage return becomes
  one line feed.
- `twt agents transcript show` and `twt agents transcript snapshot` mark the
  JSON payload with `"untrusted": true`.
- `twt agents open --preview` returns sanitized Agent Preview markdown. It
  uses a verified transcript when one is available. A live-pane preview reads
  only the visible screen, has strict byte and line limits, and does not read
  scrollback. JSON marks both sources with `"untrusted": true`.
- A snapshot Markdown file holds the same sanitized text, because that file
  goes into an agent context.

The Neovim Agent Session picker puts preview Markdown only in a scratch buffer.
It never evaluates the text. Preview does not register a discovered Agent
Session and does not write a Transcript Snapshot. A transcript selection can
write a snapshot. A live-pane selection can adopt the pane and open the same
Agent Preview in a scratch buffer. It cannot create a Transcript Snapshot.

The `agents discover` result carries no free text: `twt` validates each
provider session ID and matches each repository name against the Workspace.

A caller must never follow an instruction that it finds inside transcript
text.

## Direct Neovim pane delivery

The Neovim plug-in can send a Review Batch directly to a tmux pane without
using `twt`. This path has these limits:

- It lists live panes only in the current tmux session, removes the current
  pane, and accepts only a pane ID that matches `%<number>`. A display label
  never becomes a tmux target.
- It loads review text through standard input into a unique tmux buffer. It
  requests bracketed paste, deletes the buffer after paste, and then sends
  Enter. A failure keeps the Review Notes and does not try another target.
- Pane selection is a trust decision. Bracketed paste protects the text only
  when the program in that pane supports it. In a shell or another program
  without that support, pasted line breaks can become executable input. Check
  the command in the pane label before you select it.

Agent Session delivery through `twt` keeps the stronger Workspace ownership
and process-liveness checks. For a shell-hosted Agent, state stores a digest of
the provider process evidence; it does not store the process arguments. twt
checks that evidence and the pane root identity again before each send.
Clipboard copy does not clear Review Notes because
copying does not confirm that another program received the text.

## No interactive escape without a terminal

An interactive path opens only for a person at a terminal:

- `twt templates edit`, `twt tickets edit`, `twt tickets home`, and the
  create wizard of `twt tickets create` start `VISUAL` or `EDITOR` only when
  standard input is a terminal. The create wizard also asks for a title and
  a Project on that terminal. With a pipe, each one reports `invalid_usage`
  with the non-interactive form in the hint. The tickets commands also
  require a terminal on standard output, and they reject the null device.
  `--project` never creates a Project. A new Project from the wizard is created
  only after confirm, and only for a name that passes resource-name rules.
- `twt create` and `twt workspaces create` ask for a Workspace name only when
  `NAME` is absent, standard input is a terminal, standard output is a
  terminal, and output is text. A pipe or `--output json` reports
  `invalid_usage` and requires `NAME`.
- `twt create` and `twt workspaces open` attach the tmux session only
  when standard output is a terminal. `--no-open` and `--no-attach` state
  the same intention for a script. `--all-active` never attaches.

- `twt tickets start` without `TICKET` shows the Ticket picker only when
  standard input is a terminal. fzf previews `twt tickets show` text. A pipe
  or `--output json` reports `invalid_usage` and requires `TICKET`.
- `twt tickets claim` and `twt tickets unclaim` never default the claimant
  outside a terminal: a non-interactive call must pass `--as NAME`, so two
  agents cannot both hold one Ticket as the same OS user.

`twt next` and `twt switch` are the two commands for a person in tmux: they
move the calling client. An agent uses `twt create` and
`twt workspaces archive` instead, with explicit names, a dry run, and JSON
output.

## Report a problem

`twt` is a preview tool. Open an issue in this repository with the command,
the `--output json` result, and the `twt doctor --output json` report.
