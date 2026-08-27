# tmux-worktree

tmux-worktree creates and restores task-focused development environments that
combine Git repositories, tmux, and coding agents.

## Language

**Workspace Template**:
A reusable declaration of one or more Repository Specifications and the
initialization that a new Workspace needs.
_Avoid_: Project template, change template

**Workspace**:
A temporary, resumable place for work on zero or more Tickets from one
Project. An active Workspace owns a tmux session and Checkout Leases. An
archived Workspace keeps its branches, Template snapshot, and Agent Sessions.
An adopted Workspace can have no Checkout Leases.
_Avoid_: Project, session, workdir

**Repository Specification**:
The clone source, remotes, history depth, and initialization declared for one
repository in a Workspace Template.
_Avoid_: Clone command

**Repository Cache**:
Shared Git object data for one clone source. Multiple checkout leases can use
one Repository Cache.
_Avoid_: Workspace repository, worktree

**Checkout Lease**:
A Git worktree assigned to one Workspace for one repository.
_Avoid_: Workspace, repository clone

**Prepared Environment**:
A reusable lifecycle record for a set of Git worktrees for one exact Workspace
Template revision. One Workspace can claim the complete set as its Checkout
Leases. A release returns the same set to the ready state.
During an in-session release, it stays releasing until twt confirms that the
source tmux session is gone.
_Avoid_: Warm Workspace, spare worktree, checkout pool item

**Agent Session**:
A coding-agent run associated with one Workspace and, optionally, one
checkout lease. It can have a verified live process, a resume command, a
linked Agent Transcript, or a combination of these.
_Avoid_: Agent process, tmux pane

**Local Dispatch Session**:
One local implementation run for one Ticket: a Workspace plus one autonomous
implementation Agent Session, tracked as a durable session record so a
coordinator can dispatch, observe, and recover it. Its Workspace is the
Ticket's active Workspace.
_Avoid_: local Cloud Session, dispatch Workspace, worker record

**Agent Candidate**:
A verified provider transcript or live provider process that belongs to a
Workspace but has no Agent Session record. It has a provider-qualified,
versioned reference. Preview is read-only. Adoption creates the Agent Session.
_Avoid_: Agent Session ID, raw provider session ID

**Agent Transcript**:
The provider conversation history linked to one Agent Session. An Agent
Transcript belongs to the same Workspace as its Agent Session.
_Avoid_: Log file, latest.md

**Transcript Snapshot**:
A Workspace-scoped Markdown copy of an Agent Transcript for review and
display. Archive keeps it. Workspace removal deletes it.
_Avoid_: Agent Transcript, global latest.md

**Agent Preview**:
Read-only display text from a verified Agent Transcript or the visible screen
of a verified live Agent Session pane. A live-pane Agent Preview is not an
Agent Transcript and cannot become a Transcript Snapshot.
_Avoid_: Transcript Snapshot, pane transcript

**Review Note**:
A Neovim-session annotation anchored to lines in one file. It does not belong
to a Workspace, repository, or Agent Session.
_Avoid_: Comment, Agent feedback

**Review Batch**:
All current Review Notes in one Neovim session. A user can copy it or send it
to an Agent Session or tmux pane.
_Avoid_: Workspace review, Agent feedback

**Initialization**:
A declared setup action that prepares a physical Git worktree or Workspace for
use. Repository initialization runs after preparation and after a base refresh.
Workspace initialization runs after each claim.
_Avoid_: Bootstrap magic, implicit setup

**Environment Digest**:
The hash of the part of a Workspace Template revision that changes the physical
worktrees: each repository name, clone source, depth, remotes, default branch,
repository initialization, and recycle command. A Workspace can claim a Prepared Environment
only when the digests match. A change to the template name, a window name, the
Workspace initialization, or the pool depth keeps the digest.
_Avoid_: Template hash, template version

**Removal Blocker**:
One recorded reason that stops Workspace removal, with a stable code, a message,
the related paths, and an optional hint. A removal plan holds all of its
Removal Blockers, and removal applies no action while one stays.
_Avoid_: Error, removal failure

**Tickets home**:
Configured root directory of ticket Markdown files. Default personal value:
`~/Vaults/spacexai/tickets/`.
_Avoid_: Vault, issues dir

**Closed Tickets directory**:
The twt-owned tree at `$TICKETS_HOME/closed`, marked by `.twt-closed`. It
holds `done` and `wontfix` Tickets. The `closed` segment is not a Project.
_Avoid_: Closed Project, archive Project

**Project**:
One durable directory under Tickets home, with `index.md`. It groups Tickets
and selects one Workspace Template. A Project can have many Workspaces over
time. Its Ticket count includes active and closed Tickets. `twt_closed: true`
in `index.md` closes the Project. A closed Project keeps its directory and plan.
Default Project lists and completion omit it. New work cannot use it.
_Avoid_: Board, workspace, epic folder

**Ticket Store**:
The backend contract for tickets (`ticket.Store`): semantic operations with
atomic compare-and-set claims, idempotent retries, and dry runs, plus declared
capabilities. The markdown-plus-git Service is the reference implementation;
the conformance suite is the executable contract every backend must pass.
_Avoid_: database, ticket file API

**Ticket**:
One Markdown file with YAML frontmatter. A Ticket belongs to one Project. A
claim gives one worker exclusive work authority. A Ticket can link to one
active Workspace and one active Local Dispatch Session; the Session's
Workspace is the Ticket's active Workspace. Its directory defines
its Project, including below the Closed Tickets directory.
_Avoid_: Issue, task file, combined tickets note

**Topic note**:
An Obsidian wiki-link to a knowledge note, such as `[[Change Monitor Agent]]`.
Not a Project.
_Avoid_: Board

## Example dialogue

Developer: "Does this Review Note belong to the current Workspace?"

Domain expert: "No. It belongs to the current Neovim Review Batch. You can
send that batch to an Agent Session in a Workspace, to a tmux pane, or to the
clipboard."
