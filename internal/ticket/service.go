package ticket

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	// Sync configures git synchronization of the Tickets home. The zero
	// value disables it.
	Sync SyncOptions
	// Logf receives best-effort sync warnings. Nil means silent.
	Logf func(format string, a ...any)
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

// HomePath returns the cleaned Tickets home for callers that stamp it into
// an agent environment.
func (s *Service) HomePath() (string, error) {
	return s.home()
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
	Home              string `json:"home"`
	WroteIndex        bool   `json:"wroteIndex"`
	WroteTemplate     bool   `json:"wroteTemplate"`
	WroteClosedMarker bool   `json:"wroteClosedMarker"`
	WroteGitignore    bool   `json:"wroteGitignore"`
}

// Init creates the Tickets home with its hub index and create template. It
// writes each file only when that file is missing. It never overwrites notes.
func (s *Service) Init(dryRun bool) (InitResult, error) {
	return syncWrite(s, syncBestEffort, dryRun, func() string {
		return "twt: init tickets home"
	}, func() (InitResult, error) {
		return s.initOnce(dryRun)
	})
}

func (s *Service) initOnce(dryRun bool) (InitResult, error) {
	home, err := s.home()
	if err != nil {
		return InitResult{}, err
	}
	indexPath := filepath.Join(home, "index.md")
	templatePath := filepath.Join(home, "templates", "ticket.md")
	gitignorePath := filepath.Join(home, ".gitignore")
	closedExists, err := closedRootExists(home)
	if err != nil {
		return InitResult{}, err
	}
	result := InitResult{
		Home:              home,
		WroteIndex:        !fileExists(indexPath),
		WroteTemplate:     !fileExists(templatePath),
		WroteClosedMarker: !closedExists,
		WroteGitignore:    !fileExists(gitignorePath),
	}
	if dryRun {
		return result, nil
	}
	if err := os.MkdirAll(filepath.Join(home, "templates"), 0o755); err != nil {
		return InitResult{}, fmt.Errorf("create Tickets home: %w", err)
	}
	if err := ensureClosedRoot(home, false); err != nil {
		return InitResult{}, err
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
	if result.WroteGitignore {
		// Atomic-write temp files must never enter a sync commit.
		if err := store.WriteFileAtomic(gitignorePath, []byte(".twt-write-*\n"), 0o644, "Tickets gitignore"); err != nil {
			return InitResult{}, err
		}
	}
	return result, nil
}

// CreateProject creates one Project directory and writes its index.md only when
// that file is missing.
func (s *Service) CreateProject(name string, dryRun bool) (domain.Project, error) {
	return s.CreateProjectWithTemplate(name, "", dryRun)
}

// CreateProjectWithTemplate creates one Project and saves its Workspace
// Template reference. An empty templateName keeps the current reference.
func (s *Service) CreateProjectWithTemplate(name, templateName string, dryRun bool) (domain.Project, error) {
	return syncWrite(s, syncBestEffort, dryRun, func() string {
		return fmt.Sprintf("twt: create project %s", name)
	}, func() (domain.Project, error) {
		return s.createProjectWithTemplateOnce(name, templateName, dryRun)
	})
}

func (s *Service) createProjectWithTemplateOnce(name, templateName string, dryRun bool) (domain.Project, error) {
	if templateName != "" {
		if err := store.ValidateResourceName(templateName); err != nil {
			return domain.Project{}, clierr.Wrap(clierr.InvalidUsage, err)
		}
	}
	home, err := s.home()
	if err != nil {
		return domain.Project{}, err
	}
	if err := store.ValidateResourceName(name); err != nil {
		return domain.Project{}, clierr.Wrap(clierr.InvalidUsage, err)
	}
	if reservedProjectName(name) {
		return domain.Project{}, clierr.New(clierr.InvalidUsage, "the Project name %q is reserved", name)
	}
	if _, err := closedRootExists(home); err != nil {
		return domain.Project{}, err
	}
	path := filepath.Join(home, name)
	exists, err := projectDirectoryExists(home, name)
	if err != nil {
		return domain.Project{}, err
	}
	indexPath := filepath.Join(path, "index.md")
	if dryRun {
		project := domain.Project{Name: name, Path: path}
		if exists {
			project, err = s.projectInfo(home, name)
			if err != nil {
				return domain.Project{}, err
			}
		}
		project.HasIndex = true
		if templateName != "" {
			project.TemplateName = templateName
		}
		return project, nil
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return domain.Project{}, fmt.Errorf("create Project directory: %w", err)
	}
	if _, err := projectDirectoryExists(home, name); err != nil {
		return domain.Project{}, err
	}
	if !fileExists(indexPath) {
		if err := store.WriteFileAtomic(indexPath, projectIndexContent(name, s.today()), 0o644, "Project index"); err != nil {
			return domain.Project{}, err
		}
	}
	if templateName != "" {
		return s.setProjectTemplateOnce(name, templateName, false)
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
	if _, err := closedRootExists(home); err != nil {
		return nil, err
	}
	projects := []domain.Project{}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || reservedProjectName(name) || strings.HasPrefix(name, ".") {
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
	if reservedProjectName(name) {
		return domain.Project{}, projectMissing(name)
	}
	if _, err := closedRootExists(home); err != nil {
		return domain.Project{}, err
	}
	exists, err := projectDirectoryExists(home, name)
	if err != nil {
		return domain.Project{}, err
	}
	if !exists {
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
			indexPath := filepath.Join(path, entry.Name())
			raw, readErr := os.ReadFile(indexPath)
			if readErr != nil {
				return domain.Project{}, fmt.Errorf("read Project %q index: %w", name, readErr)
			}
			file, parseErr := ParseTicketFile(indexPath, raw)
			if parseErr != nil {
				return domain.Project{}, parseErr
			}
			if value := findMapValue(file.Mapping(), "twt_template"); value != nil && value.Tag != "!!null" {
				if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					return domain.Project{}, clierr.New(clierr.UnsafeState, "Project %q has an invalid twt_template value", name)
				}
				project.TemplateName = strings.TrimSpace(value.Value)
			}
			continue
		}
		if entry.Name() == "plan.md" {
			project.HasPlan = true
			planPath := filepath.Join(path, entry.Name())
			if info, statErr := os.Stat(planPath); statErr == nil {
				project.PlanUpdatedAt = info.ModTime().UTC().Format(time.RFC3339)
			}
			project.PlanTitle = planTitle(planPath)
			continue
		}
		if reservedProjectFile(entry.Name()) {
			continue
		}
		project.Tickets++
	}
	closedPath := filepath.Join(home, closedDirectoryName, name)
	closedExists, closedErr := regularTicketDirectory(closedPath, "closed Project")
	if closedErr != nil {
		return domain.Project{}, closedErr
	}
	if !closedExists {
		return project, nil
	}
	closedEntries, closedErr := os.ReadDir(closedPath)
	if closedErr != nil && !errors.Is(closedErr, os.ErrNotExist) {
		return domain.Project{}, fmt.Errorf("read closed Tickets for Project %q: %w", name, closedErr)
	}
	for _, entry := range closedEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") && !reservedProjectFile(entry.Name()) {
			project.Tickets++
		}
	}
	return project, nil
}

// SetProjectTemplate saves the Workspace Template that supplies Cloud Session
// settings for a Project. The rest of index.md stays byte-stable apart from
// normalized frontmatter rendering.
func (s *Service) SetProjectTemplate(name, templateName string, dryRun bool) (domain.Project, error) {
	return syncWrite(s, syncBestEffort, dryRun, func() string {
		return fmt.Sprintf("twt: set project %s template", name)
	}, func() (domain.Project, error) {
		return s.setProjectTemplateOnce(name, templateName, dryRun)
	})
}

func (s *Service) setProjectTemplateOnce(name, templateName string, dryRun bool) (domain.Project, error) {
	if err := store.ValidateResourceName(templateName); err != nil {
		return domain.Project{}, clierr.Wrap(clierr.InvalidUsage, err)
	}
	home, err := s.home()
	if err != nil {
		return domain.Project{}, err
	}
	project, err := s.Project(name)
	if err != nil {
		return domain.Project{}, err
	}
	if !project.HasIndex {
		return domain.Project{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Project %q has no index.md", name),
			"Run 'twt projects create %s' to add the Project index.", name)
	}
	indexPath := filepath.Join(home, name, "index.md")
	lock, err := store.AcquireNamedLock(s.options.StateDir, "project", name)
	if err != nil {
		return domain.Project{}, err
	}
	defer lock.Release()
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return domain.Project{}, fmt.Errorf("read Project %q index: %w", name, err)
	}
	file, err := ParseTicketFile(indexPath, raw)
	if err != nil {
		return domain.Project{}, err
	}
	setMapString(file.ensureMapping(), "twt_template", templateName)
	content, err := file.Render()
	if err != nil {
		return domain.Project{}, err
	}
	project.TemplateName = templateName
	if dryRun {
		return project, nil
	}
	if err := store.WriteFileAtomic(indexPath, content, 0o644, "Project index"); err != nil {
		return domain.Project{}, err
	}
	return s.projectInfo(home, name)
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
	// BlockedBy is the list of blocker slugs or wiki-links. Create writes
	// them as wiki-links. An empty list writes blocked_by: [].
	BlockedBy []string
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
	return syncWrite(s, syncBestEffort, dryRun, func() string {
		slug := req.Slug
		if slug == "" {
			slug = domain.Slugify(req.Title)
		}
		return fmt.Sprintf("twt: create %s", slug)
	}, func() (CreateResult, error) {
		return s.createOnce(req, dryRun)
	})
}

func (s *Service) createOnce(req CreateRequest, dryRun bool) (CreateResult, error) {
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
	if domain.ReservedTicketSlug(slug) {
		return CreateResult{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the slug %q is reserved for Project metadata files", slug),
			"Pass --slug to select a different slug.")
	}
	blockedBy, err := normalizeBlockedBy(req.BlockedBy)
	if err != nil {
		return CreateResult{}, err
	}
	if err := rejectSelfBlock(slug, blockedBy); err != nil {
		return CreateResult{}, err
	}
	ticket := domain.Ticket{
		Slug:      slug,
		Title:     title,
		Aliases:   []string{title},
		Status:    status,
		Priority:  priority,
		Project:   req.Project,
		BlockedBy: blockedBy,
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
	projectExists := true
	if req.Project != "" {
		if req.EnsureProject {
			if _, err := s.createProjectWithTemplateOnce(req.Project, "", dryRun); err != nil {
				return CreateResult{}, err
			}
		}
		projectExists, err = projectDirectoryExists(home, req.Project)
		if err != nil {
			return CreateResult{}, err
		}
		if !projectExists && !(dryRun && req.EnsureProject) {
			return CreateResult{}, projectMissing(req.Project)
		}
	}
	path := canonicalTicketPath(home, status, req.Project, slug)
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
	if projectExists {
		if err := ensureTicketDirectory(home, status, req.Project, dryRun); err != nil {
			return CreateResult{}, err
		}
	} else if closedStatus(status) {
		if err := ensureClosedRoot(home, true); err != nil {
			return CreateResult{}, err
		}
	}
	if !dryRun {
		if err := store.WriteFileExclusiveAtomic(path, content, 0o644, "Ticket"); err != nil {
			if errors.Is(err, os.ErrExist) {
				return CreateResult{}, clierr.WithHint(
					clierr.New(clierr.AlreadyExists, "ticket slug %q already exists at %q", slug, path),
					"Pass --slug to select a different slug.")
			}
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
	setMapBlockedBy(mapping, ticket.BlockedBy)
	setMapNull(mapping, "claimed_by")
	setMapNull(mapping, "claimed_at")
	setMapStringList(mapping, "pull_requests", nil)
	setMapDate(mapping, "created", ticket.Created)
	setMapDate(mapping, "updated", ticket.Updated)
	file.Body = newTicketBody(ticket.Title, body, ticket.BlockedBy)
	return file.Render()
}

// newTicketBody builds the initial body: the H1, then the given body or the
// spec skeleton.
func newTicketBody(title, body string, blockedBy []string) string {
	if strings.TrimSpace(body) == "" {
		return fmt.Sprintf(`
# %s

## What to build

## Acceptance criteria

- [ ]

## Blocked by

%s

## Comments
`, title, blockedBySection(blockedBy))
	}
	return "\n# " + title + "\n\n" + strings.Trim(body, "\n") + "\n"
}

// blockedBySection is the default body list under ## Blocked by.
func blockedBySection(slugs []string) string {
	if len(slugs) == 0 {
		return "None - can start immediately"
	}
	lines := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		lines = append(lines, "- [["+slug+"]]")
	}
	return strings.Join(lines, "\n")
}

// normalizeBlockedBy turns wiki-links and bare slugs into unique bare slugs.
// Empty values drop out. An invalid slug is invalid_usage.
func normalizeBlockedBy(values []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		slug := stripWikiLink(value)
		if slug == "" {
			continue
		}
		if !domain.ValidTicketSlug(slug) {
			return nil, clierr.WithHint(
				clierr.New(clierr.InvalidUsage, "blocked_by value %q is not a ticket slug", value),
				"Pass a slug or a wiki-link such as [[other-ticket]].")
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	return out, nil
}

// rejectSelfBlock refuses a Ticket that lists itself as a blocker.
func rejectSelfBlock(slug string, blockers []string) error {
	for _, blocker := range blockers {
		if blocker == slug {
			return clierr.WithHint(
				clierr.New(clierr.InvalidUsage, "ticket %q cannot block itself", slug),
				"Remove %q from blocked_by.", slug)
		}
	}
	return nil
}

// ListFilter selects Tickets. ProjectSet with an empty Project selects only
// ungrouped Tickets. All includes the closed Tickets that the default list
// hides.
type ListFilter struct {
	Project    string
	ProjectSet bool
	Status     string
	Ready      bool
	Claimed    bool
	// NeedsInput selects Tickets whose agent is waiting on the human:
	// claimed and parked on needs-info by an ask.
	NeedsInput bool
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
	if filter.Ready && filter.Claimed {
		return nil, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "--ready and --claimed select different sets"),
			"Use --ready or --claimed, not both.")
	}
	if filter.Ready && filter.NeedsInput {
		return nil, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "--ready and --needs-input select different sets"),
			"Use --ready or --needs-input, not both.")
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
		if filter.Claimed && ticket.ClaimedBy == "" {
			continue
		}
		if filter.NeedsInput && (ticket.ClaimedBy == "" || ticket.Status != domain.TicketNeedsInfo) {
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
	Status       string
	StatusSet    bool
	Priority     int
	PrioritySet  bool
	Project      string
	ProjectSet   bool
	BlockedBy    []string
	BlockedBySet bool
}

// Set changes status, priority, Project, or blocked_by of one Ticket. A
// Project change moves the file. Set with StatusSet is the escape hatch for
// a Ticket that carries an unrecognized status.
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
		exists, directoryErr := projectDirectoryExists(home, req.Project)
		if directoryErr != nil {
			return domain.Ticket{}, directoryErr
		}
		if !exists {
			return domain.Ticket{}, projectMissing(req.Project)
		}
	}
	var blockedBy []string
	if req.BlockedBySet {
		var normalizeErr error
		blockedBy, normalizeErr = normalizeBlockedBy(req.BlockedBy)
		if normalizeErr != nil {
			return domain.Ticket{}, normalizeErr
		}
	}
	return s.mutate(ref, dryRun, req.StatusSet, syncBestEffort, func() string {
		return fmt.Sprintf("twt: set %s", ref)
	}, func(m *mutation) error {
		if req.StatusSet {
			setMapString(m.mapping, "status", req.Status)
			m.relocate = true
		}
		if req.PrioritySet {
			setMapInt(m.mapping, "priority", req.Priority)
		}
		if req.ProjectSet {
			m.project = req.Project
			m.relocate = true
		}
		if req.BlockedBySet {
			if err := rejectSelfBlock(m.ticket.Slug, blockedBy); err != nil {
				return err
			}
			setMapBlockedBy(m.mapping, blockedBy)
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
	return s.mutate(ref, dryRun, false, syncRequired, func() string {
		return fmt.Sprintf("twt: claim %s (as %s)", ref, claimant)
	}, func(m *mutation) error {
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

// ClaimReady claims one Ticket only when it is in the ready queue. The
// readiness check and claim write use the same per-Ticket lock.
func (s *Service) ClaimReady(ref, claimant string, dryRun bool) (domain.Ticket, error) {
	claimant, err := validClaimant(claimant)
	if err != nil {
		return domain.Ticket{}, err
	}
	return s.mutate(ref, dryRun, false, syncRequired, func() string {
		return fmt.Sprintf("twt: claim %s (as %s)", ref, claimant)
	}, func(m *mutation) error {
		if !m.index.ready(m.ticket) {
			return clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "ticket %q is not ready to claim", m.ticket.Slug),
				"Run 'twt tickets queue --project PROJECT' and select a ready Ticket.")
		}
		setMapString(m.mapping, "claimed_by", claimant)
		setMapDate(m.mapping, "claimed_at", s.today())
		return nil
	})
}

// CompleteClaim changes the Ticket status and clears one expected claim in one
// locked write.
func (s *Service) CompleteClaim(ref, claimant string, status domain.TicketStatus, dryRun bool) (domain.Ticket, error) {
	return s.CompleteWork(ref, claimant, status, nil, dryRun)
}

// CompleteWork records pull requests and completes one expected claim in one
// locked write. It is the worker-facing terminal mutation: the URL write and
// the claim clear cannot race a new claimant. A retry after success is a
// no-op when the status and the URLs already match.
func (s *Service) CompleteWork(ref, claimant string, status domain.TicketStatus, pullRequests []string, dryRun bool) (domain.Ticket, error) {
	claimant, err := validClaimant(claimant)
	if err != nil {
		return domain.Ticket{}, err
	}
	if status != domain.TicketReadyForAgent && status != domain.TicketReadyForHuman {
		return domain.Ticket{}, clierr.New(clierr.InvalidUsage,
			"claim completion status %q must be ready-for-agent or ready-for-human", status)
	}
	normalized := make([]string, 0, len(pullRequests))
	for _, raw := range pullRequests {
		value := strings.TrimSpace(raw)
		if err := validatePullRequestURL(value); err != nil {
			return domain.Ticket{}, clierr.Wrap(clierr.InvalidUsage, err)
		}
		normalized = append(normalized, value)
	}
	return s.mutate(ref, dryRun, false, syncRequired, func() string {
		return fmt.Sprintf("twt: complete %s (%s)", ref, status)
	}, func(m *mutation) error {
		merged, changed := mergePullRequests(m.ticket.PullRequests, normalized)
		if m.ticket.ClaimedBy == "" && m.ticket.Status == status && !changed {
			m.skipWrite = true
			return nil
		}
		if m.ticket.ClaimedBy == "" {
			return clierr.New(clierr.UnsafeState, "ticket %q no longer has the expected claim", m.ticket.Slug)
		}
		if m.ticket.ClaimedBy != claimant {
			return claimedByOther(m.ticket.Slug, m.ticket.ClaimedBy)
		}
		if len(merged) > 0 {
			setMapStringList(m.mapping, "pull_requests", merged)
		}
		setMapString(m.mapping, "status", string(status))
		setMapNull(m.mapping, "claimed_by")
		setMapNull(m.mapping, "claimed_at")
		m.relocate = true
		return nil
	})
}

// validatePullRequestURL accepts only HTTPS URLs with a host.
func validatePullRequestURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("pull request URL %q is not an HTTPS URL", value)
	}
	return nil
}

// mergePullRequests appends new URLs after the existing ones, dropping
// duplicates and keeping order.
func mergePullRequests(existing, additions []string) ([]string, bool) {
	merged := append([]string(nil), existing...)
	seen := make(map[string]bool, len(existing))
	for _, value := range existing {
		seen[value] = true
	}
	changed := false
	for _, value := range additions {
		if seen[value] {
			continue
		}
		seen[value] = true
		merged = append(merged, value)
		changed = true
	}
	return merged, changed
}

// Unclaim clears the claim of claimant. An unclaimed Ticket succeeds without
// a write. A different claimant gets locked.
func (s *Service) Unclaim(ref, claimant string, dryRun bool) (domain.Ticket, error) {
	claimant, err := validClaimant(claimant)
	if err != nil {
		return domain.Ticket{}, err
	}
	return s.mutate(ref, dryRun, false, syncRequired, func() string {
		return fmt.Sprintf("twt: unclaim %s (as %s)", ref, claimant)
	}, func(m *mutation) error {
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
		setMapNull(m.mapping, "twt_workspace_id")
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
	return s.mutate(ref, dryRun, true, syncRequired, func() string {
		return fmt.Sprintf("twt: close %s (as %s)", ref, claimant)
	}, func(m *mutation) error {
		if current := m.ticket.ClaimedBy; current != "" && current != claimant {
			return claimedByOther(m.ticket.Slug, current)
		}
		setMapString(m.mapping, "status", string(domain.TicketDone))
		setMapNull(m.mapping, "claimed_by")
		setMapNull(m.mapping, "claimed_at")
		setMapNull(m.mapping, "twt_workspace_id")
		m.relocate = true
		return nil
	})
}

// SetWorkspace stamps or clears the Workspace ID on one Ticket. An empty
// workspaceID clears the field. The stamp is the join key for a coordinator
// read: Ticket to Workspace.
func (s *Service) SetWorkspace(ref, workspaceID string, dryRun bool) (domain.Ticket, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != "" {
		if err := store.ValidateResourceName(workspaceID); err != nil {
			return domain.Ticket{}, clierr.Wrap(clierr.InvalidUsage, err)
		}
	}
	return s.mutate(ref, dryRun, false, syncBestEffort, func() string {
		return fmt.Sprintf("twt: set %s workspace", ref)
	}, func(m *mutation) error {
		if workspaceID == "" {
			if m.ticket.WorkspaceID == "" {
				m.skipWrite = true
				return nil
			}
			setMapNull(m.mapping, "twt_workspace_id")
			return nil
		}
		if m.ticket.WorkspaceID == workspaceID {
			m.skipWrite = true
			return nil
		}
		setMapString(m.mapping, "twt_workspace_id", workspaceID)
		return nil
	})
}


// Comment appends text under the "## Comments" heading and creates that
// heading when it is missing.
func (s *Service) Comment(ref, text string, dryRun bool) (domain.Ticket, error) {
	if strings.TrimSpace(text) == "" {
		return domain.Ticket{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the comment text is empty"),
			"Pass the comment text on stdin.")
	}
	return s.mutate(ref, dryRun, false, syncBestEffort, func() string {
		return fmt.Sprintf("twt: comment on %s", ref)
	}, func(m *mutation) error {
		// Append inside the Comments section, so comments land correctly
		// even when other sections (Plan, Questions) follow it.
		m.file.Body = appendBodySection(m.file.Body, "Comments", text)
		return nil
	})
}

// SetPlanSection replaces the "## Plan" body section of one Ticket, keeping
// every other section. A claimed Ticket requires the matching claimant; an
// unclaimed Ticket accepts any.
func (s *Service) SetPlanSection(ref, claimant, plan string, dryRun bool) (domain.Ticket, error) {
	if strings.TrimSpace(plan) == "" {
		return domain.Ticket{}, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the plan text is empty"),
			"Pass the plan text on stdin.")
	}
	return s.mutate(ref, dryRun, false, syncBestEffort, func() string {
		return fmt.Sprintf("twt: plan %s", ref)
	}, func(m *mutation) error {
		if m.ticket.ClaimedBy != "" && m.ticket.ClaimedBy != claimant {
			return claimedByOther(m.ticket.Slug, m.ticket.ClaimedBy)
		}
		m.file.Body = replaceBodySection(m.file.Body, "Plan", plan)
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
	return s.mutate(ref, dryRun, false, syncBestEffort, func() string {
		return fmt.Sprintf("twt: edit %s", ref)
	}, func(m *mutation) error {
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
	index     *index
	project   string
	destPath  string
	relocate  bool
	skipWrite bool
}

// mutate wraps one ticket write in the git sync round. The class selects the
// push policy, and message names the sync commit.
func (s *Service) mutate(ref string, dryRun, allowUnknownStatus bool, class syncClass, message func() string, apply func(*mutation) error) (domain.Ticket, error) {
	return syncWrite(s, class, dryRun, message, func() (domain.Ticket, error) {
		return s.mutateOnce(ref, dryRun, allowUnknownStatus, apply)
	})
}

// mutateOnce is the shared write path: resolve, lock, re-read under the lock,
// apply, bump updated, heal project, render, and write atomically. A dry run
// performs every check and skips only the write and the source removal.
func (s *Service) mutateOnce(ref string, dryRun, allowUnknownStatus bool, apply func(*mutation) error) (domain.Ticket, error) {
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
	// Rebuild the index after the lock. A previous mutation can move the file
	// between the initial resolution and this lock acquisition.
	idx, err = buildIndex(home)
	if err != nil {
		return domain.Ticket{}, err
	}
	if _, err := idx.resolve(home, slug); err != nil {
		return domain.Ticket{}, err
	}
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
	m := &mutation{file: file, mapping: file.ensureMapping(), ticket: ticket, index: idx, project: ticket.Project, destPath: path}
	if err := apply(m); err != nil {
		return domain.Ticket{}, err
	}
	if m.skipWrite {
		return m.ticket, nil
	}
	setMapDate(m.mapping, "updated", s.today())
	if m.relocate {
		located, decodeErr := decodeTicket(m.file, slug)
		if decodeErr != nil {
			return domain.Ticket{}, decodeErr
		}
		m.destPath = canonicalTicketPath(home, located.Status, m.project, slug)
	}
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
	result.Project = m.project
	if m.destPath != path {
		if err := ensureTicketDirectory(home, result.Status, m.project, dryRun); err != nil {
			return domain.Ticket{}, err
		}
	}
	if dryRun {
		return result, nil
	}
	if m.destPath != path {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return domain.Ticket{}, fmt.Errorf("inspect ticket %q before move: %w", path, statErr)
		}
		if err := moveTicketFile(path, m.destPath, data, info.Mode().Perm()); err != nil {
			return domain.Ticket{}, err
		}
		return result, nil
	}
	if err := store.WriteFileAtomic(m.destPath, data, 0o644, "Ticket"); err != nil {
		return domain.Ticket{}, err
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

// projectOf derives the Project from one supported active or closed path.
func projectOf(home, path string) string {
	location, err := classifyTicketPath(home, path)
	if err != nil {
		return ""
	}
	return location.Project
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
