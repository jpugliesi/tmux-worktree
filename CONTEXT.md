# tmux-worktree

tmux-worktree creates and restores task-focused development environments that
combine Git repositories, tmux, and coding agents.

## Language

**Project Template**:
A reusable declaration of the repositories and initialization that a new
Project needs.
_Avoid_: Workspace template, change template

**Project**:
One unit of work created from a snapshot of a Project Template. A Project owns
its checkout leases, tmux session, and agent sessions.
_Avoid_: Change, task workspace

**Repository Specification**:
The clone source, remotes, history depth, and initialization declared for one
repository in a Project Template.
_Avoid_: Clone command

**Repository Cache**:
Shared Git object data for one clone source. Multiple checkout leases can use
one Repository Cache.
_Avoid_: Project repository, worktree

**Checkout Lease**:
A Git worktree assigned to one Project for one repository.
_Avoid_: Workspace, repository clone

**Prepared Environment**:
An unclaimed set of initialized Git worktrees for one exact Project Template
revision. A Project claims the complete set as its checkout leases.
_Avoid_: Warm Project, spare worktree, checkout pool item

**Agent Session**:
A resumable coding-agent conversation associated with one Project and,
optionally, one checkout lease.
_Avoid_: Agent process, tmux pane

**Agent Transcript**:
The provider conversation history linked to one Agent Session. An Agent
Transcript belongs to the same Project as its Agent Session.
_Avoid_: Log file, latest.md

**Transcript Snapshot**:
A Project-scoped Markdown copy of an Agent Transcript for review and display.
_Avoid_: Agent Transcript, global latest.md

**Initialization**:
A declared setup action that prepares a new physical Git worktree or Project
for use. Repository initialization runs at most once on each physical worktree.
_Avoid_: Bootstrap magic, implicit setup
