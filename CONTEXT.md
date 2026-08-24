# tmux-worktree

tmux-worktree creates and restores task-focused development environments that
combine Git repositories, tmux, and coding agents.

## Language

**Workspace Template**:
A reusable declaration of the repositories and initialization that a new
Workspace needs.
_Avoid_: Project template, change template

**Workspace**:
A temporary, resumable place for work on zero or more Tickets from one
Project. A Workspace owns its checkout leases, tmux session, and Agent
Sessions.
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
A lifecycle record for a set of Git worktrees for one exact Workspace
Template revision. twt prepares the set, and one Workspace can claim the
complete set as its checkout leases.
_Avoid_: Warm Workspace, spare worktree, checkout pool item

**Agent Session**:
A resumable coding-agent conversation associated with one Workspace and,
optionally, one checkout lease.
_Avoid_: Agent process, tmux pane

**Agent Transcript**:
The provider conversation history linked to one Agent Session. An Agent
Transcript belongs to the same Workspace as its Agent Session.
_Avoid_: Log file, latest.md

**Transcript Snapshot**:
A Workspace-scoped Markdown copy of an Agent Transcript for review and
display. Archive keeps it. Workspace removal deletes it.
_Avoid_: Agent Transcript, global latest.md

**Initialization**:
A declared setup action that prepares a new physical Git worktree or Workspace
for use. Repository initialization runs at most once on each physical worktree.
_Avoid_: Bootstrap magic, implicit setup

**Environment Digest**:
The hash of the part of a Workspace Template revision that changes the physical
worktrees: each repository name, clone source, depth, remotes, default branch,
and repository initialization. A Workspace can claim a Prepared Environment
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

**Project**:
One durable directory under Tickets home, with `index.md`. It groups Tickets
and can have many Workspaces over time.
_Avoid_: Board, workspace, epic folder

**Ticket**:
One Markdown file with YAML frontmatter. A Ticket belongs to one Project and
can belong to only one active Workspace.
_Avoid_: Issue, task file, combined tickets note

**Topic note**:
An Obsidian wiki-link to a knowledge note, such as `[[Change Monitor Agent]]`.
Not a Project.
_Avoid_: Board
