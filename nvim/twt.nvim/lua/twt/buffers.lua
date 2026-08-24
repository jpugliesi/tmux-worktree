-- The buffer-local variable names that mark a twt snapshot buffer. A snapshot
-- buffer keeps the Workspace it belongs to, so a later action uses that Workspace
-- and not the Workspace of the working directory.
return {
  workspace_id = "twt_workspace_id",
  workspace_directory = "twt_workspace_directory",
}
