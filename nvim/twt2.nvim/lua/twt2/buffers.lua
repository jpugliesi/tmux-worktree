-- The buffer-local variable names that mark a twt2 snapshot buffer. A snapshot
-- buffer keeps the Project it belongs to, so a later action uses that Project
-- and not the Project of the working directory.
return {
  project_id = "twt2_project_id",
  project_directory = "twt2_project_directory",
}
