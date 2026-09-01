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
		long: "Create and maintain reusable YAML Workspace Templates. A template declares repositories, clone policy, tmux window names, and first-use initialization.", example: "  twt templates create everysphere\n  twt templates get everysphere",
	},
	"twt workspaces": {
		long: "Create and manage change-focused Workspaces. An active Workspace owns prepared worktrees and a tmux session. An archived Workspace keeps its branches and logical state.", example: "  twt create fix-auth --template everysphere\n  twt workspaces open fix-auth",
	},
	"twt create": {
		long: "Create a Workspace from a saved Workspace Template. This command is the short form of 'twt workspaces create'. The warm path claims prepared worktrees without a fetch. Use --fresh to fetch and refresh a ready Prepared Environment before the claim. Without NAME at a text terminal, twt asks for a Workspace name. Without --template at a text terminal, twt shows a Workspace Template picker when more than one Template exists. A script uses the last-used Template, or it passes --template. A pipe and --output json still require NAME.", example: "  twt create\n  twt create fix-auth --template everysphere\n  twt create fix-auth --template everysphere --fresh",
	},
	"twt next": {
		long: "Create the next Workspace from the latest saved version of the current Workspace Template. Run this command inside the current Workspace tmux session. twt checks the current Workspace, creates the new Workspace, and cleans the old Workspace. It then stops the old tmux session. Tmux can select another session or detach the client. Without a name, twt shows an interactive Ticket picker. Use --force to discard tracked and nonignored changes. Use --fresh to fetch before the new claim. Use twt create outside a current Workspace.", example: "  twt next\n  twt next fix-auth\n  twt next fix-auth --force",
	},
	"twt switch": {
		long: "Switch the calling tmux client to a Workspace. twt claims prepared worktrees for a released Workspace. It then creates or repairs the tmux session. Without WORKSPACE, twt shows an interactive Workspace picker.", example: "  twt switch fix-auth\n  twt switch",
	},
	"twt archive": {
		long: "Archive a Workspace and return its worktrees to the prepared pool. twt keeps its branches, Workspace Template snapshot, and Agent Session records. Use --force to discard tracked and nonignored changes. twt preserves ignored files.", example: "  twt archive\n  twt archive fix-auth --force",
	},
	"twt done": {
		long: "Finish a Workspace and return its worktrees to the prepared pool. twt keeps the Workspace record and branches. From its tmux session, twt completes cleanup in the caller pane. It then stops the complete session. Tmux can select another session or detach the client. Use --force to discard tracked and nonignored changes. When one open Ticket is linked, twt asks whether it must close that Ticket. Use 'twt workspaces remove' to delete Workspace state and branches.", example: "  twt done\n  twt done fix-auth --force",
	},
	"twt agents": {
		long: "Register and control coding Agent Sessions that belong to a Workspace. Feedback delivery works only for a verified, directly started Agent process.", example: "  twt agents list --workspace current\n  twt agents open\n  twt agents resume AGENT_ID",
	},
	"twt daemon": {
		long: "Manage the background pool refresh daemon. The daemon is a launchd agent that runs 'twt daemon run' on an interval, so ready Prepared Environments stay near the tip of the default branch and warm claims start recent work.", example: "  twt daemon install\n  twt daemon run",
	},
	"twt daemon install": {
		long: "Install or replace the launchd agent that refreshes the Prepared Environment pools. The agent runs 'twt daemon run' every --interval (default 10m) and logs to the twt state directory. The plist bakes the current twt executable path, the resolved twt directories, and PATH, because launchd starts jobs with a minimal environment.", example: "  twt daemon install\n  twt daemon install --interval 15m",
	},
	"twt daemon uninstall": {
		long: "Unload and remove the launchd agent that refreshes the Prepared Environment pools.", example: "  twt daemon uninstall",
	},
	"twt daemon run": {
		long: "Run one pool refresh pass: for every Workspace Template with repositories, refresh each ready Prepared Environment and top the pool up to pool_depth. One failed Workspace Template does not stop the others. The launchd agent runs this command; run it yourself for an immediate refresh.", example: "  twt daemon run\n  twt daemon run --output json",
	},
	"twt storage": {
		long: "Inspect the disk space used by twt Workspaces, worktrees, and shared repository caches.", example: "  twt storage get",
	},
	"twt environments": {
		long: "Inspect the Prepared Environments that twt keeps for the next Workspace. A Prepared Environment holds initialized worktrees before a Workspace claims them.", example: "  twt environments list\n  twt environments get ENVIRONMENT_ID",
	},
	"twt templates create": {
		long: "Create an empty Workspace Template.\n\nNAME is the reusable template name. After creation, add one or more " +
			"repository specifications.\n\nWith --from-file, twt reads one strict Workspace Template " +
			"YAML document. Pass - as the --from-file value to read standard input. NAME stays required: it sets the template name, and a different name in the document is an error.",
		example: "  twt templates create everysphere\n  twt templates repos add everysphere app git@github.com:acme/app.git\n  twt templates create everysphere --from-file ./everysphere.yaml\n  cat ./everysphere.yaml | twt templates create everysphere --from-file -",
	},
	"twt templates list": {
		long: "List all saved Workspace Templates.", example: "  twt templates list\n  twt templates list --output json",
	},
	"twt templates get": {
		long: "Get one Workspace Template and its repository specifications.", example: "  twt templates get everysphere\n  twt templates get everysphere --output json",
	},
	"twt templates validate": {
		long: "Validate the YAML and all fields in one Workspace Template.", example: "  twt templates validate everysphere",
	},
	"twt templates prepare": {
		long: "Refresh each matching ready Prepared Environment. Then create enough Prepared Environments to meet pool_depth. Repository initialization runs again only when a refresh changes the saved base commit.", example: "  twt templates prepare everysphere",
	},
	"twt templates repos": {
		long: "Manage the repository specifications inside one Workspace Template.", example: "  twt templates repos add everysphere app https://example.com/app.git\n  twt templates repos remove everysphere app",
	},
	"twt templates init": {
		long: "Manage the initialization commands of a Workspace Template. One command runs for the Workspace, and one command runs for each repository.", example: "  twt templates init set product --cwd web -- ./scripts/init-workspace.sh\n  twt templates init set product --repo web -- ./init.sh",
	},
	"twt templates recycle": {
		long: "Manage the command that cleans ignored files before twt reuses a repository worktree.", example: "  twt templates recycle set product --repo app -- ./clean.sh",
	},
	"twt templates recycle set": {
		long: "Set one repository recycle command. twt runs it before the worktree returns to the prepared pool.", example: "  twt templates recycle set product --repo app -- ./clean.sh",
	},
	"twt templates recycle unset": {
		long: "Remove one repository recycle command.", example: "  twt templates recycle unset product --repo app",
	},
	"twt templates path": {
		long: "Print the YAML file path of one Workspace Template. The output is one bare path for command substitution.", example: "  twt templates path everysphere\n  $EDITOR $(twt templates path everysphere)",
	},
	"twt templates edit": {
		long: "Open the Workspace Template YAML file in VISUAL or EDITOR, then validate the result. An invalid file stays on disk and twt reports the unsafe_state error.", example: "  twt templates edit everysphere",
	},
	"twt templates remove": {
		long: "Delete the YAML file of a Workspace Template. twt refuses removal while a Workspace record still uses the Workspace Template.", example: "  twt templates remove everysphere\n  twt templates remove everysphere --dry-run --output json",
	},
	"twt templates repos remove": {
		long: "Remove one repository specification from a Workspace Template. Existing Workspaces keep their saved Workspace Template snapshot.", example: "  twt templates repos remove everysphere app",
	},
	"twt workspaces setup": {
		long: "Manage Workspace setup steps.", example: "  twt workspaces setup retry fix-auth",
	},
	"twt agents transcript": {
		long: "Read, link, and snapshot provider transcripts for Agent Sessions.", example: "  twt agents transcript get AGENT_ID --workspace current",
	},
	"twt templates repos add": {
		long:    "Add one repository specification to a Workspace Template. Flags define remotes, the default branch, and the tmux window name. Repository Caches always keep full commit history so Workspace branches keep valid ancestry.",
		example: "  twt templates repos add everysphere everysphere \\\n    https://origin.cursor.com/anysphere/everysphere.git \\\n    --remote github=https://github.com/anysphere/everysphere.git",
	},
	"twt templates init set": {
		long: "Set one initialization command. Put the command and its arguments after --.\n\n" +
			"With --repo REPO, the command is repository initialization. twt runs it after preparation and after a base refresh.\n\n" +
			"Without --repo, the command is Workspace initialization: twt runs it after all repository worktrees exist, and --cwd PATH must give its working directory inside the Workspace root.",
		example: "  twt templates init set product --cwd web -- ./scripts/init-workspace.sh\n  twt templates init set product --repo web -- ./init.sh",
	},
	"twt workspaces create": {
		long:    "Create a Workspace from a saved Workspace Template. The warm path claims a ready Prepared Environment without a fetch. Use --fresh to fetch and refresh it first. Repeat --ticket to link open Tickets from one Project. The branch name comes from --branch, the template branch_pattern, or the default pattern {prefix}{name}. Without NAME at a text terminal, twt asks for a Workspace name. Without --template at a text terminal, twt shows a Workspace Template picker when more than one Template exists. A script uses the last-used Template, or it passes --template. A pipe and --output json still require NAME.",
		example: "  twt workspaces create\n  twt workspaces create fix-auth --template everysphere\n  twt workspaces create fix-auth --template everysphere --fresh",
	},
	"twt workspaces list": {
		long: "List Workspaces and their setup state. --project, --ticket, and --status filter the list. JSON includes the linked Project and Ticket slugs.", example: "  twt workspaces list\n  twt workspaces list --project change-monitor --status active --output json\n  twt workspaces list --ticket fix-auth --output json",
	},
	"twt workspaces get": {
		long: "Get one Workspace by name or immutable ID. Without WORKSPACE, twt gets the current Workspace from the tmux pane or the working directory.", example: "  twt workspaces get\n  twt workspaces get fix-auth --output json",
	},
	"twt workspaces rename": {
		long: "Change the display name of a Workspace. twt also renames the owned tmux session to match. The Workspace ID, root, checkouts, branches, and Agent Sessions do not change. One NAME argument renames the current Workspace. Two arguments set the Workspace and the new name. Without arguments, twt shows the Workspace picker and asks for the new name.", example: "  twt workspaces rename auth-fix\n  twt workspaces rename fix-auth auth-fix\n  twt workspaces rename",
	},
	"twt workspaces set": {
		long: "Set the Ticket Project on one Workspace. The Project must be active. When the Workspace links Tickets, every Ticket must already belong to that Project. twt does not move Tickets, checkouts, or Environments.", example: "  twt workspaces set current --project change-monitor\n  twt workspaces set fix-auth --project change-monitor --dry-run --output json",
	},
	"twt workspaces current": {
		long: "Find the Workspace for the current directory or tmux pane.", example: "  twt workspaces current",
	},
	"twt workspaces path": {
		long: "Print the root path of a Workspace, or the checkout path of one repository in it. The output is one bare path for command substitution.", example: "  cd $(twt workspaces path fix-auth)\n  cd $(twt workspaces path fix-auth app)",
	},
	"twt workspaces open": {
		long: "Open a Workspace tmux session. An archived Workspace claims matching prepared worktrees and restores its saved branches. Workspace Initialization runs again. twt claims an unowned tmux session with the expected name. It also repairs missing managed windows. --all-active repairs each active Workspace and attaches no client.", example: "  twt workspaces open fix-auth\n  twt workspaces open fix-auth --no-attach\n  twt workspaces open --all-active",
	},
	"twt workspaces archive": {
		long: "Archive a Workspace and return its worktrees to the prepared pool. twt keeps its branches and logical state. Use --force to discard tracked and nonignored changes. twt preserves ignored files.", example: "  twt workspaces archive fix-auth\n  twt workspaces open fix-auth",
	},
	"twt workspaces setup retry": {
		long: "Retry failed or interrupted setup steps from the saved Workspace Template snapshot.", example: "  twt workspaces setup retry fix-auth",
	},
	"twt workspaces adopt": {
		long: "Adopt an existing tmux session as a Workspace. twt records the git repositories that the panes of the session sit in, and marks the session with the Workspace ID. twt did not create the directories of an adopted Workspace, and removal never deletes them: removal deletes only the twt state and releases the session marker.", example: "  twt workspaces adopt\n  twt workspaces adopt my-session --name fix-auth",
	},
	"twt workspaces remove": {
		long: "Show a removal plan for an archived Workspace. Add --apply to remove its saved branches and state. Removal deletes the Workspace branches, unpublished commits included, and never reads the remote. A released Workspace does not remove its ready Prepared Environment. Use --all-archived with --older-than to select archived Workspaces.", example: "  twt workspaces archive fix-auth\n  twt workspaces remove fix-auth --apply\n  twt workspaces remove --all-archived --older-than 14d --apply",
	},
	"twt agents register": {
		long: "Register a resumable coding Agent Session with a Workspace. Put the resume command after --. twt infers the provider and the provider session ID from that command. Use --provider and --session to set them yourself.", example: "  twt agents register -- codex resume SESSION_ID\n  twt agents register --workspace fix-auth --label review -- grok --resume SESSION_ID",
	},
	"twt agents adopt": {
		long: "Register one discovered Agent Session. The reference can name a verified provider transcript or a verified live provider process. A dry run checks the same evidence and does not write state or claim a pane.", example: "  twt agents adopt AGENT_ID --workspace current\n  twt agents adopt AGENT_ID --workspace current --dry-run --output json",
	},
	"twt agents list": {
		long: "List Agent Sessions for one Workspace, newest first. Text output is provider, ID, and age. twt finds verified Codex, Claude Code, Cursor Agent, and Grok processes in live Workspace panes. It also scans the Codex, Claude, and Grok transcript stores. A discovered session has a provider-qualified candidate ID and status \"discovered\". Adopt, resume, open, or send registers it. List and preview do not write state. Use --registered to list saved Agent Sessions only. Use --live=false for a cheap read that does not probe tmux or scan provider stores.", example: "  twt agents list --workspace current --output json\n  twt agents list --workspace current --registered\n  twt agents list --workspace current --live=false",
	},
	"twt agents get": {
		long: "Get one Agent Session record and each liveness check. A failed check tells you why twt does not send feedback. For an adopted shell-hosted process, twt checks the saved process ID, start time, provider, and current input target.", example: "  twt agents get AGENT_ID\n  twt agents get AGENT_ID --workspace current --output json",
	},
	"twt agents discover": {
		long: "Find the Codex, Claude, and Grok sessions that ran inside a repository of the Workspace and that no Agent Session uses. The newest session comes first. Add --adopt to register each session with a resume command. 'twt agents list' also shows provider-qualified references for these sessions. Use discover --adopt for bulk adoption.", example: "  twt agents discover --workspace current\n  twt agents discover --workspace current --adopt --limit 3",
	},
	"twt agents rm": {
		long: "Delete an Agent Session record. twt keeps the provider transcript and does not stop a live Agent process.", example: "  twt agents rm AGENT_ID\n  twt agents rm AGENT_ID --dry-run --output json",
	},
	"twt agents resume": {
		long: "Focus a live Agent Session or start its saved resume command in a new Workspace window. A discovered provider session ID also resumes: twt registers it first.", example: "  twt agents resume AGENT_ID\n  twt agents resume PROVIDER_SESSION_ID",
	},
	"twt agents focus": {
		long: "Focus the tmux pane for a live Agent Session.", example: "  twt agents focus AGENT_ID",
	},
	"twt agents open": {
		long: "Open an Agent Session of the selected Workspace. A live session is focused. A stopped session starts its saved provider resume command in the current pane. Without AGENT_ID, twt shows an interactive picker. The fzf preview shows a verified transcript or the bounded visible screen of a verified live pane. --preview is read-only and never registers a session or writes a snapshot.", example: "  twt agents open\n  twt agents open AGENT_ID\n  twt agents open --preview AGENT_ID --workspace current",
	},
	"twt agents send": {
		long: "Send standard-input text to a live, owned Agent Session in the selected Workspace. twt can adopt a verified provider process below a shell. Before each send, it checks the saved process identity and the current input target. Pass - after AGENT_ID to read the text from standard input.", example: "  printf '%s\\n' 'Please fix this review note.' | twt agents send AGENT_ID - --workspace current",
	},
	"twt agents transcript get": {
		long: "Read the provider transcript linked to one Agent Session. twt checks that the transcript belongs to the selected Workspace and does not return its source path.", example: "  twt agents transcript get AGENT_ID --workspace current --output json",
	},
	"twt agents transcript snapshot": {
		long: "Read a linked provider transcript and save a private Workspace-owned Markdown snapshot. Each Agent Session has its own file, and twt also writes latest.md as a copy of the most recent snapshot. The result gives the file path. Workspace removal deletes these snapshots.", example: "  twt agents transcript snapshot AGENT_ID --workspace current --output json",
	},
	"twt agents transcript link": {
		long: "Link an existing Agent Session to its provider session ID. This enables transcript loading without changing its resume command.", example: "  twt agents transcript link AGENT_ID --workspace current --session SESSION_ID",
	},
	"twt tickets": {
		long: "Create and manage Markdown tickets in the configured Tickets home. Each Ticket is one Obsidian note with YAML frontmatter, and the CLI owns every mutation.", example: "  twt tickets create \"fix the vfs tools\" --project change-monitor\n  twt tickets list --ready --output json",
	},
	"twt tickets init": {
		long: "Create the Tickets home directory with its hub index.md and its create template. twt writes each file only when that file is missing. It never overwrites notes.", example: "  twt tickets init\n  twt tickets init --dry-run --output json",
	},
	"twt tickets home": {
		long: "Open the Tickets home directory in VISUAL or EDITOR. Tickets home is the ticketsHome value of config.yaml, or TWT_TICKETS_HOME.", example: "  twt tickets home",
	},
	"twt tickets create": {
		long: "Create one Ticket file. DESCRIPTION becomes the body, and its first line becomes the title when --title is absent. A lone - reads the body from standard input. - requires --title. With no DESCRIPTION and no - in an interactive terminal, twt asks for a title, then a Project, then opens VISUAL or EDITOR on an empty file for the description. A typed Project name that does not exist is created only after confirm. --project never creates a missing Project. --label writes a loose theme. Repeat the flag. --label never creates a Project. --blocked-by writes blocked_by as wiki-links. Repeat the flag. Each value may be a slug or a wiki-link.", example: "  twt tickets create\n  twt tickets create \"fix the vfs tools\" --project change-monitor --output json\n  twt tickets create \"follow-up work\" --status ready-for-agent --blocked-by fix-the-vfs-tools --output json\n  twt tickets create \"spike the monitor\" --label change-monitor --output json\n  printf '%s\\n' 'Steps...' | twt tickets create - --title \"Fix auth\"",
	},
	"twt tickets list": {
		long: "List Tickets sorted by priority, then by slug. The list uses --project, then TWT_PROJECT, then the current Workspace Project. With no Project in scope, the list includes every Project. A scoped text list is a simple table. --all-projects lists every Project even when a Workspace Project is set.\n\nA wide text table has a PROJECT column. JSON stays a flat array. The list hides done and wontfix Tickets by default. --all includes them, and an explicit --status shows that status. --ready lists only pickable work: ready-for-agent, unclaimed, and with every blocker done or wontfix. --claimed lists Tickets that have a claimant. --label keeps Tickets that carry that label. Repeat the flag to require every named label. --label does not change Project scope. Pass --all-projects for a cross-Project label feed.\n\n--status is a raw filter. Do not use it together with --ready. --project and --all-projects cannot be used together.", example: "  twt tickets list --ready --output json\n  twt tickets list -A --claimed --output json\n  twt tickets list --label change-monitor -A --output json\n  twt tickets list --project change-monitor --limit 10",
	},
	"twt tickets queue": {
		long: "Show the complete open Ticket dependency graph for one Project. The Project comes from --project, then TWT_PROJECT, then the current Workspace Project. ready contains only ready-for-agent, unclaimed Tickets whose blockers are done or wontfix. The command sorts graph and ready by priority and then by Ticket slug. --limit cuts ready but does not cut graph.\n\ncycles reports dependency cycles that stop affected Tickets from becoming ready.", example: "  twt tickets queue --project change-monitor --output json\n  twt tickets queue --project change-monitor --limit 4 --fields ready,readyTotalCount --output json",
	},
	"twt tickets dispatch": {
		long: "Start one autonomous implementation Session for a ready Ticket: twt claims the Ticket, creates a Workspace, and starts the implementation agent in tmux. Agent mode implements the Ticket and creates pull requests; --plan starts a planning run instead. A dispatch creates a Workspace, so it can take minutes when no Prepared Environment is ready, and dispatches serialize per wave. A launch failure returns the Ticket to the queue and names the partial Workspace.", example: "  twt tickets dispatch canonical-pr-comment --dry-run --output json\n  twt tickets dispatch canonical-pr-comment --output json",
	},
	"twt tickets get": {
		long: "Get one Ticket with its metadata, its labels, its open blockers, and its body. TICKET accepts a slug, a unique slug prefix, a title, an alias, a wiki-link, or a path under the Tickets home.", example: "  twt tickets get fix-the-vfs-tools\n  twt tickets get '[[fix-the-vfs-tools]]' --output json",
	},
	"twt tickets set": {
		long: "Change the status, the priority, the Project, blocked_by, or labels of one Ticket. Pass at least one flag. A Project change moves the Ticket file into the Project directory. An empty --project ungroups the Ticket. Label changes do not move the file. --label replaces the whole label list. Pass an empty value to clear it. --add-label and --remove-label change the current list. Do not mix --label with --add-label or --remove-label. --blocked-by replaces the whole blocker list. Pass an empty value to clear it.", example: "  twt tickets set fix-the-vfs-tools --status done\n  twt tickets set follow-up-work --blocked-by fix-the-vfs-tools\n  twt tickets set follow-up-work --blocked-by \"\"\n  twt tickets set fix-the-vfs-tools --priority 1 --project change-monitor\n  twt tickets set spike-the-monitor --add-label change-monitor\n  twt tickets set spike-the-monitor --project \"\"",
	},
	"twt labels": {
		long: "Manage labels across Tickets. A label is a loose theme. It is not a Project. twt does not keep a label registry. The files are the store. Add, remove, and rename rewrite Ticket frontmatter and do not move files.", example: "  twt labels list --output json\n  twt labels add change-monitor --ticket spike-the-monitor --output json\n  twt labels remove change-monitor --output json\n  twt labels rename change-monitor monitor-theme --output json",
	},
	"twt labels list": {
		long: "List unique labels derived from Ticket files. The default list reads open Tickets. --all includes labels that appear only on closed Tickets. Each row shows the label name and the Ticket count in that set.", example: "  twt labels list --output json\n  twt labels list --all --limit 20 --output json",
	},
	"twt labels add": {
		long: "Add one label to one or more Tickets. Repeat --ticket. The write does not move a file and does not create a Project.", example: "  twt labels add change-monitor --ticket spike-the-monitor --dry-run --output json\n  twt labels add change-monitor --ticket spike-the-monitor --ticket feature-work --output json",
	},
	"twt labels remove": {
		long: "Remove one label from Tickets. Without --ticket, twt removes the label from every Ticket that carries it, including closed Tickets. --ticket limits the write to those Tickets.", example: "  twt labels remove change-monitor --dry-run --output json\n  twt labels remove change-monitor --output json\n  twt labels remove change-monitor --ticket spike-the-monitor --output json",
	},
	"twt labels rename": {
		long: "Rename one label on every Ticket that carries it, including closed Tickets. The write does not move a file. A Ticket that already has the new name keeps one copy.", example: "  twt labels rename change-monitor monitor-theme --dry-run --output json\n  twt labels rename change-monitor monitor-theme --output json",
	},
	"twt tickets claim": {
		long: "Claim one Ticket for a work session. The claimant comes from --as, then TWT_CLAIMANT, then the OS username in an interactive terminal. A Ticket that a different claimant holds returns the locked error. --workspace stamps the Workspace ID on the Ticket so a later coordinator read can join Ticket to Workspace.", example: "  twt tickets claim fix-the-vfs-tools --as codex-fix-auth\n  twt tickets claim fix-the-vfs-tools --as codex-fix-auth --workspace current --output json",
	},
	"twt tickets start": {
		long: "Claim one or more Tickets and start one Workspace for them. All Tickets must be open and belong to one Project. twt claims every Ticket before Workspace work starts. The Workspace name is --name, or the first Ticket slug. --with-agent adds one planning Agent Session for all selected Tickets. ticketAgent in config.yaml selects its provider, effort, and custom instructions. The default is Codex with large effort. --detached or -d starts the complete Workspace without opening or switching tmux. The detached form accepts JSON when the command has explicit Ticket arguments. On success twt appends a start comment to each Ticket. A create or Agent start failure keeps the claims. Without --detached, twt switches to the new Workspace and keeps the current Workspace active. Use 'twt next TICKET' when the current Workspace must be archived. Without TICKET, twt shows an interactive Ticket picker. The picker offers the current Project: TWT_PROJECT, then the current Workspace Project. Use --all-projects or -A to offer every Project. The picker uses fzf when fzf is installed, or it uses a numbered list. The fzf preview shows the Ticket contents from 'twt tickets get'. Without --template, a start outside a Workspace uses the Project Template, then a Template picker at a text terminal, then the last-used Template in a script. A script must pass TICKET.", example: "  twt tickets start\n  twt tickets start -A\n  twt tickets start fix-auth-tokens\n  twt tickets start fix-auth-tokens add-auth-tests --name auth-fix\n  twt tickets start fix-auth-tokens --with-agent --detached --as coordinator --output json",
	},
	"twt tickets unclaim": {
		long: "Remove the claim on one Ticket. The claimant resolution is the same as claim, and only the current claimant can remove its claim. An unclaimed Ticket succeeds without a change.", example: "  twt tickets unclaim fix-the-vfs-tools --as codex-fix-auth",
	},
	"twt tickets close": {
		long: "Resolve one Ticket in one write: the status becomes done and the claim fields become empty. The claimant resolution is the same as claim, and a Ticket that a different claimant holds returns the locked error. Use 'twt tickets set --status' and 'twt tickets unclaim' when you need only one of the two changes.", example: "  twt tickets close fix-the-vfs-tools --as codex-fix-auth\n  twt tickets close fix-the-vfs-tools --as codex-fix-auth --output json",
	},
	"twt tickets comment": {
		long: "Append one comment from standard input under the '## Comments' heading of a Ticket. twt creates that heading when it is missing. Pass - after TICKET to read the comment from standard input.", example: "  printf '%s\\n' 'Shipped in PR 42.' | twt tickets comment fix-the-vfs-tools -",
	},
	"twt tickets doctor": {
		long: "Check every Ticket file. Report invalid files, duplicate slugs, closed-directory conflicts, and Tickets outside the correct active or closed location. This command never writes files.", example: "  twt tickets doctor\n  twt tickets doctor --output json",
	},
	"twt projects plan": {
		long: "Write the plan document of a Project. The file is plan.md beside the Project tickets. It is visible in Obsidian and git-synced. The plan is the top-level design that the human and the PM agent iterate. The ticket DAG mirrors it. The write is an upsert: it creates plan.md when missing. There is no required structure.\n\nWith no subcommand, twt writes the plan of the named Project, or of the current Project when PROJECT is absent. The current Project comes from TWT_PROJECT, then the current Workspace Project. The argument - reads the content from standard input. In an interactive terminal without -, twt opens VISUAL or EDITOR. A missing plan.md opens a blank file. The save creates the file. Edits through twt trigger the tickets git sync. Do not edit plan.md on disk from an agent.", example: "  twt projects plan change-monitor\n  twt projects plan\n  printf '%s' \"$PLAN\" | twt projects plan change-monitor - --output json\n  twt projects plan get change-monitor --output json\n  twt projects plan path change-monitor",
	},
	"twt projects plan get": {
		long: "Print the plan document of a Project. JSON includes the content, path, and updated time. A missing plan is not_found. Write it with twt projects plan PROJECT.", example: "  twt projects plan get change-monitor\n  twt projects plan get change-monitor --output json",
	},
	"twt projects plan path": {
		long: "Print the plan document path for editor use. The file itself may not exist yet.", example: "  nvim \"$(twt projects plan path change-monitor)\"",
	},
	"twt tickets ask": {
		long: "Ask the human a question from a working agent: the question lands under the Ticket's ## Questions section, the status parks on needs-info, and the claim stays. The prior status is remembered and restored by answer. Requires the matching --as claimant. After asking, end the turn and wait; the answer arrives as the next message.", example: "  printf '%s' \"Which OAuth provider?\" | twt tickets ask fix-auth - --as twt-local-01234567 --output json",
	},
	"twt tickets answer": {
		long: "Answer a Ticket that waits on input: the reply lands under ## Questions, the pre-ask status is restored, and the answer is relayed into the asking agent's live tmux pane when one exists on this machine (best-effort; the ticket record is the durable copy). Agents may also run answer to record a reply the human gave in the pane directly.", example: "  printf '%s' \"Use OAuth.\" | twt tickets answer fix-auth - --output json\n  printf '%s' \"Use OAuth.\" | twt tickets answer fix-auth - --agent AGENT_ID --output json",
	},
	"twt tickets approve": {
		long: "Approve a Ticket's ## Plan section for implementation. The approval stamps plan_approved_by and plan_approved_at; implementation dispatch refuses a planned Ticket without the stamp. When the Ticket waits on the planning agent's approval ask, approve also acts as the answer: it restores the pre-ask status and relays into the live session. A plan rewrite clears the approval.", example: "  twt tickets approve fix-auth --output json\n  printf '%s' \"Ship it; keep the scope small.\" | twt tickets approve fix-auth - --output json",
	},
	"twt tickets tree": {
		long: "Render the Project's dependency graph as a tree, children being the Tickets a node unblocks. Each node shows its derived state (ready, blocked, in-progress, needs-input, in-review, done), claimant, and a PR badge from live forge state (cached ~120s; --no-fetch uses only the cache). --all includes closed Tickets. Cycles print flat below the tree.", example: "  twt tickets tree --project change-monitor --output json\n  twt tickets tree --project change-monitor --all\n  twt tickets tree --project change-monitor --no-fetch",
	},
	"twt tickets pr": {
		long: "Manage the pull request URLs linked to a Ticket. URLs are the canonical record in the ticket frontmatter; live PR state is fetched on demand by the board and tree views.", example: "  twt tickets pr add fix-auth --pr https://origin.cursor.com/acme/api/pull/7 --as CLAIMANT --output json",
	},
	"twt tickets pr add": {
		long: "Attach pull request URLs to a Ticket without changing its status or claim. Run it the moment the PR exists so the board tracks it; tickets complete later dedupes the same URLs. A claimed Ticket requires the matching --as claimant.", example: "  twt tickets pr add fix-auth --pr https://origin.cursor.com/acme/api/pull/7 --as twt-local-01234567 --output json",
	},
	"twt tickets pr rm": {
		long: "Detach pull request URLs from a Ticket. Removing an absent URL is a no-op. A claimed Ticket requires the matching --as claimant.", example: "  twt tickets pr rm fix-auth --pr https://origin.cursor.com/acme/api/pull/7 --output json",
	},
	"twt tickets plan": {
		long: "Replace the ## Plan section of one Ticket. twt keeps every other section. The argument - reads the plan from standard input. In an interactive terminal without -, twt opens VISUAL or EDITOR on a draft of the current plan and writes the saved result. Planning agents write their decision-complete plan here before implementation. A claimed Ticket requires the matching --as claimant.", example: "  twt tickets plan fix-auth\n  printf '%s' \"$PLAN\" | twt tickets plan fix-auth - --as codex-fix-auth --output json\n  printf '%s' \"$PLAN\" | twt tickets plan fix-auth - --dry-run --output json",
	},
	"twt tickets complete": {
		long: "Record pull request URLs and release the claim in one write, so the URL write can never race a new claimant. This is the worker's terminal command: run it when the Ticket's work ships. The default status ready-for-human hands the Ticket to review; ready-for-agent returns it to the queue. A retry after success is a no-op.", example: "  twt tickets complete fix-auth --as twt-local-01234567 --pr https://origin.cursor.com/acme/api/pull/7 --output json\n  twt tickets complete fix-auth --as twt-local-01234567 --status ready-for-agent --output json",
	},
	"twt tickets sync": {
		long: "Reconcile the Tickets home with its git remote (commit manual edits, pull, rebase, push), and then, with --project, reconcile that Project's dispatch Sessions with Ticket states. The Session reconciler joins session records, agent liveness, and ticket claims: a stopped agent with a held claim becomes a stuck diagnostic and is never auto-released. Any diagnostic sets capacity.known to false; dispatch only with known capacity. Without ticketsSync the store phase is a reported no-op.", example: "  twt tickets sync --output json\n  twt tickets sync --project core --dry-run --output json\n  twt tickets sync --project core --output json",
	},
	"twt tickets abandon": {
		long: "Stop recovery for one local dispatch Session. Abandon makes the Session terminal and returns the Ticket to ready-for-agent only when the Session's own claimant still holds it. It never stops tmux: the Workspace and its agent keep running until 'twt done'. Use it with user authority after 'twt tickets sync' reports a stuck Session that resume cannot fix.", example: "  twt tickets abandon 0123abcd --force --dry-run --output json\n  twt tickets abandon 0123abcd --force --output json",
	},
	"twt tickets repair": {
		long: "Move Tickets to the correct active or closed location from the current status and Project directory. Repair applies no move while the doctor report has a blocker. Run --dry-run first.", example: "  twt tickets repair --dry-run --output json\n  twt tickets repair --output json",
	},
	"twt projects": {
		long: "Manage Projects. A Project is one directory under the Tickets home that groups Tickets and outlives any Workspace.", example: "  twt projects create change-monitor\n  twt projects list --output json",
	},
	"twt projects create": {
		long: "Create one Project directory and write its index.md only when that file is missing. With no NAME in an interactive terminal, twt asks for a Project name, then opens VISUAL or EDITOR on an empty file for the plan. After you save the plan, twt shows a Workspace Template picker when --template is absent and more than one Template exists. It then asks whether to start a Workspace. A script must pass NAME.", example: "  twt projects create\n  twt projects create change-monitor\n  twt projects create change-monitor --template everysphere --output json",
	},
	"twt projects remove": {
		long: "Show a removal plan for one Project. Add --apply to delete the Project directory, its plan, and its Ticket files, including closed Tickets under closed/NAME. After apply, the name can be created again. A Workspace that still names the Project is a blocker. Archive that Workspace, then run 'twt workspaces remove WORKSPACE --apply'. Close keeps history. Remove deletes the Project. Do not use --dry-run with --apply.", example: "  twt projects remove change-monitor\n  twt projects remove change-monitor --apply --output json",
	},
	"twt projects rename": {
		long: "Rename one Project. twt moves the Project directory and its closed Ticket tree. It heals Ticket project frontmatter from the new path. It also retargets Workspaces that still name the old Project. The new name must be free and must not be reserved. Rename works on a closed Project.", example: "  twt projects rename old-name new-name\n  twt projects rename old-name new-name --dry-run --output json",
	},
	"twt projects close": {
		long: "Close one Project. With no open Tickets, the command closes it immediately. At a text terminal, twt asks before it sets open Tickets to wontfix. A script must pass --force when open Tickets remain. Close clears each affected Ticket claim and Workspace link. It does not stop Workspaces or agents. It keeps the Project directory, index.md, and plan.md. Closed Projects do not appear in Project lists or completion.", example: "  twt projects close change-monitor\n  twt projects close change-monitor --force --output json",
	},
	"twt projects list": {
		long: "List every active Project with its Ticket count.", example: "  twt projects list\n  twt projects list --output json",
	},
	"twt projects get": {
		long: "Get one Project as the coordinator board: Tickets waiting on the human, in progress (with the newest dispatch Session each), in review (with live pull request state and an all-merged close marker), ready, blocked, done, and the linked Workspaces. Without NAME, twt gets the current Project: TWT_PROJECT, then the current Workspace Project. Sessions come from the last sync, never from a live probe; storeAsOf reports the store freshness. --no-fetch uses only cached pull request state.", example: "  twt projects get\n  twt projects get change-monitor --output json\n  twt projects get change-monitor --no-fetch --output json",
	},
	"twt projects set": {
		long: "Set the Workspace Template that a Project uses for future Workspaces and dispatch Sessions. Existing Workspaces keep their saved Template snapshots.", example: "  twt projects set change-monitor --template product\n  twt projects set change-monitor --template product --dry-run --output json",
	},
	"twt context": {
		long: "Show the Workspace, tmux session, and repository context for a directory or the current tmux pane. When Tickets home is set, JSON also lists the linked Tickets and the ready queue for the Workspace Project.", example: "  twt context --output json\n  twt context --directory /path/to/worktree --output json",
	},
	"twt config": {
		long: "Show every resolved twt setting, including defaults. Each setting reports its value and its source: env for an environment variable, file for config.yaml, or default.", example: "  twt config\n  twt config --output json",
	},
	"twt storage get": {
		long: "Get the disk space used by active Workspaces, archived Workspaces, Prepared Environments, worktrees, and shared repository caches.", example: "  twt storage get\n  twt storage get --output json",
	},
	"twt storage clean": {
		long: "Show a safe cleanup plan for failed and obsolete Prepared Environments, orphan Transcript Snapshots, and orphan Agent Session records. Add --apply to remove only twt-owned data.", example: "  twt storage clean\n  twt storage clean --apply",
	},
	"twt environments list": {
		long: "List the Prepared Environments of each Workspace Template, with status, age, and disk space. The list shows releasing while an Environment waits for source session confirmation. The next Workspace claim completes that confirmation. A ready Prepared Environment that no longer matches its Workspace Template has status obsolete.", example: "  twt environments list\n  twt environments list --limit 10 --output json",
	},
	"twt environments get": {
		long: "Get one Prepared Environment, its preparation steps, its base commit for each repository, and the Workspace that claims it. ENVIRONMENT_ID accepts a unique ID prefix.", example: "  twt environments get 1a2b3c4d\n  twt environments get ENVIRONMENT_ID --output json",
	},
	"twt doctor": {
		long: "Check required tools, Workspace Templates, Workspace state, ownership markers, and tmux session drift. A missing or unowned session of an active Workspace is a warning. Repair it with 'twt workspaces open --all-active --no-attach'.", example: "  twt doctor\n  twt doctor --output json",
	},
	"twt skills": {
		long: "Install the twt agent skill that this build carries. The skill tells an agent how to call twt: JSON output, dry runs, limits, and untrusted transcript text.", example: "  twt skills install\n  twt skills get",
	},
	"twt skills install": {
		long: "Write the twt agent skill of this build into each skill tree. Without --dir, twt writes ~/.cursor/skills/twt/SKILL.md, ~/.claude/skills/twt/SKILL.md, and ~/.agents/skills/twt/SKILL.md. Each copy is a real file with a version stamp, not a symlink, so the skill stays correct when the repository checkout moves. Run this command again after a twt upgrade.", example: "  twt skills install\n  twt skills install --dir ./skills --dry-run --output json",
	},
	"twt skills get": {
		long: "Print the twt agent skill of this build, with the version stamp that install writes.", example: "  twt skills get\n  twt skills get --output json",
	},
	"twt schema": {
		long: "Show the versioned machine-readable schema for commands, arguments, flags, and raw apply operations.", example: "  twt schema | jq .",
	},
	"twt apply": {
		long: "Read one strict JSON mutation request from standard input. Pass - to read that request. Unknown fields and extra JSON values cause an error.", example: `  printf '%s' '{"operation":"templates.create","template":{"name":"demo"}}' | twt apply - --dry-run --output json`,
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
