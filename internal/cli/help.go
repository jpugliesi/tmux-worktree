package cli

import "github.com/spf13/cobra"

type helpContent struct {
	long    string
	example string
}

// commandHelp maps each command path to its long help and examples. A test
// checks that every key matches a command and that every command has a key.
var commandHelp = map[string]helpContent{
	"twt2 templates": {
		long: "Create and maintain reusable YAML Project Templates. A template declares repositories, clone policy, tmux window names, and first-use initialization.", example: "  twt2 templates create everysphere\n  twt2 templates show everysphere",
	},
	"twt2 projects": {
		long: "Create and manage change-focused Projects. Each Project owns its worktrees, tmux session, setup checkpoints, and Agent Sessions.", example: "  twt2 projects create fix-auth --template everysphere\n  twt2 projects open fix-auth",
	},
	"twt2 new": {
		long: "Create a new Project from the latest saved version of the current Project Template. twt2 switches the calling client to the new Project, then archives the old Project. If NAME is absent, twt2 asks for it in an interactive terminal. Use 'twt2 projects create' for automation.", example: "  twt2 new fix-auth\n  twt2 new",
	},
	"twt2 switch": {
		long: "Switch the calling tmux client to the session of a Project. An archived Project opens first. Without PROJECT, twt2 shows an interactive Project picker: it uses fzf when fzf is installed, or a numbered list.", example: "  twt2 switch fix-auth\n  twt2 switch",
	},
	"twt2 archive": {
		long: "Archive the current Project or a Project that you specify. twt2 keeps its worktrees, branches, Project Template snapshot, and Agent Session records.", example: "  twt2 archive\n  twt2 archive fix-auth",
	},
	"twt2 done": {
		long: "Archive the current Project or a Project that you specify, then remove its worktrees, branches, and state. From inside the Project tmux session, twt2 moves your tmux client to the most recent other active Project, or detaches the client. Use --keep to stop after the archive.", example: "  twt2 done\n  twt2 done fix-auth --keep",
	},
	"twt2 agents": {
		long: "Register and control coding Agent Sessions that belong to a Project. Feedback delivery works only for a verified, directly started Agent process.", example: "  twt2 agents list --project current\n  twt2 agents resume AGENT_ID",
	},
	"twt2 storage": {
		long: "Inspect the disk space used by twt2 Projects, worktrees, and shared repository caches.", example: "  twt2 storage show",
	},
	"twt2 environments": {
		long: "Inspect the Prepared Environments that twt2 keeps for the next Project. A Prepared Environment holds initialized worktrees before a Project claims them.", example: "  twt2 environments list\n  twt2 environments show ENVIRONMENT_ID",
	},
	"twt2 templates create": {
		long: "Create an empty Project Template.\n\nNAME is the reusable template name. After creation, add one or more " +
			"repository specifications.\n\nWith --from-file or --from-stdin, twt2 reads one strict Project Template " +
			"YAML document. NAME stays required: it sets the template name, and a different name in the document is an error.",
		example: "  twt2 templates create everysphere\n  twt2 templates repos add everysphere app git@github.com:acme/app.git\n  twt2 templates create everysphere --from-file ./everysphere.yaml",
	},
	"twt2 templates list": {
		long: "List all saved Project Templates.", example: "  twt2 templates list\n  twt2 templates list --output json",
	},
	"twt2 templates show": {
		long: "Show one Project Template and its repository specifications.", example: "  twt2 templates show everysphere\n  twt2 templates show everysphere --output json",
	},
	"twt2 templates validate": {
		long: "Validate the YAML and all fields in one Project Template.", example: "  twt2 templates validate everysphere",
	},
	"twt2 templates prepare": {
		long: "Create and initialize one Prepared Environment for the next Project. Repository initialization does not run again when a Project claims it.", example: "  twt2 templates prepare everysphere",
	},
	"twt2 templates repos": {
		long: "Manage the repository specifications inside one Project Template.", example: "  twt2 templates repos add everysphere app https://example.com/app.git\n  twt2 templates repos remove everysphere app",
	},
	"twt2 templates init": {
		long: "Manage the initialization commands of a Project Template. One command runs for the Project, and one command runs for each repository.", example: "  twt2 templates init set product --cwd web -- ./scripts/init-project.sh\n  twt2 templates init set product --repo web -- ./init.sh",
	},
	"twt2 templates path": {
		long: "Print the YAML file path of one Project Template. The output is one bare path for command substitution.", example: "  twt2 templates path everysphere\n  $EDITOR $(twt2 templates path everysphere)",
	},
	"twt2 templates edit": {
		long: "Open the Project Template YAML file in VISUAL or EDITOR, then validate the result. An invalid file stays on disk and twt2 reports the unsafe_state error.", example: "  twt2 templates edit everysphere",
	},
	"twt2 templates remove": {
		long: "Delete the YAML file of a Project Template. twt2 refuses removal while a Project record still uses the Project Template.", example: "  twt2 templates remove everysphere\n  twt2 templates remove everysphere --dry-run --output json",
	},
	"twt2 templates repos remove": {
		long: "Remove one repository specification from a Project Template. Existing Projects keep their saved Project Template snapshot.", example: "  twt2 templates repos remove everysphere app",
	},
	"twt2 projects setup": {
		long: "Manage Project setup steps.", example: "  twt2 projects setup retry fix-auth",
	},
	"twt2 agents transcript": {
		long: "Read, link, and snapshot provider transcripts for Agent Sessions.", example: "  twt2 agents transcript show AGENT_ID --project current",
	},
	"twt2 templates repos add": {
		long:    "Add one repository specification to a Project Template. Flags define clone depth, remotes, default branch, and tmux window name.",
		example: "  twt2 templates repos add everysphere everysphere \\\n    https://origin.cursor.com/anysphere/everysphere.git \\\n    --depth 1 \\\n    --remote github=https://github.com/anysphere/everysphere.git",
	},
	"twt2 templates init set": {
		long: "Set one initialization command. Put the command and its arguments after --.\n\n" +
			"With --repo REPO, the command is repository initialization: twt2 runs it one time on each new physical worktree of that repository.\n\n" +
			"Without --repo, the command is Project initialization: twt2 runs it after all repository worktrees exist, and --cwd PATH must give its working directory inside the Project root.",
		example: "  twt2 templates init set product --cwd web -- ./scripts/init-project.sh\n  twt2 templates init set product --repo web -- ./init.sh",
	},
	"twt2 projects create": {
		long:    "Create a Project from a saved Project Template. twt2 claims a matching Prepared Environment or prepares one when necessary, then creates the tmux session.",
		example: "  twt2 projects create fix-auth --template everysphere\n  twt2 projects create fix-auth --template everysphere --dry-run --output json",
	},
	"twt2 projects list": {
		long: "List all Projects and their setup state.", example: "  twt2 projects list\n  twt2 projects list --limit 10 --output json",
	},
	"twt2 projects show": {
		long: "Show one Project by name or immutable ID.", example: "  twt2 projects show fix-auth --output json",
	},
	"twt2 projects current": {
		long: "Find the Project for the current directory or tmux pane.", example: "  twt2 projects current",
	},
	"twt2 projects path": {
		long: "Print the root path of a Project, or the checkout path of one repository in it. The output is one bare path for command substitution.", example: "  cd $(twt2 projects path fix-auth)\n  cd $(twt2 projects path fix-auth app)",
	},
	"twt2 projects open": {
		long: "Open a Project tmux session. twt2 makes an archived Project active. It also repairs missing managed windows.", example: "  twt2 projects open fix-auth\n  twt2 projects open fix-auth --no-attach",
	},
	"twt2 projects archive": {
		long: "Archive a Project and stop its owned tmux session. twt2 keeps the Project data so that you can open it again.", example: "  twt2 projects archive fix-auth\n  twt2 projects open fix-auth",
	},
	"twt2 projects setup retry": {
		long: "Retry failed or interrupted setup steps from the saved Project Template snapshot.", example: "  twt2 projects setup retry fix-auth",
	},
	"twt2 projects remove": {
		long: "Show a safe removal plan for an archived Project. Add --apply to remove clean, published Project worktrees and state. Use --all-archived with an optional --older-than age to plan or apply removal of all archived Projects; apply skips blocked Projects.", example: "  twt2 projects archive fix-auth\n  twt2 projects remove fix-auth --apply\n  twt2 projects remove --all-archived --older-than 14d --apply",
	},
	"twt2 agents register": {
		long: "Register a resumable coding Agent Session with a Project. Put the resume command after --. twt2 infers the provider and the provider session ID from that command. Use --provider and --session to set them yourself.", example: "  twt2 agents register -- codex resume SESSION_ID\n  twt2 agents register --project fix-auth --label review -- claude --resume SESSION_ID",
	},
	"twt2 agents list": {
		long: "List Agent Sessions for one Project, including status and capabilities. twt2 asks tmux for the live state of each pane. Use --live=false to not probe tmux.", example: "  twt2 agents list --project current --output json\n  twt2 agents list --project current --live=false",
	},
	"twt2 agents show": {
		long: "Show one Agent Session record and the result of each liveness check. A failed check tells you why twt2 does not send feedback to the Agent Session. The current command of the pane is advisory only.", example: "  twt2 agents show AGENT_ID\n  twt2 agents show AGENT_ID --project current --output json",
	},
	"twt2 agents discover": {
		long: "Find the Codex and Claude sessions that ran inside a repository of the Project and that no Agent Session uses. The newest session comes first. Add --adopt to register each session with a resume command.", example: "  twt2 agents discover --project current\n  twt2 agents discover --project current --adopt --limit 3",
	},
	"twt2 agents rm": {
		long: "Delete an Agent Session record. twt2 keeps the provider transcript and does not stop a live Agent process.", example: "  twt2 agents rm AGENT_ID\n  twt2 agents rm AGENT_ID --dry-run --output json",
	},
	"twt2 agents resume": {
		long: "Focus a live Agent Session or start its saved resume command in a new Project window.", example: "  twt2 agents resume AGENT_ID",
	},
	"twt2 agents focus": {
		long: "Focus the tmux pane for a live Agent Session.", example: "  twt2 agents focus AGENT_ID",
	},
	"twt2 agents send": {
		long: "Send standard-input text to a live, owned Agent Session in the selected Project. twt2 never sends to an unverified shell pane.", example: "  printf '%s\\n' 'Please fix this review note.' | twt2 agents send AGENT_ID --project current --stdin",
	},
	"twt2 agents transcript show": {
		long: "Read the provider transcript linked to one Agent Session. twt2 checks that the transcript belongs to the selected Project and does not return its source path.", example: "  twt2 agents transcript show AGENT_ID --project current --output json",
	},
	"twt2 agents transcript snapshot": {
		long: "Read a linked provider transcript and save a private Project-owned Markdown snapshot. Each Agent Session has its own file, and twt2 also writes latest.md as a copy of the most recent snapshot. The result gives the file path. Project removal deletes these snapshots.", example: "  twt2 agents transcript snapshot AGENT_ID --project current --output json",
	},
	"twt2 agents transcript link": {
		long: "Link an existing Agent Session to its provider session ID. This enables transcript loading without changing its resume command.", example: "  twt2 agents transcript link AGENT_ID --project current --session SESSION_ID",
	},
	"twt2 tickets": {
		long: "Create and manage Markdown tickets in the configured Tickets home. Each Ticket is one Obsidian note with YAML frontmatter, and the CLI owns every mutation.", example: "  twt2 tickets create \"fix the vfs tools\" --board change-monitor\n  twt2 tickets list --ready --output json",
	},
	"twt2 tickets init": {
		long: "Create the Tickets home directory with its hub index.md and its create template. twt2 writes each file only when that file is missing. It never overwrites notes.", example: "  twt2 tickets init\n  twt2 tickets init --dry-run --output json",
	},
	"twt2 tickets create": {
		long: "Create one Ticket file. DESCRIPTION becomes the body, and its first line becomes the title when --title is absent. With --stdin, twt2 reads the body from standard input and --title is required. With no input in an interactive terminal, twt2 opens VISUAL or EDITOR on a copy of the create template.", example: "  twt2 tickets create \"fix the vfs tools\" --board change-monitor --output json\n  printf '%s\\n' 'Steps...' | twt2 tickets create --title \"Fix auth\" --stdin",
	},
	"twt2 tickets list": {
		long: "List Tickets sorted by priority, then by slug. --ready lists only pickable work: ready-for-agent, unclaimed, and with every blocker done or wontfix. --status is a raw filter; do not use it together with --ready.", example: "  twt2 tickets list --ready --output json\n  twt2 tickets list --board change-monitor --limit 10",
	},
	"twt2 tickets show": {
		long: "Show one Ticket with its metadata, its open blockers, and its body. TICKET accepts a slug, a unique slug prefix, a title, an alias, a wiki-link, or a path under the Tickets home.", example: "  twt2 tickets show fix-the-vfs-tools\n  twt2 tickets show '[[fix-the-vfs-tools]]' --output json",
	},
	"twt2 tickets edit": {
		long: "Replace the body of one Ticket. With --stdin, twt2 reads the new body from standard input. In an interactive terminal without --stdin, twt2 opens VISUAL or EDITOR on the Ticket file and then validates the result. An invalid file stays on disk and twt2 reports the unsafe_state error.", example: "  printf '%s\\n' '# Title' 'New body' | twt2 tickets edit fix-the-vfs-tools --stdin\n  twt2 tickets edit fix-the-vfs-tools",
	},
	"twt2 tickets set": {
		long: "Change the status, the priority, or the Board of one Ticket. Pass at least one flag. A Board change moves the Ticket file into the Board directory.", example: "  twt2 tickets set fix-the-vfs-tools --status done\n  twt2 tickets set fix-the-vfs-tools --priority 1 --board change-monitor",
	},
	"twt2 tickets claim": {
		long: "Claim one Ticket for a work session. The claimant comes from --as, then TWT2_CLAIMANT, then the OS username in an interactive terminal. A Ticket that a different claimant holds returns the locked error.", example: "  twt2 tickets claim fix-the-vfs-tools --as codex-fix-auth\n  twt2 tickets claim fix-the-vfs-tools --as codex-fix-auth --output json",
	},
	"twt2 tickets unclaim": {
		long: "Remove the claim on one Ticket. The claimant resolution is the same as claim, and only the current claimant can remove its claim. An unclaimed Ticket succeeds without a change.", example: "  twt2 tickets unclaim fix-the-vfs-tools --as codex-fix-auth",
	},
	"twt2 tickets comment": {
		long: "Append one comment from standard input under the '## Comments' heading of a Ticket. twt2 creates that heading when it is missing.", example: "  printf '%s\\n' 'Shipped in PR 42.' | twt2 tickets comment fix-the-vfs-tools --stdin",
	},
	"twt2 tickets boards": {
		long: "Manage Boards. A Board is one directory under the Tickets home that groups Tickets and outlives any checkout.", example: "  twt2 tickets boards create change-monitor\n  twt2 tickets boards list --output json",
	},
	"twt2 tickets boards create": {
		long: "Create one Board directory and write its index.md only when that file is missing.", example: "  twt2 tickets boards create change-monitor",
	},
	"twt2 tickets boards list": {
		long: "List every Board with its Ticket count.", example: "  twt2 tickets boards list\n  twt2 tickets boards list --output json",
	},
	"twt2 tickets boards show": {
		long: "Show one Board: its path, its Ticket count, and whether it has an index.md.", example: "  twt2 tickets boards show change-monitor --output json",
	},
	"twt2 context": {
		long: "Show the Project and repository context for a directory or the current tmux pane.", example: "  twt2 context --output json\n  twt2 context --directory /path/to/worktree --output json",
	},
	"twt2 storage show": {
		long: "Show the disk space used by active Projects, archived Projects, Prepared Environments, worktrees, and shared repository caches.", example: "  twt2 storage show\n  twt2 storage show --output json",
	},
	"twt2 storage clean": {
		long: "Show a safe cleanup plan for failed and obsolete Prepared Environments, orphan Transcript Snapshots, and orphan Agent Session records. Add --apply to remove only twt2-owned data.", example: "  twt2 storage clean\n  twt2 storage clean --apply",
	},
	"twt2 environments list": {
		long: "List the Prepared Environments of each Project Template, with status, age, and disk space. A ready Prepared Environment that no longer matches its Project Template has status obsolete.", example: "  twt2 environments list\n  twt2 environments list --limit 10 --output json",
	},
	"twt2 environments show": {
		long: "Show one Prepared Environment, its preparation steps, its base commit for each repository, and the Project that claims it. ENVIRONMENT_ID accepts a unique ID prefix.", example: "  twt2 environments show 1a2b3c4d\n  twt2 environments show ENVIRONMENT_ID --output json",
	},
	"twt2 doctor": {
		long: "Check required tools, Project Templates, Project state, and ownership markers.", example: "  twt2 doctor\n  twt2 doctor --output json",
	},
	"twt2 schema": {
		long: "Show the versioned machine-readable schema for commands, arguments, flags, and raw apply operations.", example: "  twt2 schema | jq .",
	},
	"twt2 apply": {
		long: "Read one strict JSON mutation request from standard input. Unknown fields and extra JSON values cause an error.", example: `  printf '%s' '{"operation":"templates.create","template":{"name":"demo"}}' | twt2 apply --stdin --dry-run --output json`,
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
