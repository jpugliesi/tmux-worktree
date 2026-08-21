package cli

import "github.com/spf13/cobra"

type helpContent struct {
	long    string
	example string
}

// commandHelp maps each command path to its long help and examples. A test
// checks that every key matches a command and that every command has a key.
var commandHelp = map[string]helpContent{
	"twt templates": {
		long: "Create and maintain reusable YAML Project Templates. A template declares repositories, clone policy, tmux window names, and first-use initialization.", example: "  twt templates create everysphere\n  twt templates show everysphere",
	},
	"twt projects": {
		long: "Create and manage change-focused Projects. Each Project owns its worktrees, tmux session, setup checkpoints, and Agent Sessions.", example: "  twt projects create fix-auth --template everysphere\n  twt projects open fix-auth",
	},
	"twt new": {
		long: "Create a new Project from the latest saved version of the current Project Template. twt switches the calling client to the new Project, then archives the old Project. If NAME is absent, twt asks for it in an interactive terminal. Use 'twt projects create' for automation.", example: "  twt new fix-auth\n  twt new",
	},
	"twt switch": {
		long: "Switch the calling tmux client to the session of a Project. An archived Project opens first. Without PROJECT, twt shows an interactive Project picker: it uses fzf when fzf is installed, or a numbered list.", example: "  twt switch fix-auth\n  twt switch",
	},
	"twt archive": {
		long: "Archive the current Project or a Project that you specify. twt keeps its worktrees, branches, Project Template snapshot, and Agent Session records.", example: "  twt archive\n  twt archive fix-auth",
	},
	"twt done": {
		long: "Archive the current Project or a Project that you specify, then remove its worktrees, branches, and state. From inside the Project tmux session, twt moves your tmux client to the most recent other active Project, or detaches the client. Use --keep to stop after the archive.", example: "  twt done\n  twt done fix-auth --keep",
	},
	"twt agents": {
		long: "Register and control coding Agent Sessions that belong to a Project. Feedback delivery works only for a verified, directly started Agent process.", example: "  twt agents list --project current\n  twt agents resume AGENT_ID",
	},
	"twt storage": {
		long: "Inspect the disk space used by twt Projects, worktrees, and shared repository caches.", example: "  twt storage show",
	},
	"twt environments": {
		long: "Inspect the Prepared Environments that twt keeps for the next Project. A Prepared Environment holds initialized worktrees before a Project claims them.", example: "  twt environments list\n  twt environments show ENVIRONMENT_ID",
	},
	"twt templates create": {
		long: "Create an empty Project Template.\n\nNAME is the reusable template name. After creation, add one or more " +
			"repository specifications.\n\nWith --from-file or --from-stdin, twt reads one strict Project Template " +
			"YAML document. NAME stays required: it sets the template name, and a different name in the document is an error.",
		example: "  twt templates create everysphere\n  twt templates repos add everysphere app git@github.com:acme/app.git\n  twt templates create everysphere --from-file ./everysphere.yaml",
	},
	"twt templates list": {
		long: "List all saved Project Templates.", example: "  twt templates list\n  twt templates list --output json",
	},
	"twt templates show": {
		long: "Show one Project Template and its repository specifications.", example: "  twt templates show everysphere\n  twt templates show everysphere --output json",
	},
	"twt templates validate": {
		long: "Validate the YAML and all fields in one Project Template.", example: "  twt templates validate everysphere",
	},
	"twt templates prepare": {
		long: "Create and initialize one Prepared Environment for the next Project. Repository initialization does not run again when a Project claims it.", example: "  twt templates prepare everysphere",
	},
	"twt templates repos": {
		long: "Manage the repository specifications inside one Project Template.", example: "  twt templates repos add everysphere app https://example.com/app.git\n  twt templates repos remove everysphere app",
	},
	"twt templates init": {
		long: "Manage the initialization commands of a Project Template. One command runs for the Project, and one command runs for each repository.", example: "  twt templates init set product --cwd web -- ./scripts/init-project.sh\n  twt templates init set product --repo web -- ./init.sh",
	},
	"twt templates path": {
		long: "Print the YAML file path of one Project Template. The output is one bare path for command substitution.", example: "  twt templates path everysphere\n  $EDITOR $(twt templates path everysphere)",
	},
	"twt templates edit": {
		long: "Open the Project Template YAML file in VISUAL or EDITOR, then validate the result. An invalid file stays on disk and twt reports the unsafe_state error.", example: "  twt templates edit everysphere",
	},
	"twt templates remove": {
		long: "Delete the YAML file of a Project Template. twt refuses removal while a Project record still uses the Project Template.", example: "  twt templates remove everysphere\n  twt templates remove everysphere --dry-run --output json",
	},
	"twt templates repos remove": {
		long: "Remove one repository specification from a Project Template. Existing Projects keep their saved Project Template snapshot.", example: "  twt templates repos remove everysphere app",
	},
	"twt projects setup": {
		long: "Manage Project setup steps.", example: "  twt projects setup retry fix-auth",
	},
	"twt agents transcript": {
		long: "Read, link, and snapshot provider transcripts for Agent Sessions.", example: "  twt agents transcript show AGENT_ID --project current",
	},
	"twt templates repos add": {
		long:    "Add one repository specification to a Project Template. Flags define clone depth, remotes, default branch, and tmux window name.",
		example: "  twt templates repos add everysphere everysphere \\\n    https://origin.cursor.com/anysphere/everysphere.git \\\n    --depth 1 \\\n    --remote github=https://github.com/anysphere/everysphere.git",
	},
	"twt templates init set": {
		long: "Set one initialization command. Put the command and its arguments after --.\n\n" +
			"With --repo REPO, the command is repository initialization: twt runs it one time on each new physical worktree of that repository.\n\n" +
			"Without --repo, the command is Project initialization: twt runs it after all repository worktrees exist, and --cwd PATH must give its working directory inside the Project root.",
		example: "  twt templates init set product --cwd web -- ./scripts/init-project.sh\n  twt templates init set product --repo web -- ./init.sh",
	},
	"twt projects create": {
		long:    "Create a Project from a saved Project Template. twt claims a matching Prepared Environment or prepares one when necessary, then creates the tmux session. The Project branch name comes from --branch, then the branch_pattern of the Project Template, then the default pattern {prefix}{name}. Without a branch prefix (TWT_BRANCH_PREFIX or the branchPrefix value of config.yaml) the default is the Project name.",
		example: "  twt projects create fix-auth --template everysphere\n  twt projects create fix-auth --template everysphere --dry-run --output json",
	},
	"twt projects list": {
		long: "List all Projects and their setup state.", example: "  twt projects list\n  twt projects list --limit 10 --output json",
	},
	"twt projects show": {
		long: "Show one Project by name or immutable ID.", example: "  twt projects show fix-auth --output json",
	},
	"twt projects current": {
		long: "Find the Project for the current directory or tmux pane.", example: "  twt projects current",
	},
	"twt projects path": {
		long: "Print the root path of a Project, or the checkout path of one repository in it. The output is one bare path for command substitution.", example: "  cd $(twt projects path fix-auth)\n  cd $(twt projects path fix-auth app)",
	},
	"twt projects open": {
		long: "Open a Project tmux session. twt makes an archived Project active. It also repairs missing managed windows.", example: "  twt projects open fix-auth\n  twt projects open fix-auth --no-attach",
	},
	"twt projects archive": {
		long: "Archive a Project and stop its owned tmux session. twt keeps the Project data so that you can open it again.", example: "  twt projects archive fix-auth\n  twt projects open fix-auth",
	},
	"twt projects setup retry": {
		long: "Retry failed or interrupted setup steps from the saved Project Template snapshot.", example: "  twt projects setup retry fix-auth",
	},
	"twt projects adopt": {
		long: "Adopt an existing tmux session as a Project. twt records the git repositories that the panes of the session sit in, and marks the session with the Project ID. twt did not create the directories of an adopted Project, and removal never deletes them: removal deletes only the twt state and releases the session marker.", example: "  twt projects adopt\n  twt projects adopt my-session --name fix-auth",
	},
	"twt projects remove": {
		long: "Show a safe removal plan for an archived Project. Add --apply to remove clean, published Project worktrees and state. Use --all-archived with an optional --older-than age to plan or apply removal of all archived Projects; apply skips blocked Projects.", example: "  twt projects archive fix-auth\n  twt projects remove fix-auth --apply\n  twt projects remove --all-archived --older-than 14d --apply",
	},
	"twt agents register": {
		long: "Register a resumable coding Agent Session with a Project. Put the resume command after --. twt infers the provider and the provider session ID from that command. Use --provider and --session to set them yourself.", example: "  twt agents register -- codex resume SESSION_ID\n  twt agents register --project fix-auth --label review -- claude --resume SESSION_ID",
	},
	"twt agents list": {
		long: "List Agent Sessions for one Project, including status and capabilities. twt asks tmux for the live state of each pane, and scans the Codex and Claude stores for discovered sessions of the Project. A discovered session has status \"discovered\"; the first action on it registers it. The list writes nothing. Use --registered to not scan the providers. Use --live=false for a cheap read that does not probe tmux and does not scan the providers.", example: "  twt agents list --project current --output json\n  twt agents list --project current --registered\n  twt agents list --project current --live=false",
	},
	"twt agents show": {
		long: "Show one Agent Session record and the result of each liveness check. A failed check tells you why twt does not send feedback to the Agent Session. The current command of the pane is advisory only.", example: "  twt agents show AGENT_ID\n  twt agents show AGENT_ID --project current --output json",
	},
	"twt agents discover": {
		long: "Find the Codex and Claude sessions that ran inside a repository of the Project and that no Agent Session uses. The newest session comes first. Add --adopt to register each session with a resume command. 'twt agents list' also shows these sessions, and the first action on one adopts it; use discover --adopt for bulk adoption.", example: "  twt agents discover --project current\n  twt agents discover --project current --adopt --limit 3",
	},
	"twt agents rm": {
		long: "Delete an Agent Session record. twt keeps the provider transcript and does not stop a live Agent process.", example: "  twt agents rm AGENT_ID\n  twt agents rm AGENT_ID --dry-run --output json",
	},
	"twt agents resume": {
		long: "Focus a live Agent Session or start its saved resume command in a new Project window. A discovered provider session ID also resumes: twt registers it first.", example: "  twt agents resume AGENT_ID\n  twt agents resume PROVIDER_SESSION_ID",
	},
	"twt agents focus": {
		long: "Focus the tmux pane for a live Agent Session.", example: "  twt agents focus AGENT_ID",
	},
	"twt agents send": {
		long: "Send standard-input text to a live, owned Agent Session in the selected Project. twt never sends to an unverified shell pane.", example: "  printf '%s\\n' 'Please fix this review note.' | twt agents send AGENT_ID --project current --stdin",
	},
	"twt agents transcript show": {
		long: "Read the provider transcript linked to one Agent Session. twt checks that the transcript belongs to the selected Project and does not return its source path.", example: "  twt agents transcript show AGENT_ID --project current --output json",
	},
	"twt agents transcript snapshot": {
		long: "Read a linked provider transcript and save a private Project-owned Markdown snapshot. Each Agent Session has its own file, and twt also writes latest.md as a copy of the most recent snapshot. The result gives the file path. Project removal deletes these snapshots.", example: "  twt agents transcript snapshot AGENT_ID --project current --output json",
	},
	"twt agents transcript link": {
		long: "Link an existing Agent Session to its provider session ID. This enables transcript loading without changing its resume command.", example: "  twt agents transcript link AGENT_ID --project current --session SESSION_ID",
	},
	"twt tickets": {
		long: "Create and manage Markdown tickets in the configured Tickets home. Each Ticket is one Obsidian note with YAML frontmatter, and the CLI owns every mutation.", example: "  twt tickets create \"fix the vfs tools\" --board change-monitor\n  twt tickets list --ready --output json",
	},
	"twt tickets init": {
		long: "Create the Tickets home directory with its hub index.md and its create template. twt writes each file only when that file is missing. It never overwrites notes.", example: "  twt tickets init\n  twt tickets init --dry-run --output json",
	},
	"twt tickets create": {
		long: "Create one Ticket file. DESCRIPTION becomes the body, and its first line becomes the title when --title is absent. With --stdin, twt reads the body from standard input and --title is required. With no input in an interactive terminal, twt opens VISUAL or EDITOR on a copy of the create template.", example: "  twt tickets create \"fix the vfs tools\" --board change-monitor --output json\n  printf '%s\\n' 'Steps...' | twt tickets create --title \"Fix auth\" --stdin",
	},
	"twt tickets list": {
		long: "List Tickets sorted by priority, then by slug. --ready lists only pickable work: ready-for-agent, unclaimed, and with every blocker done or wontfix. --status is a raw filter; do not use it together with --ready.", example: "  twt tickets list --ready --output json\n  twt tickets list --board change-monitor --limit 10",
	},
	"twt tickets show": {
		long: "Show one Ticket with its metadata, its open blockers, and its body. TICKET accepts a slug, a unique slug prefix, a title, an alias, a wiki-link, or a path under the Tickets home.", example: "  twt tickets show fix-the-vfs-tools\n  twt tickets show '[[fix-the-vfs-tools]]' --output json",
	},
	"twt tickets edit": {
		long: "Replace the body of one Ticket. With --stdin, twt reads the new body from standard input. In an interactive terminal without --stdin, twt opens VISUAL or EDITOR on the Ticket file and then validates the result. An invalid file stays on disk and twt reports the unsafe_state error.", example: "  printf '%s\\n' '# Title' 'New body' | twt tickets edit fix-the-vfs-tools --stdin\n  twt tickets edit fix-the-vfs-tools",
	},
	"twt tickets set": {
		long: "Change the status, the priority, or the Board of one Ticket. Pass at least one flag. A Board change moves the Ticket file into the Board directory.", example: "  twt tickets set fix-the-vfs-tools --status done\n  twt tickets set fix-the-vfs-tools --priority 1 --board change-monitor",
	},
	"twt tickets claim": {
		long: "Claim one Ticket for a work session. The claimant comes from --as, then TWT_CLAIMANT, then the OS username in an interactive terminal. A Ticket that a different claimant holds returns the locked error.", example: "  twt tickets claim fix-the-vfs-tools --as codex-fix-auth\n  twt tickets claim fix-the-vfs-tools --as codex-fix-auth --output json",
	},
	"twt tickets unclaim": {
		long: "Remove the claim on one Ticket. The claimant resolution is the same as claim, and only the current claimant can remove its claim. An unclaimed Ticket succeeds without a change.", example: "  twt tickets unclaim fix-the-vfs-tools --as codex-fix-auth",
	},
	"twt tickets comment": {
		long: "Append one comment from standard input under the '## Comments' heading of a Ticket. twt creates that heading when it is missing.", example: "  printf '%s\\n' 'Shipped in PR 42.' | twt tickets comment fix-the-vfs-tools --stdin",
	},
	"twt tickets boards": {
		long: "Manage Boards. A Board is one directory under the Tickets home that groups Tickets and outlives any checkout.", example: "  twt tickets boards create change-monitor\n  twt tickets boards list --output json",
	},
	"twt tickets boards create": {
		long: "Create one Board directory and write its index.md only when that file is missing.", example: "  twt tickets boards create change-monitor",
	},
	"twt tickets boards list": {
		long: "List every Board with its Ticket count.", example: "  twt tickets boards list\n  twt tickets boards list --output json",
	},
	"twt tickets boards show": {
		long: "Show one Board: its path, its Ticket count, and whether it has an index.md.", example: "  twt tickets boards show change-monitor --output json",
	},
	"twt context": {
		long: "Show the Project and repository context for a directory or the current tmux pane.", example: "  twt context --output json\n  twt context --directory /path/to/worktree --output json",
	},
	"twt storage show": {
		long: "Show the disk space used by active Projects, archived Projects, Prepared Environments, worktrees, and shared repository caches.", example: "  twt storage show\n  twt storage show --output json",
	},
	"twt storage clean": {
		long: "Show a safe cleanup plan for failed and obsolete Prepared Environments, orphan Transcript Snapshots, and orphan Agent Session records. Add --apply to remove only twt-owned data.", example: "  twt storage clean\n  twt storage clean --apply",
	},
	"twt environments list": {
		long: "List the Prepared Environments of each Project Template, with status, age, and disk space. A ready Prepared Environment that no longer matches its Project Template has status obsolete.", example: "  twt environments list\n  twt environments list --limit 10 --output json",
	},
	"twt environments show": {
		long: "Show one Prepared Environment, its preparation steps, its base commit for each repository, and the Project that claims it. ENVIRONMENT_ID accepts a unique ID prefix.", example: "  twt environments show 1a2b3c4d\n  twt environments show ENVIRONMENT_ID --output json",
	},
	"twt doctor": {
		long: "Check required tools, Project Templates, Project state, and ownership markers.", example: "  twt doctor\n  twt doctor --output json",
	},
	"twt skills": {
		long: "Install the twt agent skill that this build carries. The skill tells an agent how to call twt: JSON output, dry runs, limits, and untrusted transcript text.", example: "  twt skills install\n  twt skills show",
	},
	"twt skills install": {
		long: "Write the twt agent skill of this build into each skill tree. Without --dir, twt writes ~/.cursor/skills/twt/SKILL.md, ~/.claude/skills/twt/SKILL.md, and ~/.agents/skills/twt/SKILL.md. Each copy is a real file with a version stamp, not a symlink, so the skill stays correct when the repository checkout moves. Run this command again after a twt upgrade.", example: "  twt skills install\n  twt skills install --dir ./skills --dry-run --output json",
	},
	"twt skills show": {
		long: "Print the twt agent skill of this build, with the version stamp that install writes.", example: "  twt skills show\n  twt skills show --output json",
	},
	"twt schema": {
		long: "Show the versioned machine-readable schema for commands, arguments, flags, and raw apply operations.", example: "  twt schema | jq .",
	},
	"twt apply": {
		long: "Read one strict JSON mutation request from standard input. Unknown fields and extra JSON values cause an error.", example: `  printf '%s' '{"operation":"templates.create","template":{"name":"demo"}}' | twt apply --stdin --dry-run --output json`,
	},
}

func configureCommandHelp(root *cobra.Command) {
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if value, ok := commandHelp[command.CommandPath()]; ok {
			command.Long = value.long
			command.Example = value.example
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(root)
}
