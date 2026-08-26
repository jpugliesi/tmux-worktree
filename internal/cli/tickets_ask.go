package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

func newTicketsAskCommand(options Options) *cobra.Command {
	var fromStdin bool
	var as string
	command := &cobra.Command{
		Use:   "ask TICKET --stdin --as NAME",
		Short: "Ask the human a question and wait on the Ticket",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			if !fromStdin {
				return invalidUsageWithHint(command, "Pass the question text on standard input with --stdin.",
					"missing required flag --stdin")
			}
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			claimant, err := resolveClaimant(command, as)
			if err != nil {
				return err
			}
			text, err := readTicketStdin(command)
			if err != nil {
				return err
			}
			return askTicket(command, service, args[0], claimant, text)
		},
	}
	command.Flags().BoolVar(&fromStdin, "stdin", false, "Read the question text from standard input")
	_ = command.MarkFlagRequired("stdin")
	command.Flags().StringVar(&as, "as", "", "Set the claimant name")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// askTicket records one question. Both the tickets ask command and apply use
// it.
func askTicket(command *cobra.Command, service ticketservice.Store, ref, claimant, text string) error {
	return runMutation(command, "tickets.ask",
		func() (string, string, error) {
			ticket, err := service.Ask(ref, claimant, text, true)
			return ticket.Slug, ticket.Title, err
		},
		func() (string, string, error) {
			ticket, err := service.Ask(ref, claimant, text, false)
			return ticket.Slug, ticket.Title, err
		},
		func(out io.Writer, id, _ string) error {
			_, err := fmt.Fprintf(out, "Asked on ticket %q; waiting on input\n", id)
			return err
		})
}

// answerRelay reports whether the recorded answer also reached a live agent
// session.
type answerRelay struct {
	Delivered   bool   `json:"delivered"`
	AgentID     string `json:"agentId,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type ticketAnswerOutput struct {
	SchemaVersion int         `json:"schemaVersion"`
	Operation     string      `json:"operation"`
	Status        string      `json:"status"`
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Relay         answerRelay `json:"relay"`
}

func newTicketsAnswerCommand(options Options) *cobra.Command {
	var fromStdin bool
	var agentID string
	command := &cobra.Command{
		Use:   "answer TICKET --stdin [--agent AGENT_ID]",
		Short: "Answer a waiting Ticket and relay into its agent session",
		Args:  exactArgs("TICKET"),
		RunE: func(command *cobra.Command, args []string) error {
			if !fromStdin {
				return invalidUsageWithHint(command, "Pass the answer text on standard input with --stdin.",
					"missing required flag --stdin")
			}
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			text, err := readTicketStdin(command)
			if err != nil {
				return err
			}
			return answerTicket(command, options, service, args[0], agentID, text)
		},
	}
	command.Flags().BoolVar(&fromStdin, "stdin", false, "Read the answer text from standard input")
	_ = command.MarkFlagRequired("stdin")
	command.Flags().StringVar(&agentID, "agent", "", "Relay into this Agent Session when several are live")
	setArguments(command, requiredArgument("ticket"))
	command.ValidArgsFunction = ticketSlugCompletion(options)
	return command
}

// answerTicket records the answer and then relays it best-effort. Both the
// tickets answer command and apply use it.
func answerTicket(command *cobra.Command, options Options, service ticketservice.Store, ref, agentID, text string) error {
	if isDryRun(command) {
		ticket, err := service.Answer(ref, text, true)
		if err != nil {
			return err
		}
		return writeMutation(command, "tickets.answer", statusValid, ticket.Slug, ticket.Title)
	}
	ticket, err := service.Answer(ref, text, false)
	if err != nil {
		return err
	}
	relay := relayTicketAnswer(options, ticket, text, agentID)
	if WantsJSON(command) {
		return writeJSONOutput(command, ticketAnswerOutput{
			SchemaVersion: jsonSchemaVersion, Operation: "tickets.answer", Status: statusApplied,
			ID: ticket.Slug, Name: ticket.Title, Relay: relay,
		})
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(), "Answered ticket %q\n", ticket.Slug); err != nil {
		return err
	}
	if relay.Delivered {
		_, err = fmt.Fprintf(command.OutOrStdout(), "Relayed the answer to Agent Session %s\n", relay.AgentID)
		return err
	}
	_, err = fmt.Fprintf(command.ErrOrStderr(),
		"Warning: the answer is recorded on the ticket but was not relayed: %s\n", relay.Reason)
	return err
}

// relayTicketAnswer finds the asking agent's live pane and injects the
// answer. Resolution: the active local dispatch session for the slug, then
// the ticket's linked Workspace. Failures are reasons, never errors — the
// answer is already durable on the ticket.
func relayTicketAnswer(options Options, ticket domain.Ticket, text, agentOverride string) answerRelay {
	message := fmt.Sprintf("Answer to your question on ticket %s:\n\n%s\n\nContinue the ticket work.",
		ticket.Slug, strings.TrimSpace(text))
	workspaceID := ""
	agentID := agentOverride
	if sessions, err := store.NewLocalDispatchSessionStore(options.StateDir).List(); err == nil {
		for _, session := range sessions {
			if session.TicketSlug == ticket.Slug && session.Active() {
				workspaceID = session.WorkspaceID
				if agentID == "" {
					agentID = session.AgentSessionID
				}
				break
			}
		}
	}
	if workspaceID == "" {
		workspaceID = ticket.WorkspaceID
	}
	if workspaceID == "" {
		return answerRelay{Reason: "the ticket has no linked Workspace on this machine; the agent reads the answer when it resumes"}
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find(workspaceID)
	if err != nil {
		return answerRelay{WorkspaceID: workspaceID,
			Reason: "the linked Workspace is not on this machine; the agent reads the answer when it resumes"}
	}
	agents := options.agentService()
	records, err := store.NewAgentStore(options.StateDir).List(workspace.ID)
	if err != nil {
		return answerRelay{WorkspaceID: workspace.ID, Reason: fmt.Sprintf("could not list agent sessions: %v", err)}
	}
	var target *domain.AgentSession
	if agentID != "" {
		for index := range records {
			if records[index].ID == agentID {
				target = &records[index]
				break
			}
		}
		if target == nil {
			return answerRelay{WorkspaceID: workspace.ID, Reason: fmt.Sprintf("agent session %q is not in Workspace %q", agentID, workspace.ID)}
		}
	} else {
		var live []*domain.AgentSession
		for index := range records {
			if agents.CanSend(records[index]) {
				live = append(live, &records[index])
			}
		}
		if len(live) == 0 {
			return answerRelay{WorkspaceID: workspace.ID,
				Reason: "no live agent session accepts input; resume it with 'twt agents resume', it reads the answer from the ticket"}
		}
		if len(live) > 1 {
			ids := make([]string, 0, len(live))
			for _, candidate := range live {
				ids = append(ids, candidate.ID)
			}
			return answerRelay{WorkspaceID: workspace.ID,
				Reason: fmt.Sprintf("several live agent sessions accept input (%s); pass --agent", strings.Join(ids, ", "))}
		}
		target = live[0]
	}
	if err := agents.Send(*target, workspace.ID, message); err != nil {
		return answerRelay{WorkspaceID: workspace.ID, AgentID: target.ID,
			Reason: fmt.Sprintf("send failed: %v", err)}
	}
	return answerRelay{Delivered: true, AgentID: target.ID, WorkspaceID: workspace.ID}
}
