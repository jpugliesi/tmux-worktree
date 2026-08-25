package cli

import (
	"reflect"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/agentprovider"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestAddTicketPlanningAgentUsesTheFirstFreeLabel(t *testing.T) {
	template := domain.Template{Agents: []domain.TemplateAgent{
		{Label: "ticket-plan", Provider: "command", Start: []string{"one"}},
		{Label: "ticket-plan-2", Provider: "command", Start: []string{"two"}},
		{Label: "review", Provider: "command", Start: []string{"three"}},
	}}
	launch := agentprovider.TicketPlanningLaunch{
		Provider: "codex", Start: []string{"codex", "PROMPT"}, Resume: []string{"codex"},
	}

	got := addTicketPlanningAgent(template, launch)
	if len(got.Agents) != 4 || got.Agents[3].Label != "ticket-plan-3" {
		t.Fatalf("generated agents = %+v", got.Agents)
	}
	if !reflect.DeepEqual(got.Agents[3].Start, launch.Start) || !reflect.DeepEqual(got.Agents[3].Resume, launch.Resume) {
		t.Fatalf("generated declaration = %+v", got.Agents[3])
	}
	if len(template.Agents) != 3 {
		t.Fatalf("source Template changed: %+v", template.Agents)
	}
}

func TestValidateTicketAgentConfigRejectsUnknownValues(t *testing.T) {
	for _, config := range []store.TicketAgentConfig{
		{Provider: "command", Effort: "large"},
		{Provider: "codex", Effort: "huge"},
	} {
		if err := validateTicketAgentConfig(config); clierr.CodeOf(err) != clierr.InvalidUsage {
			t.Fatalf("validateTicketAgentConfig(%+v) = %v", config, err)
		}
	}
}
