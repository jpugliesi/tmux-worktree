package cli

import "github.com/spf13/cobra"

type helpContent struct {
	long    string
	example string
}

func configureCommandHelp(root *cobra.Command) {
	content := map[string]helpContent{
		"twt2 templates": {
			long: "Create and maintain reusable YAML Project Templates. A template declares repositories, clone policy, tmux window names, and first-use initialization.", example: "  twt2 templates create everysphere\n  twt2 templates show everysphere",
		},
		"twt2 projects": {
			long: "Create and manage change-focused Projects. Each Project owns its worktrees, tmux session, setup checkpoints, and Agent Sessions.", example: "  twt2 projects create fix-auth --template everysphere\n  twt2 projects open fix-auth",
		},
		"twt2 agents": {
			long: "Register and control coding Agent Sessions that belong to a Project. Feedback delivery works only for a verified, directly started Agent process.", example: "  twt2 agents list --project current\n  twt2 agents resume AGENT_ID",
		},
		"twt2 storage": {
			long: "Inspect the disk space used by twt2 Projects, worktrees, and shared repository caches.", example: "  twt2 storage status",
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
		"twt2 templates repos add": {
			long:    "Add one repository specification to a Project Template. Flags define clone depth, remotes, default branch, and tmux window name.",
			example: "  twt2 templates repos add everysphere everysphere \\\n    https://origin.cursor.com/anysphere/everysphere.git \\\n    --depth 1 \\\n    --remote github=https://github.com/anysphere/everysphere.git",
		},
		"twt2 templates repos init set": {
			long:    "Set the command that runs once when twt2 prepares a new repository worktree. Put the command and its arguments after --.",
			example: "  twt2 templates repos init set everysphere everysphere -- ./init.sh",
		},
		"twt2 templates init set": {
			long:    "Set a Project-level initialization command. The command runs after all repository worktrees exist.",
			example: "  twt2 templates init set product --cwd web -- ./scripts/init-project.sh",
		},
		"twt2 projects create": {
			long:    "Create a Project from a saved Project Template. twt2 creates the required worktrees, runs initialization, and creates the tmux session.",
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
		"twt2 projects open": {
			long: "Open a Project tmux session. If managed windows are missing, twt2 repairs them first.", example: "  twt2 projects open fix-auth\n  twt2 projects open fix-auth --no-attach",
		},
		"twt2 projects setup retry": {
			long: "Retry failed or interrupted setup steps from the saved Project Template snapshot.", example: "  twt2 projects setup retry fix-auth",
		},
		"twt2 projects remove": {
			long: "Show a safe removal plan. Add --apply to remove clean, published Project worktrees and state.", example: "  twt2 projects remove fix-auth\n  twt2 projects remove fix-auth --apply",
		},
		"twt2 agents register": {
			long: "Register a resumable coding Agent Session with a Project. Put the resume command after --.", example: "  twt2 agents register --project fix-auth --provider codex --label review -- codex resume SESSION_ID",
		},
		"twt2 agents list": {
			long: "List Agent Sessions for one Project, including status and capabilities.", example: "  twt2 agents list --project current --output json",
		},
		"twt2 agents resume": {
			long: "Focus a live Agent Session or start its saved resume command in a new Project window.", example: "  twt2 agents resume AGENT_ID",
		},
		"twt2 agents focus": {
			long: "Focus the tmux pane for a live Agent Session.", example: "  twt2 agents focus AGENT_ID",
		},
		"twt2 agents send": {
			long: "Send standard-input text to a live, owned Agent Session. twt2 never sends to an unverified shell pane.", example: "  printf '%s\\n' 'Please fix this review note.' | twt2 agents send AGENT_ID --stdin",
		},
		"twt2 context": {
			long: "Show the Project and repository context for a directory or the current tmux pane.", example: "  twt2 context --output json\n  twt2 context --directory /path/to/worktree --output json",
		},
		"twt2 storage status": {
			long: "Show the disk space used by Projects, worktrees, and shared repository caches.", example: "  twt2 storage status\n  twt2 storage status --output json",
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
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if value, ok := content[command.CommandPath()]; ok {
			command.Long = value.long
			command.Example = value.example
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(root)
}
