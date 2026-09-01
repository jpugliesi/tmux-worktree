package ticket

import (
	"fmt"
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

// LabelChangeResult reports one cross-Ticket label write. Tickets holds the
// slugs that the command targeted. The write never moves a file.
type LabelChangeResult struct {
	Name    string   `json:"name"`
	NewName string   `json:"newName,omitempty"`
	Tickets []string `json:"tickets"`
}

// AddLabel writes one label onto the named Tickets. The label does not
// create a Project or move a file.
func (s *Service) AddLabel(name string, refs []string, dryRun bool) (LabelChangeResult, error) {
	label, err := normalizeOneLabel(name)
	if err != nil {
		return LabelChangeResult{}, err
	}
	if len(refs) == 0 {
		return LabelChangeResult{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "labels add needs at least one Ticket"),
			"Pass --ticket TICKET. Repeat the flag for more Tickets.")
	}
	return s.rewriteLabels(dryRun, fmt.Sprintf("twt: add label %s", label), refs, func(m *mutation) error {
		next, mergeErr := mergeLabels(m.ticket.Labels, SetRequest{AddLabels: []string{label}})
		if mergeErr != nil {
			return mergeErr
		}
		return writeTicketLabels(m, next)
	}, func(result LabelChangeResult) LabelChangeResult {
		result.Name = label
		return result
	})
}

// RemoveLabel removes one label from Tickets. An empty refs list selects
// every Ticket that already carries the label, including closed Tickets.
func (s *Service) RemoveLabel(name string, refs []string, dryRun bool) (LabelChangeResult, error) {
	label, err := normalizeOneLabel(name)
	if err != nil {
		return LabelChangeResult{}, err
	}
	targets := refs
	if len(targets) == 0 {
		tickets, listErr := s.List(ListFilter{All: true, Labels: []string{label}})
		if listErr != nil {
			return LabelChangeResult{}, listErr
		}
		if len(tickets) == 0 {
			return LabelChangeResult{}, labelMissing(label)
		}
		targets = make([]string, 0, len(tickets))
		for _, ticket := range tickets {
			targets = append(targets, ticket.Slug)
		}
	}
	return s.rewriteLabels(dryRun, fmt.Sprintf("twt: remove label %s", label), targets, func(m *mutation) error {
		next, mergeErr := mergeLabels(m.ticket.Labels, SetRequest{RemoveLabels: []string{label}})
		if mergeErr != nil {
			return mergeErr
		}
		return writeTicketLabels(m, next)
	}, func(result LabelChangeResult) LabelChangeResult {
		result.Name = label
		return result
	})
}

// RenameLabel rewrites one label name on every Ticket that carries it,
// including closed Tickets. The write never moves a file.
func (s *Service) RenameLabel(oldName, newName string, dryRun bool) (LabelChangeResult, error) {
	oldLabel, err := normalizeOneLabel(oldName)
	if err != nil {
		return LabelChangeResult{}, err
	}
	newLabel, err := normalizeOneLabel(newName)
	if err != nil {
		return LabelChangeResult{}, err
	}
	tickets, err := s.List(ListFilter{All: true, Labels: []string{oldLabel}})
	if err != nil {
		return LabelChangeResult{}, err
	}
	if len(tickets) == 0 {
		return LabelChangeResult{}, labelMissing(oldLabel)
	}
	targets := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		targets = append(targets, ticket.Slug)
	}
	if oldLabel == newLabel {
		sort.Strings(targets)
		return LabelChangeResult{Name: oldLabel, NewName: newLabel, Tickets: targets}, nil
	}
	return s.rewriteLabels(dryRun, fmt.Sprintf("twt: rename label %s to %s", oldLabel, newLabel), targets, func(m *mutation) error {
		next := renameTicketLabel(m.ticket.Labels, oldLabel, newLabel)
		return writeTicketLabels(m, next)
	}, func(result LabelChangeResult) LabelChangeResult {
		result.Name = oldLabel
		result.NewName = newLabel
		return result
	})
}

func (s *Service) rewriteLabels(dryRun bool, message string, refs []string, apply func(*mutation) error, finish func(LabelChangeResult) LabelChangeResult) (LabelChangeResult, error) {
	return syncWrite(s, syncBestEffort, dryRun, func() string {
		return message
	}, func() (LabelChangeResult, error) {
		changed := make([]string, 0, len(refs))
		seen := map[string]bool{}
		for _, ref := range refs {
			ticket, err := s.mutateOnce(ref, dryRun, true, apply)
			if err != nil {
				return LabelChangeResult{}, err
			}
			if seen[ticket.Slug] {
				continue
			}
			seen[ticket.Slug] = true
			changed = append(changed, ticket.Slug)
		}
		sort.Strings(changed)
		return finish(LabelChangeResult{Tickets: changed}), nil
	})
}

func writeTicketLabels(m *mutation, labels []string) error {
	if labelsEqual(m.ticket.Labels, labels) {
		m.skipWrite = true
		return nil
	}
	setMapStringList(m.mapping, "labels", labels)
	return nil
}

func renameTicketLabel(current []string, oldLabel, newLabel string) []string {
	next := make([]string, 0, len(current))
	seen := map[string]bool{}
	for _, label := range current {
		if label == oldLabel {
			label = newLabel
		}
		if seen[label] {
			continue
		}
		seen[label] = true
		next = append(next, label)
	}
	return next
}

func labelsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func normalizeOneLabel(value string) (string, error) {
	labels, err := normalizeLabels([]string{value})
	if err != nil {
		return "", err
	}
	if len(labels) == 0 {
		return "", clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the label name is empty"),
			"Use lowercase letters, digits, and hyphens.")
	}
	return labels[0], nil
}

func labelMissing(name string) error {
	return clierr.WithHint(
		clierr.New(clierr.NotFound, "label %q is not on any Ticket", name),
		"Run 'twt labels list --all'.")
}
