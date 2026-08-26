package cli

import (
	"strconv"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/agentprovider"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func (o Options) ticketPlanningLaunch(tickets []string) (agentprovider.TicketPlanningLaunch, error) {
	config, err := store.LoadConfig(o.ConfigDir)
	if err != nil {
		return agentprovider.TicketPlanningLaunch{}, err
	}
	resolved := resolvedTicketAgentConfig(config.TicketAgent)
	if err := validateTicketAgentConfig(resolved); err != nil {
		return agentprovider.TicketPlanningLaunch{}, err
	}
	launch, err := agentprovider.BuildTicketPlanningLaunch(agentprovider.TicketPlanningRequest{
		Provider:     resolved.Provider,
		Effort:       agentprovider.TicketPlanningEffort(resolved.Effort),
		Instructions: resolved.Instructions,
		Tickets:      tickets,
	}, nil)
	if err != nil {
		return agentprovider.TicketPlanningLaunch{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "%v", err),
			"Install the configured provider, or change ticketAgent.provider in config.yaml.",
		)
	}
	return launch, nil
}

func validateTicketAgentConfig(config store.TicketAgentConfig) error {
	providerValid := false
	for _, provider := range agentprovider.TicketPlanningProviders() {
		if config.Provider == provider {
			providerValid = true
			break
		}
	}
	if !providerValid {
		return clierr.New(clierr.InvalidUsage, "unsupported ticketAgent.provider %q; use %s", config.Provider, strings.Join(agentprovider.TicketPlanningProviders(), ", "))
	}
	if _, err := agentprovider.TicketPlanningEffort(config.Effort).ProviderLevel(); err != nil {
		return clierr.New(clierr.InvalidUsage, "unsupported ticketAgent.effort %q; use %s", config.Effort, strings.Join(agentprovider.TicketPlanningEfforts(), ", "))
	}
	return nil
}

func addTicketPlanningAgent(template domain.Template, launch agentprovider.TicketPlanningLaunch, env []string) domain.Template {
	template.Agents = append([]domain.TemplateAgent(nil), template.Agents...)
	used := make(map[string]bool, len(template.Agents))
	for _, declared := range template.Agents {
		used[declared.Label] = true
	}
	label := "ticket-plan"
	for number := 2; used[label]; number++ {
		label = "ticket-plan-" + strconv.Itoa(number)
	}
	template.Agents = append(template.Agents, domain.TemplateAgent{
		Label: label, Provider: launch.Provider,
		Start: append([]string(nil), launch.Start...), Resume: append([]string(nil), launch.Resume...), PreferProviderResume: true,
		Env: append([]string(nil), env...),
	})
	return template
}

// ticketAgentEnv builds the environment pairs for an injected ticket agent,
// so its twt commands resolve the same Tickets home as the command that
// started it.
func ticketAgentEnv(options Options) []string {
	home, err := options.resolveTicketsHome()
	if err != nil || home == "" {
		return nil
	}
	return []string{"TWT_TICKETS_HOME=" + home}
}
