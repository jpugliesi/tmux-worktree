package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/prstate"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

// renderTicketTree writes the Project dependency graph as a tree in
// dependents-flow direction: children are the tickets a node unblocks.
// Roots are tickets without in-graph dependencies. A node on the current
// DFS path prints as "(cycle)"; a node already expanded elsewhere prints as
// "…" without re-expansion, so the walk is O(V+E) and terminates on any
// graph.
func renderTicketTree(out io.Writer, result ticketservice.QueueResult, prStates map[string]prstate.PRState) error {
	nodes := make(map[string]ticketservice.QueueTicket, len(result.Graph))
	for _, ticket := range result.Graph {
		nodes[ticket.Slug] = ticket
	}
	children := map[string][]string{}
	inGraphDeps := map[string]int{}
	for _, ticket := range result.Graph {
		for _, dependency := range ticket.Dependencies {
			if !dependency.InProject {
				continue
			}
			if _, exists := nodes[dependency.Slug]; !exists {
				continue
			}
			children[dependency.Slug] = append(children[dependency.Slug], ticket.Slug)
			inGraphDeps[ticket.Slug]++
		}
	}
	for slug := range children {
		sort.Slice(children[slug], func(i, j int) bool {
			left, right := nodes[children[slug][i]], nodes[children[slug][j]]
			if left.Priority != right.Priority {
				return left.Priority < right.Priority
			}
			return left.Slug < right.Slug
		})
	}
	roots := []string{}
	for _, ticket := range result.Graph {
		if inGraphDeps[ticket.Slug] == 0 {
			roots = append(roots, ticket.Slug)
		}
	}

	if _, err := fmt.Fprintf(out, "Project: %s\n", result.Project); err != nil {
		return err
	}
	printed := map[string]bool{}
	onPath := map[string]bool{}
	var walk func(slug, prefix, branchMark, childPrefix string) error
	walk = func(slug, prefix, branchMark, childPrefix string) error {
		node := nodes[slug]
		suffix := ""
		if onPath[slug] {
			suffix = " (cycle)"
		} else if printed[slug] {
			suffix = " …"
		}
		if _, err := fmt.Fprintf(out, "%s%s%s%s\n", prefix, branchMark, treeNodeLine(node, prStates), suffix); err != nil {
			return err
		}
		if suffix != "" {
			return nil
		}
		printed[slug] = true
		onPath[slug] = true
		defer func() { onPath[slug] = false }()
		kids := children[slug]
		for index, child := range kids {
			mark, nextPrefix := "├── ", childPrefix+"│   "
			if index == len(kids)-1 {
				mark, nextPrefix = "└── ", childPrefix+"    "
			}
			if err := walk(child, childPrefix, mark, nextPrefix); err != nil {
				return err
			}
		}
		return nil
	}
	for index, root := range roots {
		mark, childPrefix := "├── ", "│   "
		if index == len(roots)-1 {
			mark, childPrefix = "└── ", "    "
		}
		if err := walk(root, "", mark, childPrefix); err != nil {
			return err
		}
	}
	// Cycle members never qualify as roots; render each cycle flat so its
	// tickets stay visible.
	for _, cycle := range result.Cycles {
		if _, err := fmt.Fprintf(out, "Dependency cycle: %s\n", strings.Join(cycle, ", ")); err != nil {
			return err
		}
		for _, member := range cycle {
			if printed[member] {
				continue
			}
			printed[member] = true
			if _, err := fmt.Fprintf(out, "└── %s (cycle)\n", treeNodeLine(nodes[member], prStates)); err != nil {
				return err
			}
		}
	}
	return nil
}

// treeNodeLine renders one node: slug, priority, derived state, claimant,
// PR badge, and dependency warnings.
func treeNodeLine(node ticketservice.QueueTicket, prStates map[string]prstate.PRState) string {
	parts := []string{node.Slug, fmt.Sprintf("p%d", node.Priority),
		deriveTicketState(node.Status, node.ClaimedBy, node.PullRequests, prStates, node.Ready)}
	if node.ClaimedBy != "" {
		parts = append(parts, "@"+node.ClaimedBy)
	}
	if badge := prBadge(node.PullRequests, prStates); badge != "" {
		parts = append(parts, badge)
	}
	for _, dependency := range node.Dependencies {
		switch dependency.State {
		case ticketservice.QueueDependencyMissing:
			parts = append(parts, fmt.Sprintf("[dep missing: %s]", dependency.Slug))
		case ticketservice.QueueDependencyInvalid:
			parts = append(parts, fmt.Sprintf("[dep invalid: %s]", dependency.Slug))
		}
	}
	return strings.Join(parts, "  ")
}
