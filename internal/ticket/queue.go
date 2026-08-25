package ticket

import (
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

type QueueDependencyState string

const (
	QueueDependencyOpen    QueueDependencyState = "open"
	QueueDependencyClosed  QueueDependencyState = "closed"
	QueueDependencyMissing QueueDependencyState = "missing"
	QueueDependencyInvalid QueueDependencyState = "invalid"
)

// QueueDependency describes one edge in a Project Ticket graph.
type QueueDependency struct {
	Slug      string               `json:"slug"`
	State     QueueDependencyState `json:"state"`
	Project   string               `json:"project,omitempty"`
	InProject bool                 `json:"inProject"`
}

// QueueTicket is one open Ticket in a Project queue.
type QueueTicket struct {
	Slug         string              `json:"slug"`
	Title        string              `json:"title"`
	Status       domain.TicketStatus `json:"status"`
	Priority     int                 `json:"priority"`
	Dependencies []QueueDependency   `json:"dependencies"`
	ClaimedBy    string              `json:"claimedBy,omitempty"`
	Ready        bool                `json:"ready"`
}

// QueueResult is one consistent view of a Project Ticket graph and its ready
// work. A limit cuts Ready only. Graph and Cycles always describe the full
// open Project graph.
type QueueResult struct {
	Project         string        `json:"project"`
	Graph           []QueueTicket `json:"graph"`
	Ready           []QueueTicket `json:"ready"`
	ReadyTotalCount int           `json:"readyTotalCount"`
	ReadyTruncated  bool          `json:"readyTruncated,omitempty"`
	Cycles          [][]string    `json:"cycles"`
}

// Queue builds one Project queue from one Ticket index snapshot.
func (s *Service) Queue(projectName string, limit int) (QueueResult, error) {
	if limit < 0 {
		return QueueResult{}, clierr.New(clierr.InvalidUsage, "--limit must be zero or greater")
	}
	if _, err := s.Project(projectName); err != nil {
		return QueueResult{}, err
	}
	home, err := s.home()
	if err != nil {
		return QueueResult{}, err
	}
	idx, err := buildIndex(home)
	if err != nil {
		return QueueResult{}, err
	}

	graph := make([]QueueTicket, 0)
	for _, ticket := range idx.tickets {
		if ticket.Project != projectName || closedStatus(ticket.Status) {
			continue
		}
		dependencies := make([]QueueDependency, 0, len(ticket.BlockedBy))
		for _, blocker := range ticket.BlockedBy {
			dependencies = append(dependencies, queueDependency(idx, blocker, projectName))
		}
		graph = append(graph, QueueTicket{
			Slug: ticket.Slug, Title: ticket.Title, Status: ticket.Status, Priority: ticket.Priority,
			Dependencies: dependencies, ClaimedBy: ticket.ClaimedBy, Ready: idx.ready(ticket),
		})
	}
	sort.Slice(graph, func(i, j int) bool {
		if graph[i].Priority != graph[j].Priority {
			return graph[i].Priority < graph[j].Priority
		}
		return graph[i].Slug < graph[j].Slug
	})

	ready := make([]QueueTicket, 0)
	for _, ticket := range graph {
		if ticket.Ready {
			ready = append(ready, ticket)
		}
	}
	total := len(ready)
	truncated := limit > 0 && len(ready) > limit
	if truncated {
		ready = ready[:limit]
	}
	return QueueResult{
		Project: projectName, Graph: graph, Ready: ready,
		ReadyTotalCount: total, ReadyTruncated: truncated, Cycles: queueCycles(graph),
	}, nil
}

func queueDependency(idx *index, slug, projectName string) QueueDependency {
	dependency := QueueDependency{Slug: slug}
	paths := idx.bySlug[slug]
	if len(paths) == 0 {
		dependency.State = QueueDependencyMissing
		return dependency
	}
	if len(paths) != 1 {
		dependency.State = QueueDependencyInvalid
		return dependency
	}
	ticket, ok := idx.tickets[paths[0]]
	if !ok {
		dependency.State = QueueDependencyInvalid
		return dependency
	}
	dependency.Project = ticket.Project
	dependency.InProject = ticket.Project == projectName
	if closedStatus(ticket.Status) {
		dependency.State = QueueDependencyClosed
	} else {
		dependency.State = QueueDependencyOpen
	}
	return dependency
}

// queueCycles returns each dependency cycle as one sorted strongly connected
// component. The component list follows lexical order.
func queueCycles(graph []QueueTicket) [][]string {
	nodes := make(map[string]QueueTicket, len(graph))
	for _, ticket := range graph {
		nodes[ticket.Slug] = ticket
	}
	indices := map[string]int{}
	lowLinks := map[string]int{}
	onStack := map[string]bool{}
	stack := make([]string, 0, len(graph))
	nextIndex := 0
	cycles := [][]string{}

	var visit func(string)
	visit = func(slug string) {
		indices[slug] = nextIndex
		lowLinks[slug] = nextIndex
		nextIndex++
		stack = append(stack, slug)
		onStack[slug] = true

		for _, dependency := range nodes[slug].Dependencies {
			if dependency.State != QueueDependencyOpen || !dependency.InProject {
				continue
			}
			if _, exists := nodes[dependency.Slug]; !exists {
				continue
			}
			if _, seen := indices[dependency.Slug]; !seen {
				visit(dependency.Slug)
				lowLinks[slug] = min(lowLinks[slug], lowLinks[dependency.Slug])
			} else if onStack[dependency.Slug] {
				lowLinks[slug] = min(lowLinks[slug], indices[dependency.Slug])
			}
		}

		if lowLinks[slug] != indices[slug] {
			return
		}
		component := []string{}
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == slug {
				break
			}
		}
		if len(component) > 1 || queueSelfCycle(nodes[slug]) {
			sort.Strings(component)
			cycles = append(cycles, component)
		}
	}

	for _, ticket := range graph {
		if _, seen := indices[ticket.Slug]; !seen {
			visit(ticket.Slug)
		}
	}
	sort.Slice(cycles, func(i, j int) bool { return cycles[i][0] < cycles[j][0] })
	return cycles
}

func queueSelfCycle(ticket QueueTicket) bool {
	for _, dependency := range ticket.Dependencies {
		if dependency.State == QueueDependencyOpen && dependency.InProject && dependency.Slug == ticket.Slug {
			return true
		}
	}
	return false
}
