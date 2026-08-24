package ticket

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"go.yaml.in/yaml/v3"
)

// Options configures the ticket Service.
type Options struct {
	// Home is the Tickets home directory.
	Home string
	// StateDir holds the per-ticket lock files. Locks never live in Home.
	StateDir string
}

// Service owns every ticket read and write.
type Service struct {
	options Options
	// now uses local time, not the UTC of the Workspace service. Ticket dates
	// are human-facing vault dates, so they follow the user's wall clock.
	now func() time.Time
}

func NewService(options Options) *Service {
	return &Service{options: options, now: time.Now}
}

// today formats the current local date as the vault date form.
func (s *Service) today() string {
	return s.now().Format("2006-01-02")
}

// home returns the cleaned Tickets home.
func (s *Service) home() (string, error) {
	if strings.TrimSpace(s.options.Home) == "" {
		return "", clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the Tickets home is not set"),
			"Set ticketsHome in the twt config file, or set TWT_TICKETS_HOME.")
	}
	return filepath.Clean(s.options.Home), nil
}

// InitResult reports what Init wrote or would write.
type InitResult struct {
	Home          string `json:"home"`
	WroteIndex    bool   `json:"wroteIndex"`
	WroteTemplate bool   `json:"wroteTemplate"`
}

// Init creates the Tickets home with its hub index and create template. It
// writes each file only when that file is missing. It never overwrites notes.
func (s *Service) Init(dryRun bool) (InitResult, error) {
	home, err := s.home()
	if err != nil {
		return InitResult{}, err
	}
	indexPath := filepath.Join(home, "index.md")
	templatePath := filepath.Join(home, "templates", "ticket.md")
	result := InitResult{
		Home:          home,
		WroteIndex:    !fileExists(indexPath),
		WroteTemplate: !fileExists(templatePath),
	}
	if dryRun {
		return result, nil
	}
	if err := os.MkdirAll(filepath.Join(home, "templates"), 0o755); err != nil {
		return InitResult{}, fmt.Errorf("create Tickets home: %w", err)
	}
	if result.WroteIndex {
		if err := store.WriteFileAtomic(indexPath, rootIndexContent(home, s.today()), 0o644, "Tickets index"); err != nil {
			return InitResult{}, err
		}
	}
	if result.WroteTemplate {
		if err := store.WriteFileAtomic(templatePath, ticketTemplateContent(), 0o644, "Ticket template"); err != nil {
			return InitResult{}, err
		}
	}
	return result, nil
}

// CreateProject creates one Project directory and writes its index.md only when
// that file is missing.
func (s *Service) CreateProject(name string, dryRun bool) (domain.Project, error) {
	home, err := s.home()
	if err != nil {
		return domain.Project{}, err
	}
	if err := store.ValidateResourceName(name); err != nil {
		return domain.Project{}, clierr.Wrap(clierr.InvalidUsage, err)
	}
	if name == "templates" {
		return domain.Project{}, clierr.New(clierr.InvalidUsage, "the Project name %q is reserved", name)
	}
	path := filepath.Join(home, name)
	if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
		return domain.Project{}, clierr.New(clierr.UnsafeState, "the Project path %q is a file, not a directory", path)
	}
	indexPath := filepath.Join(path, "index.md")
	if dryRun {
		project, projectErr := s.projectInfo(home, name)
		if projectErr != nil {
			project = domain.Project{Name: name, Path: path}
		}
		project.HasIndex = true
		return project, nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return domain.Project{}, fmt.Errorf("create Project directory: %w", err)
	}
	if !fileExists(indexPath) {
		if err := store.WriteFileAtomic(indexPath, projectIndexContent(name, s.today()), 0o644, "Project index"); err != nil {
			return domain.Project{}, err
		}
	}
	return s.projectInfo(home, name)
}

// Projects lists every Project directory sorted by name.
func (s *Service) Projects() ([]domain.Project, error) {
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(home)
	if errors.Is(err, os.ErrNotExist) {
		return nil, homeMissing(home)
	}
	if err != nil {
		return nil, fmt.Errorf("list Projects: %w", err)
	}
	projects := []domain.Project{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == "templates" || strings.HasPrefix(name, ".") {
			continue
		}
		project, err := s.projectInfo(home, name)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects, nil
}

// Project returns one Project by name.
func (s *Service) Project(name string) (domain.Project, error) {
	home, err := s.home()
	if err != nil {
		return domain.Project{}, err
	}
	info, statErr := os.Stat(filepath.Join(home, name))
	if statErr != nil || !info.IsDir() {
		return domain.Project{}, projectMissing(name)
	}
	return s.projectInfo(home, name)
}

func (s *Service) projectInfo(home, name string) (domain.Project, error) {
	path := filepath.Join(home, name)
	project := domain.Project{Name: name, Path: path}
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return project, nil
	}
	if err != nil {
		return domain.Project{}, fmt.Errorf("read Project %q: %w", name, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if entry.Name() == "index.md" {
			project.HasIndex = true
			continue
		}
		project.Tickets++
	}
	return project, nil
}

// CreateRequest describes one new Ticket. Priority -1 selects the default
// priority 2. An empty Status selects needs-triage. An empty Slug derives the
// slug from the title.
type CreateRequest struct {
	Title    string
	Slug     string
	Project  string
	Body     string
	Status   domain.TicketStatus
	Priority int
	// EnsureProject creates Project when it is missing. The interactive create
	// wizard sets this after confirm. --project and apply never set it.
	EnsureProject bool
}

// CreateResult is the created Ticket plus the rendered file content. A dry
// run returns the content without a write.
type CreateResult struct {
	Ticket  domain.Ticket
	Content []byte
}

// Create writes one new Ticket file.
func (s *Service) Create(req CreateRequest, dryRun bool) (CreateResult, error) {
	home, err := s.home()
	if err != nil {
		return CreateResult{}, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return CreateResult{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the ticket title is empty"),
			"Pass a DESCRIPTION or --title.")
	}
	status := req.Status
	if status == "" {
		status = domain.TicketNeedsTriage
	}
	priority := req.Priority
	if priority == -1 {
		priority = 2
	}
	slug := req.Slug
	if slug == "" {
		slug = domain.Slugify(title)
		if slug == "" {
			return CreateResult{}, clierr.WithHint(
				clierr.New(clierr.InvalidUsage, "title %q produces an empty slug", title),
				"Pass --slug.")
		}
	}
	ticket := domain.Ticket{
		Slug:      slug,
		Title:     title,
		Aliases:   []string{title},
		Status:    status,
		Priority:  priority,
		Project:   req.Project,
		BlockedBy: []string{},
		Created:   s.today(),
		Updated:   s.today(),
	}
	if err := ticket.Validate(); err != nil {
		return CreateResult{}, clierr.Wrap(clierr.InvalidUsage, err)
	}
	idx, err := buildIndex(home)
	if err != nil {
		return CreateResult{}, err
	}
	if paths := idx.bySlug[slug]; len(paths) > 0 {
		return CreateResult{}, clierr.WithHint(
			clierr.New(clierr.AlreadyExists, "ticket slug %q already exists at %q", slug, paths[0]),
			"Pass --slug to select a different slug.")
	}
	directory := home
	if req.Project != "" {
		if req.EnsureProject {
			if _, err := s.CreateProject(req.Project, dryRun); err != nil {
				return CreateResult{}, err
			}
		}
		info, statErr := os.Stat(filepath.Join(home, req.Project))
		if statErr != nil || !info.IsDir() {
			if dryRun && req.EnsureProject {
				directory = filepath.Join(home, req.Project)
			} else {
				return CreateResult{}, projectMissing(req.Project)
			}
		} else {
			directory = filepath.Join(home, req.Project)
		}
	}
	path := filepath.Join(directory, slug+".md")
	lock, err := store.AcquireNamedLock(s.options.StateDir, "ticket", slug)
	if err != nil {
		return CreateResult{}, err
	}
	defer lock.Release()
	if fileExists(path) {
		return CreateResult{}, clierr.WithHint(
			clierr.New(clierr.AlreadyExists, "ticket slug %q already exists at %q", slug, path),
			"Pass --slug to select a different slug.")
	}
	content, err := renderNewTicket(ticket, req.Body)
	if err != nil {
		return CreateResult{}, err
	}
	if !dryRun {
		if err := store.WriteFileAtomic(path, content, 0o644, "Ticket"); err != nil {
			return CreateResult{}, err
		}
	}
	ticket.Path = path
	return CreateResult{Ticket: ticket, Content: content}, nil
}

// renderNewTicket renders the v1 ticket file shape.
func renderNewTicket(ticket domain.Ticket, body string) ([]byte, error) {
	file := &TicketFile{}
	mapping := file.ensureMapping()
	node := mapValueForUpdate(mapping, "title")
	node.Kind = yaml.ScalarNode
	node.Tag = "!!str"
	node.Value = ticket.Title
	node.Style = yaml.DoubleQuotedStyle
	setMapStringList(mapping, "aliases", []string{ticket.Title})
	setMapStringList(mapping, "tags", []string{"tickets"})
	setMapString(mapping, "status", string(ticket.Status))
	setMapInt(mapping, "priority", ticket.Priority)
	if ticket.Project == "" {
		setMapNull(mapping, "project")
	} else {
		setMapString(mapping, "project", ticket.Project)
	}
	setMapStringList(mapping, "blocked_by", nil)
	setMapNull(mapping, "claimed_by")
	setMapNull(mapping, "claimed_at")
	setMapDate(mapping, "created", ticket.Created)
	setMapDate(mapping, "updated", ticket.Updated)
	file.Body = newTicketBody(ticket.Title, body)
	return file.Render()
}

// newTicketBody builds the initial body: the H1, then the given body or the
// spec skeleton.
func newTicketBody(title, body string) string {
	if strings.TrimSpace(body) == "" {
		return fmt.Sprintf(`
# %s

## What to build

## Acceptance criteria

- [ ]

## Blocked by

None - can start immediately

## Comments
`, title)
	}
	return "\n# " + title + "\n\n" + strings.Trim(body, "\n") + "\n"
}

// ListFilter selects Tickets. ProjectSet with an empty Project selects only
// ungrouped Tickets. All includes the closed Tickets that the default list
// hides.
type ListFilter struct {
	Project    string
	ProjectSet bool
	Status     string
	Ready      bool
	All        bool
}

// closedStatus reports whether a status resolves a Ticket. The default list
// hides these Tickets, because a closed Ticket is not open work.
func closedStatus(status domain.TicketStatus) bool {
	return status == domain.TicketDone || status == domain.TicketWontfix
}

// List returns the Tickets that match the filter, sorted by priority then by
// slug. Files that fail to parse are not listed. By default the list holds
// only open Tickets: All and an explicit Status both turn that default off.
func (s *Service) List(filter ListFilter) ([]domain.Ticket, error) {
	if filter.Ready && filter.Status != "" {
		return nil, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "--ready and --status select different sets"),
			"Use --ready or --status, not both.")
	}
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	idx, err := buildIndex(home)
	if err != nil {
		return nil, err
	}
	hideClosed := !filter.All && filter.Status == ""
	tickets := []domain.Ticket{}
	for _, ticket := range idx.tickets {
		if filter.ProjectSet && ticket.Project != filter.Project {
			continue
		}
		if filter.Status != "" && string(ticket.Status) != filter.Status {
			continue
		}
		if hideClosed && closedStatus(ticket.Status) {
			continue
		}
		if filter.Ready && !idx.ready(ticket) {
			continue
		}
		tickets = append(tickets, ticket)
	}
	sort.Slice(tickets, func(i, j int) bool {
		if tickets[i].Priority != tickets[j].Priority {
			return tickets[i].Priority < tickets[j].Priority
		}
		return tickets[i].Slug < tickets[j].Slug
	})
	return tickets, nil
}

// OpenBlocker is one blocker that keeps a Ticket out of the ready queue.
// Missing is true when no file carries the blocker slug. The CLI renders the
// missing marker.
type OpenBlocker struct {
	Slug    string `json:"slug"`
	Missing bool   `json:"missing"`
}

// ShowResult is one Ticket with its body and its readiness.
type ShowResult struct {
	Ticket        domain.Ticket
	Body          string
	Ready         bool
	BlockedByOpen []OpenBlocker
}

// Show returns one Ticket with its body.
func (s *Service) Show(ref string) (ShowResult, error) {
	home, err := s.home()
	if err != nil {
		return ShowResult{}, err
	}
	idx, err := buildIndex(home)
	if err != nil {
		return ShowResult{}, err
	}
	slug, err := idx.resolve(home, ref)
	if err != nil {
		return ShowResult{}, err
	}
	path := idx.path(slug)
	ticket := idx.tickets[path]
	open := []OpenBlocker{}
	for _, blocker := range ticket.BlockedBy {
		if !idx.blockerClosed(blocker) {
			open = append(open, OpenBlocker{Slug: blocker, Missing: idx.missingBlocker(blocker)})
		}
	}
	return ShowResult{
		Ticket:        ticket,
		Body:          idx.bodies[path],
		Ready:         idx.ready(ticket),
		BlockedByOpen: open,
	}, nil
}

// Resolve maps a reference to its Ticket.
func (s *Service) Resolve(ref string) (domain.Ticket, error) {
	home, err := s.home()
	if err != nil {
		return domain.Ticket{}, err
	}
	idx, err := buildIndex(home)
	if err != nil {
		return domain.Ticket{}, err
	}
	slug, err := idx.resolve(home, ref)
	if err != nil {
		return domain.Ticket{}, err
	}
	ticket, _ := idx.ticket(slug)
	return ticket, nil
}

// Slugs returns every indexed slug for shell completion. A missing Tickets
// home completes to nothing.
func (s *Service) Slugs() ([]string, error) {
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	idx, err := buildIndex(home)
	if clierr.CodeOf(err) == clierr.NotFound {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return idx.slugs, nil
}

// SetRequest carries the field changes of one set mutation. Each Set flag
// tells whether the caller passed that field.
type SetRequest struct {
	Status      string
	StatusSet   bool
	Priority    int
	PrioritySet bool
	Project     string
	ProjectSet  bool
}

// Set changes status, priority, or Project of one Ticket. A Project change moves
// the file. Set with StatusSet is the escape hatch for a Ticket that carries
// an unrecognized status.
func (s *Service) Set(ref string, req SetRequest, dryRun bool) (domain.Ticket, error) {
	if req.StatusSet && !domain.ValidTicketStatus(domain.TicketStatus(req.Status)) {
		return domain.Ticket{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "status %q is not valid", req.Status),
			"Use one of: %s.", strings.Join(domain.TicketStatuses(), ", "))
	}
	if req.PrioritySet && (req.Priority < 0 || req.Priority > 4) {
		return domain.Ticket{}, clierr.New(clierr.InvalidUsage, "priority %d is not in the range 0 to 4", req.Priority)
	}
	home, err := s.home()
	if err != nil {
		return domain.Ticket{}, err
	}
	if req.ProjectSet {
		if req.Project == "" {
			return domain.Ticket{}, clierr.WithHint(
				clierr.New(clierr.InvalidUsage, "the Project name is empty"),
				"Pass a Project name.")
		}
		info, statErr := os.Stat(filepath.Join(home, req.Project))
		if statErr != nil || !info.IsDir() {
			return domain.Ticket{}, projectMissing(req.Project)
		}
	}
	return s.mutate(ref, dryRun, req.StatusSet, func(m *mutation) error {
		if req.StatusSet {
			setMapString(m.mapping, "status", req.Status)
		}
		if req.PrioritySet {
			setMapInt(m.mapping, "priority", req.Priority)
		}
		if req.ProjectSet {
			destination := filepath.Join(home, req.Project, m.ticket.Slug+".md")
			if destination != m.destPath {
				if fileExists(destination) {
					return clierr.New(clierr.AlreadyExists, "ticket file %q already exists", destination)
				}
				m.destPath = destination
			}
		}
		return nil
	})
}

// Claim writes claimant into an unclaimed Ticket. The compare-and-set runs
// under the per-ticket lock: an empty claim succeeds, the same claimant
// succeeds without a write, and a different claimant gets locked.
func (s *Service) Claim(ref, claimant string, dryRun bool) (domain.Ticket, error) {
	claimant, err := validClaimant(claimant)
	if err != nil {
		return domain.Ticket{}, err
	}
	return s.mutate(ref, dryRun, false, func(m *mutation) error {
		current := m.ticket.ClaimedBy
		if current == claimant {
			m.skipWrite = true
			return nil
		}
		if current != "" {
			return claimedByOther(m.ticket.Slug, current)
		}
		setMapString(m.mapping, "claimed_by", claimant)
		setMapDate(m.mapping, "claimed_at", s.today())
		return nil
	})
}

// Unclaim clears the claim of claimant. An unclaimed Ticket succeeds without
// a write. A different claimant gets locked.
func (s *Service) Unclaim(ref, claimant string, dryRun bool) (domain.Ticket, error) {
	claimant, err := validClaimant(claimant)
	if err != nil {
		return domain.Ticket{}, err
	}
	return s.mutate(ref, dryRun, false, func(m *mutation) error {
		current := m.ticket.ClaimedBy
		if current == "" {
			m.skipWrite = true
			return nil
		}
		if current != claimant {
			return claimedByOther(m.ticket.Slug, current)
		}
		setMapNull(m.mapping, "claimed_by")
		setMapNull(m.mapping, "claimed_at")
		return nil
	})
}

// Close resolves one Ticket in one write: the status becomes done and the
// claim fields become null. The claim check matches Unclaim: an unclaimed
// Ticket or the same claimant proceeds, and a different claimant gets
// locked.
func (s *Service) Close(ref, claimant string, dryRun bool) (domain.Ticket, error) {
	claimant, err := validClaimant(claimant)
	if err != nil {
		return domain.Ticket{}, err
	}
	// Close writes the status, so it is a resolution escape hatch like Set
	// with --status: it overwrites an unrecognized legacy status instead of
	// refusing the mutation.
	return s.mutate(ref, dryRun, true, func(m *mutation) error {
		if current := m.ticket.ClaimedBy; current != "" && current != claimant {
			return claimedByOther(m.ticket.Slug, current)
		}
		setMapString(m.mapping, "status", string(domain.TicketDone))
		setMapNull(m.mapping, "claimed_by")
		setMapNull(m.mapping, "claimed_at")
		return nil
	})
}

var commentsHeading = regexp.MustCompile(`(?m)^## Comments\s*$`)

// Comment appends text under the "## Comments" heading and creates that
// heading when it is missing.
func (s *Service) Comment(ref, text string, dryRun bool) (domain.Ticket, error) {
	if strings.TrimSpace(text) == "" {
		return domain.Ticket{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the comment text is empty"),
			"Pass the comment text on stdin.")
	}
	return s.mutate(ref, dryRun, false, func(m *mutation) error {
		body := m.file.Body
		if !commentsHeading.MatchString(body) {
			body = strings.TrimRight(body, "\n") + "\n\n## Comments\n"
		}
		m.file.Body = strings.TrimRight(body, "\n") + "\n\n" + strings.TrimRight(text, "\n") + "\n"
		return nil
	})
}

// Edit replaces the whole body of one Ticket. The frontmatter stays as it
// is, except the updated date and the project heal.
func (s *Service) Edit(ref, body string, dryRun bool) (domain.Ticket, error) {
	if strings.TrimSpace(body) == "" {
		return domain.Ticket{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the new body is empty: refusing to erase the ticket body"),
			"Pass the new body on stdin.")
	}
	return s.mutate(ref, dryRun, false, func(m *mutation) error {
		m.file.Body = "\n" + strings.Trim(body, "\n") + "\n"
		return nil
	})
}

// mutation is one in-progress ticket write. apply functions change the
// mapping nodes, the body, or the destination path. skipWrite ends the
// mutation with success and without a write.
type mutation struct {
	file      *TicketFile
	mapping   *yaml.Node
	ticket    domain.Ticket
	destPath  string
	skipWrite bool
}

// mutate is the shared write path: resolve, lock, re-read under the lock,
// apply, bump updated, heal project, render, and write atomically. A dry run
// performs every check and skips only the write and the source removal.
func (s *Service) mutate(ref string, dryRun, allowUnknownStatus bool, apply func(*mutation) error) (domain.Ticket, error) {
	home, err := s.home()
	if err != nil {
		return domain.Ticket{}, err
	}
	idx, err := buildIndex(home)
	if err != nil {
		return domain.Ticket{}, err
	}
	slug, err := idx.resolve(home, ref)
	if err != nil {
		return domain.Ticket{}, err
	}
	lock, err := store.AcquireNamedLock(s.options.StateDir, "ticket", slug)
	if err != nil {
		return domain.Ticket{}, err
	}
	defer lock.Release()
	path := idx.path(slug)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return domain.Ticket{}, clierr.New(clierr.NotFound, "ticket file %q no longer exists", path)
	}
	if err != nil {
		return domain.Ticket{}, fmt.Errorf("read ticket %q: %w", path, err)
	}
	file, err := ParseTicketFile(path, raw)
	if err != nil {
		return domain.Ticket{}, err
	}
	ticket, err := decodeTicket(file, slug)
	if err != nil {
		return domain.Ticket{}, err
	}
	ticket.Project = projectOf(home, path)
	if !allowUnknownStatus && !domain.ValidTicketStatus(ticket.Status) {
		return domain.Ticket{}, clierr.WithHint(
			clierr.New(clierr.UnsafeState, "ticket %q has unrecognized status %q", slug, ticket.Status),
			"Set one of %s with 'twt tickets set %s --status STATUS'.",
			strings.Join(domain.TicketStatuses(), ", "), slug)
	}
	m := &mutation{file: file, mapping: file.ensureMapping(), ticket: ticket, destPath: path}
	if err := apply(m); err != nil {
		return domain.Ticket{}, err
	}
	if m.skipWrite {
		return m.ticket, nil
	}
	setMapDate(m.mapping, "updated", s.today())
	healProject(m.mapping, home, m.destPath)
	data, err := m.file.Render()
	if err != nil {
		return domain.Ticket{}, err
	}
	result, err := decodeTicket(m.file, slug)
	if err != nil {
		return domain.Ticket{}, err
	}
	result.Path = m.destPath
	result.Project = projectOf(home, m.destPath)
	if dryRun {
		return result, nil
	}
	if err := store.WriteFileAtomic(m.destPath, data, 0o644, "Ticket"); err != nil {
		return domain.Ticket{}, err
	}
	if m.destPath != path {
		if err := os.Remove(path); err != nil {
			return domain.Ticket{}, fmt.Errorf("remove moved ticket %q: %w", path, err)
		}
	}
	return result, nil
}

// decodeTicket reads the frontmatter into a Ticket without strict fields, so
// legacy keys survive. A missing priority keeps the default 2. A missing
// title falls back to the first H1 line, then to the slug. blocked_by
// entries normalize to bare slugs in memory only; a mutation never rewrites
// blocked_by on disk.
func decodeTicket(file *TicketFile, slug string) (domain.Ticket, error) {
	ticket := domain.Ticket{Priority: 2}
	if mapping := file.Mapping(); mapping != nil {
		if err := mapping.Decode(&ticket); err != nil {
			return domain.Ticket{}, clierr.Wrap(clierr.UnsafeState,
				fmt.Errorf("ticket file %q has invalid frontmatter: %w", file.Path, err))
		}
	}
	ticket.Slug = slug
	ticket.Path = file.Path
	if strings.TrimSpace(ticket.Title) == "" {
		ticket.Title = bodyTitle(file.Body, slug)
	}
	normalized := make([]string, 0, len(ticket.BlockedBy))
	for _, blocker := range ticket.BlockedBy {
		if bare := stripWikiLink(blocker); bare != "" {
			normalized = append(normalized, bare)
		}
	}
	ticket.BlockedBy = normalized
	return ticket, nil
}

// bodyTitle returns the first H1 heading of the body, or the slug.
func bodyTitle(body, slug string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			if title := strings.TrimSpace(line[2:]); title != "" {
				return title
			}
		}
	}
	return slug
}

// projectOf derives the Project of a ticket path from its directory.
func projectOf(home, path string) string {
	directory := filepath.Dir(path)
	if directory == filepath.Clean(home) {
		return ""
	}
	return filepath.Base(directory)
}

// healProject writes the path-derived Project into the frontmatter. An ungrouped
// ticket gets a null project.
func healProject(mapping *yaml.Node, home, path string) {
	deleteMapKey(mapping, "board")
	if project := projectOf(home, path); project != "" {
		setMapString(mapping, "project", project)
		return
	}
	setMapNull(mapping, "project")
}

// validClaimant checks the claimant name against the twt resource-name
// rules.
func validClaimant(claimant string) (string, error) {
	claimant = strings.TrimSpace(claimant)
	if claimant == "" {
		return "", clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the claimant name is empty"),
			"Pass --as NAME or set TWT_CLAIMANT.")
	}
	if err := store.ValidateResourceName(claimant); err != nil {
		return "", clierr.Wrap(clierr.InvalidUsage, err)
	}
	return claimant, nil
}

func claimedByOther(slug, current string) error {
	return clierr.WithHint(
		clierr.New(clierr.Locked, "ticket %q is claimed by %q", slug, current),
		"Select a ticket from 'twt tickets list --ready'.")
}

func projectMissing(name string) error {
	return clierr.WithHint(
		clierr.New(clierr.NotFound, "Project %q does not exist", name),
		"Run 'twt projects create %s'.", name)
}

func homeMissing(home string) error {
	return clierr.WithHint(
		clierr.New(clierr.NotFound, "Tickets home %q does not exist", home),
		"Run 'twt tickets init'.")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
