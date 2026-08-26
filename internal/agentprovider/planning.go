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

func planningBaseCommand(provider, executable, level string) []string {
	switch provider {
	case "codex":
		return []string{executable, "-c", `model_reasoning_effort="` + level + `"`}
	case "claude":
		return []string{executable, "--permission-mode", "plan", "--effort", level}
	case "cursor":
		return []string{executable, "--plan"}
	case "grok":
		return []string{executable, "--permission-mode", "plan", "--reasoning-effort", level}
	default:
		panic("validated Ticket planning provider is missing a command")
	}
}

func ticketPlanningPrompt(request TicketPlanningRequest) string {
	sections := make([]string, 0, 4)
	if instructions := strings.TrimSpace(request.Instructions); instructions != "" {
		sections = append(sections, instructions)
	}
	if request.Provider == "cursor" {
		sections = append(sections, cursorEffortInstruction(request.Effort))
	}
	sections = append(sections, ticketPlanningTask(request.Tickets))
	if request.Claimant != "" && len(request.Tickets) > 0 {
		// A plan-time question is cheaper than a wrong plan: ask early.
		sections = append(sections, askContract(request.Tickets[0], request.Claimant))
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

func ticketPlanningTask(tickets []string) string {
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
		"Work in plan mode. Make no implementation changes.",
		"Return a decision-complete plan that is ready for implementation.",
	)
	return strings.Join(lines, "\n")
}
