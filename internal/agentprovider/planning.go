package agentprovider

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	DefaultTicketPlanningProvider = "codex"
	DefaultTicketPlanningEffort   = TicketPlanningEffortLarge
)

// TicketPlanningEffort is the provider-neutral planning effort in config.
type TicketPlanningEffort string

const (
	TicketPlanningEffortSmall  TicketPlanningEffort = "small"
	TicketPlanningEffortMedium TicketPlanningEffort = "medium"
	TicketPlanningEffortLarge  TicketPlanningEffort = "large"
	TicketPlanningEffortXLarge TicketPlanningEffort = "xlarge"
)

// TicketPlanningEfforts returns the valid values in config display order.
func TicketPlanningEfforts() []string {
	return []string{
		string(TicketPlanningEffortSmall),
		string(TicketPlanningEffortMedium),
		string(TicketPlanningEffortLarge),
		string(TicketPlanningEffortXLarge),
	}
}

// ProviderLevel maps the provider-neutral effort to common provider terms.
func (e TicketPlanningEffort) ProviderLevel() (string, error) {
	switch e {
	case TicketPlanningEffortSmall:
		return "low", nil
	case TicketPlanningEffortMedium:
		return "medium", nil
	case TicketPlanningEffortLarge:
		return "high", nil
	case TicketPlanningEffortXLarge:
		return "xhigh", nil
	default:
		return "", fmt.Errorf("unsupported Ticket planning effort %q", e)
	}
}

// TicketPlanningProviders returns the providers that can start planning agents.
func TicketPlanningProviders() []string {
	return []string{"codex", "claude", "cursor", "grok"}
}

// TicketPlanningRequest contains the provider-neutral planning input.
type TicketPlanningRequest struct {
	Provider     string
	Effort       TicketPlanningEffort
	Instructions string
	Tickets      []string
	// Claimant identifies the planning session in the ticket claim, so the
	// prompt can name the twt commands the agent may run.
	Claimant string
}

// TicketPlanningLaunch contains direct process arguments for first start and
// fallback resume. Resume never contains the initial Ticket prompt.
type TicketPlanningLaunch struct {
	Provider string
	Start    []string
	Resume   []string
}

// BuildTicketPlanningLaunch validates a request and returns direct argv.
func BuildTicketPlanningLaunch(request TicketPlanningRequest, lookPath func(string) (string, error)) (TicketPlanningLaunch, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if !isTicketPlanningProvider(request.Provider) {
		return TicketPlanningLaunch{}, fmt.Errorf("unsupported Ticket planning provider %q", request.Provider)
	}
	level, err := request.Effort.ProviderLevel()
	if err != nil {
		return TicketPlanningLaunch{}, err
	}
	if len(request.Tickets) == 0 {
		return TicketPlanningLaunch{}, fmt.Errorf("at least one Ticket is required for a planning agent")
	}
	executable, err := providerExecutable(request.Provider, lookPath)
	if err != nil {
		return TicketPlanningLaunch{}, err
	}
	prompt := ticketPlanningPrompt(request)
	resume := planningBaseCommand(request.Provider, executable, level)
	start := append(append([]string(nil), resume...), prompt)
	return TicketPlanningLaunch{Provider: request.Provider, Start: start, Resume: resume}, nil
}

// askContract is the waiting protocol shared by planning and implementation
// prompts.
func askContract(ticket, claimant string) string {
	return strings.Join([]string{
		"If you need a decision or information from the human, ask through the Ticket and stop:",
		fmt.Sprintf("printf '%%s' \"QUESTION\" | twt tickets ask %s --stdin --as %s", ticket, claimant),
		"Then end your turn with the final line WAITING FOR ANSWER. Do not guess, do not",
		"poll, and do not work around the question. The answer arrives as your next",
		"message. If the human answers you directly instead, record it yourself:",
		fmt.Sprintf("printf '%%s' \"THEIR_ANSWER\" | twt tickets answer %s --stdin", ticket),
	}, "\n")
}

func isTicketPlanningProvider(provider string) bool {
	for _, candidate := range TicketPlanningProviders() {
		if provider == candidate {
			return true
		}
	}
	return false
}

func providerExecutable(provider string, lookPath func(string) (string, error)) (string, error) {
	executable := provider
	if provider == "cursor" {
		executable = "cursor-agent"
	}
	if _, err := lookPath(executable); err == nil {
		return executable, nil
	}
	return "", fmt.Errorf("cannot find the %q Ticket planning provider on PATH", provider)
}

// planningBaseCommand launches planning agents in the same autonomous mode
// as implementation agents. Provider plan modes would block the twt writes
// the workflow needs (tickets plan, ask, set, unclaim); the plan-only rule
// is a prompt contract instead.
func planningBaseCommand(provider, executable, level string) []string {
	return implementationBaseCommand(provider, executable, level)
}

func ticketPlanningPrompt(request TicketPlanningRequest) string {
	sections := make([]string, 0, 4)
	if instructions := strings.TrimSpace(request.Instructions); instructions != "" {
		sections = append(sections, instructions)
	}
	if request.Provider == "cursor" {
		sections = append(sections, cursorEffortInstruction(request.Effort))
	}
	sections = append(sections, ticketPlanningTask(request.Tickets, request.Claimant))
	if request.Claimant != "" && len(request.Tickets) > 0 {
		// A plan-time question is cheaper than a wrong plan: ask early.
		sections = append(sections, askContract(request.Tickets[0], request.Claimant))
		sections = append(sections, planningApprovalContract(request.Tickets, request.Claimant))
	}
	return strings.Join(sections, "\n\n")
}

func cursorEffortInstruction(effort TicketPlanningEffort) string {
	switch effort {
	case TicketPlanningEffortSmall:
		return "Use a brief planning effort."
	case TicketPlanningEffortMedium:
		return "Use a standard planning effort."
	case TicketPlanningEffortLarge:
		return "Use a thorough planning effort. Check important assumptions."
	case TicketPlanningEffortXLarge:
		return "Use an exhaustive planning effort. Compare alternatives and check all important assumptions."
	default:
		panic("validated Ticket planning effort is missing a Cursor instruction")
	}
}

func ticketPlanningTask(tickets []string, claimant string) string {
	var first string
	if len(tickets) == 1 {
		first = fmt.Sprintf("Create a plan to implement twt Ticket `%s`.", tickets[0])
	} else {
		quoted := make([]string, 0, len(tickets))
		for _, ticket := range tickets {
			quoted = append(quoted, "`"+ticket+"`")
		}
		first = "Create one plan to implement these twt Tickets: " + strings.Join(quoted, ", ") + "."
	}
	lines := []string{first, "Read each Ticket before you make the plan:"}
	for _, ticket := range tickets {
		lines = append(lines, "twt tickets show "+ticket+" --output json")
	}
	lines = append(lines,
		"When the Ticket names a Project, also read the Project plan:",
		"twt projects plan show PROJECT --output json",
		"",
		"Explore the repository read-only to ground the plan.",
		"HARD RULE: plan only. Make no file edits, no commits, and no branches.",
		"Your only writes are the twt commands in this prompt.",
		"",
		"Write a decision-complete plan into each Ticket. The write replaces the",
		"whole ## Plan section and keeps every other section:")
	for _, ticket := range tickets {
		write := fmt.Sprintf("printf '%%s' \"PLAN\" | twt tickets plan %s --stdin", ticket)
		if claimant != "" {
			write += " --as " + claimant
		}
		lines = append(lines, write)
	}
	return strings.Join(lines, "\n")
}

// planningApprovalContract ends the planning contract: request the human's
// approval, wait, then promote and release.
func planningApprovalContract(tickets []string, claimant string) string {
	lines := []string{
		"When every plan is written, request approval and stop:",
		fmt.Sprintf("printf '%%s' \"Plan ready for your approval.\" | twt tickets ask %s --stdin --as %s", tickets[0], claimant),
		"End your turn with the final line WAITING FOR ANSWER.",
		"The human approves with 'twt tickets approve', which arrives as your next",
		"message. Only then promote each Ticket for implementation and release it:",
	}
	for _, ticket := range tickets {
		lines = append(lines,
			fmt.Sprintf("twt tickets set %s --status ready-for-agent", ticket),
			fmt.Sprintf("twt tickets unclaim %s --as %s", ticket, claimant))
	}
	return strings.Join(lines, "\n")
}
