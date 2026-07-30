---
name: twt
description: Manage isolated Git worktrees with matching tmux sessions using twt. Use when asked to create or open parallel workspaces for a Git repository, share local project files across worktrees, or reset a twt workspace, especially when running separate AI coding agents.
---

# twt

Use `twt` as the entry point. Do not replace it with raw `git clone`, `git
worktree`, or `tmux new-session` commands.

## Create the workspaces

1. Determine the exact repository URL and requested workspace names. Preserve
   names supplied by the user. If no names are supplied, derive `<repo>-0`,
   then increment past existing worktree and session names.
2. Run `command -v twt`, `command -v tmux`, and `twt help`. If either command
   is missing, stop and report the missing prerequisite.
3. Ensure an interactive tmux client is active. If `$TMUX` is empty, run
   `tmux` once before continuing. `twt create` will switch an existing client
   or attach a new one.
4. Use the `TMUX_WORKTREE_DIR` shown by `twt help` to check for collisions:
   - If `.<repo>.git` exists, verify its `origin` points to the requested
     repository. Equivalent SSH and HTTPS GitHub URLs are acceptable.
   - If a requested worktree or tmux session already exists, reuse it only
     when the user asked to open existing work. Otherwise choose the next free
     numeric suffix.
5. Create each workspace:

   ```sh
   twt create https://github.com/example/repo repo-0
   twt create https://github.com/example/repo repo-1
   ```

   The first command creates the shared bare clone. Every command creates or
   opens a branch, worktree, and tmux session with the supplied name.
6. Verify each result with `tmux has-session -t <name>` and `git -C
   <worktree-path> status --short --branch`.
7. Report the repository URL, worktree path, branch, and tmux session for each
   workspace.

## Shared project files

If the user asks to share local configuration across the new worktrees, enable
the mechanism from the bare repo:

```sh
cd "$TMUX_WORKTREE_DIR/.repo.git"
twt shared enable
```

Place files at matching paths under `shared/`, such as
`shared/.lazy.lua`. The checkout hook symlinks them into every worktree without
overwriting existing non-symlink files.

## Reset a workspace

Run `twt reset` only when the user explicitly asks to reuse the current
workspace and confirms its work is safely pushed. It kills the other pane
processes and hard-resets tracked files to the origin default branch. It does
not remove untracked files.

## Guardrails

- Never delete a worktree, branch, or tmux session unless explicitly asked.
- Never add or edit shared files unless explicitly asked; changes affect every
  worktree for that repository.
