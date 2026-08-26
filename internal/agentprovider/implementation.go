package agentprovider

import (
	"fmt"
	"os/exec"
	"strings"
)

// TicketImplementationRequest contains the provider-neutral implementation
// input. An implementation agent works on exactly one Ticket.
type TicketImplementationRequest struct {
	Provider     string
	Effort       TicketPlanningEffort
	Instructions string
	Ticket       string
	// Claimant identifies the worker in the ticket claim. The prompt tells
	// the agent to report through twt as this claimant.
	Claimant string
}

// BuildTicketImplementationLaunch validates a request and returns direct
// argv for an autonomous implementation run. The launch shape matches the
// planning launch: Start carries the prompt, Resume never does.
func BuildTicketImplementationLaunch(request TicketImplementationRequest, lookPath func(string) (string, error)) (TicketPlanningLaunch, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if !isTicketPlanningProvider(request.Provider) {
		return TicketPlanningLaunch{}, fmt.Errorf("unsupported Ticket implementation provider %q", request.Provider)
	}
	level, err := request.Effort.ProviderLevel()
	if err != nil {
		return TicketPlanningLaunch{}, err
	}
	if strings.TrimSpace(request.Ticket) == "" {
		return TicketPlanningLaunch{}, fmt.Errorf("a Ticket is required for an implementation agent")
	}
	if strings.TrimSpace(request.Claimant) == "" {
		return TicketPlanningLaunch{}, fmt.Errorf("a claimant is required for an implementation agent")
	}
	executable, err := providerExecutable(request.Provider, lookPath)
	if err != nil {
		return TicketPlanningLaunch{}, err
	}
	prompt := ticketImplementationPrompt(request)
	resume := implementationBaseCommand(request.Provider, executable, level)
	start := append(append([]string(nil), resume...), prompt)
	return TicketPlanningLaunch{Provider: request.Provider, Start: start, Resume: resume}, nil
}

// implementationBaseCommand returns the autonomous, full-permission argv for
// each provider. The flags come from the installed CLIs:
// codex --help, claude --help, cursor-agent --help, grok --help.
func implementationBaseCommand(provider, executable, level string) []string {
	switch provider {
	case "codex":
		return []string{executable, "--dangerously-bypass-approvals-and-sandbox", "-c", `model_reasoning_effort="` + level + `"`}
	case "claude":
		return []string{executable, "--permission-mode", "bypassPermissions", "--effort", level}
	case "cursor":
		// --trust skips the interactive workspace-trust prompt, which would
		// otherwise hang an unattended run in a fresh worktree.
		return []string{executable, "--force", "--trust"}
	case "grok":
		return []string{executable, "--permission-mode", "bypassPermissions", "--reasoning-effort", level}
	default:
		panic("validated Ticket implementation provider is missing a command")
	}
}

func ticketImplementationPrompt(request TicketImplementationRequest) string {
	sections := make([]string, 0, 4)
	if instructions := strings.TrimSpace(request.Instructions); instructions != "" {
		sections = append(sections, instructions)
	}
	if request.Provider == "cursor" {
		sections = append(sections, cursorImplementationEffortInstruction(request.Effort))
	}
	sections = append(sections, ticketImplementationTask(request.Ticket, request.Claimant))
	return strings.Join(sections, "\n\n")
}

func cursorImplementationEffortInstruction(effort TicketPlanningEffort) string {
	switch effort {
	case TicketPlanningEffortSmall:
		return "Use a brief implementation effort."
	case TicketPlanningEffortMedium:
		return "Use a standard implementation effort."
	case TicketPlanningEffortLarge:
		return "Use a thorough implementation effort. Check important assumptions."
	case TicketPlanningEffortXLarge:
		return "Use an exhaustive implementation effort. Compare alternatives and check all important assumptions."
	default:
		panic("validated Ticket implementation effort is missing a Cursor instruction")
	}
}

func ticketImplementationTask(ticket, claimant string) string {
	lines := []string{
		fmt.Sprintf("Implement twt Ticket `%s`. Run the relevant tests and create one pull request for each repository that changes.", ticket),
		"Read the Ticket before you start:",
		"twt tickets show " + ticket + " --output json",
		"",
		fmt.Sprintf("You work as the claimant %q of this Ticket. Do not claim other Tickets, and do not change this Ticket through any command other than the two below.", claimant),
		"",
		"When the work ships, record every pull request and release the Ticket in one command:",
		fmt.Sprintf("twt tickets complete %s --as %s --pr URL", ticket, claimant),
		"Repeat --pr for each pull request.",
		"",
		askContract(ticket, claimant),
		"",
		"Only when you cannot proceed at all, write what blocks you as a comment, then release the claim:",
		fmt.Sprintf("printf '%%s' \"BLOCKED_REASON\" | twt tickets comment %s --stdin", ticket),
		fmt.Sprintf("twt tickets unclaim %s --as %s", ticket, claimant),
	}
	return strings.Join(lines, "\n")
}
