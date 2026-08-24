package ticket

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// ambiguityHintLimit caps how many candidate slugs an ambiguity hint names.
const ambiguityHintLimit = 8

// index is one walk over the Tickets home. Slugs come from file stems. Keys
// of byTitle and byAlias are lowercased and map to slugs. skipped keeps the
// parse error of each file that the walk indexed by slug only.
type index struct {
	root    string
	bySlug  map[string][]string
	byTitle map[string][]string
	byAlias map[string][]string
	slugs   []string
	skipped map[string]error
	// tickets and bodies are keyed by path, so a duplicate slug keeps both
	// files visible to List.
	tickets map[string]domain.Ticket
	bodies  map[string]string
}

// buildIndex walks home once. Only regular *.md files at depth zero (the
// home) or depth one (inside one Project directory) are Tickets. index.md at
// any level, the templates directory, and dot directories are not.
func buildIndex(home string) (*index, error) {
	root := filepath.Clean(home)
	idx := &index{
		root:    root,
		bySlug:  map[string][]string{},
		byTitle: map[string][]string{},
		byAlias: map[string][]string{},
		skipped: map[string]error{},
		tickets: map[string]domain.Ticket{},
		bodies:  map[string]string{},
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root && os.IsNotExist(walkErr) {
				return homeMissing(root)
			}
			return walkErr
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if relative == "." {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || relative == "templates" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		if entry.Name() == "index.md" {
			return nil
		}
		depth := strings.Count(relative, string(filepath.Separator))
		if depth > 1 {
			return nil
		}
		idx.add(path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	idx.slugs = make([]string, 0, len(idx.bySlug))
	for slug := range idx.bySlug {
		idx.slugs = append(idx.slugs, slug)
	}
	sort.Strings(idx.slugs)
	return idx, nil
}

// add indexes one ticket file. A file that fails to parse still gets its slug
// so a direct reference reports the stored error instead of "not found".
func (x *index) add(path string) {
	slug := strings.TrimSuffix(filepath.Base(path), ".md")
	x.bySlug[slug] = append(x.bySlug[slug], path)
	raw, err := os.ReadFile(path)
	if err != nil {
		x.skipped[slug] = fmt.Errorf("read ticket %q: %w", path, err)
		return
	}
	file, err := ParseTicketFile(path, raw)
	if err != nil {
		x.skipped[slug] = err
		return
	}
	ticket, err := decodeTicket(file, slug)
	if err != nil {
		x.skipped[slug] = err
		return
	}
	// The directory is the truth for the Project, so a stale project key never
	// misfiles a ticket in a listing.
	ticket.Project = projectOf(x.root, path)
	x.tickets[path] = ticket
	x.bodies[path] = file.Body
	if ticket.Title != "" {
		key := strings.ToLower(ticket.Title)
		x.byTitle[key] = appendUnique(x.byTitle[key], slug)
	}
	for _, alias := range ticket.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		key := strings.ToLower(alias)
		x.byAlias[key] = appendUnique(x.byAlias[key], slug)
	}
}

func appendUnique(slugs []string, slug string) []string {
	for _, existing := range slugs {
		if existing == slug {
			return slugs
		}
	}
	return append(slugs, slug)
}

// resolve maps a user reference to one indexed slug. Match order: exact
// slug, unique slug prefix, title, alias, wiki-link, then path.
func (x *index) resolve(home, ref string) (string, error) {
	if slug, ok, err := x.resolveName(ref); ok || err != nil {
		return slug, err
	}
	if strings.HasPrefix(strings.TrimSpace(ref), "[[") {
		if slug, ok, err := x.resolveName(stripWikiLink(ref)); ok || err != nil {
			return slug, err
		}
	}
	if strings.Contains(ref, "/") || strings.HasSuffix(ref, ".md") {
		if slug, ok, err := x.resolvePath(home, ref); ok || err != nil {
			return slug, err
		}
	}
	return "", clierr.WithHint(
		clierr.New(clierr.NotFound, "no ticket matches %q", ref),
		"Run 'twt tickets list' to see the tickets.")
}

// resolveName runs the slug, prefix, title, and alias steps.
func (x *index) resolveName(ref string) (string, bool, error) {
	if _, exact := x.bySlug[ref]; exact {
		slug, err := x.check(ref)
		return slug, true, err
	}
	var prefixed []string
	for _, slug := range x.slugs {
		if strings.HasPrefix(slug, ref) && ref != "" {
			prefixed = append(prefixed, slug)
		}
	}
	if len(prefixed) == 1 {
		slug, err := x.check(prefixed[0])
		return slug, true, err
	}
	if len(prefixed) > 1 {
		return "", true, ambiguous(ref, prefixed)
	}
	lower := strings.ToLower(ref)
	for _, candidates := range [][]string{x.byTitle[lower], x.byAlias[lower]} {
		if len(candidates) == 1 {
			slug, err := x.check(candidates[0])
			return slug, true, err
		}
		if len(candidates) > 1 {
			sorted := append([]string(nil), candidates...)
			sort.Strings(sorted)
			return "", true, ambiguous(ref, sorted)
		}
	}
	return "", false, nil
}

// resolvePath resolves a path reference against the working directory and
// against home. The result must stay inside home and must be indexed.
func (x *index) resolvePath(home, ref string) (string, bool, error) {
	root := filepath.Clean(home)
	candidates := []string{}
	if absolute, err := filepath.Abs(ref); err == nil {
		candidates = append(candidates, filepath.Clean(absolute))
	}
	candidates = append(candidates, filepath.Clean(filepath.Join(root, ref)))
	for _, candidate := range candidates {
		if !insideDirectory(root, candidate) {
			continue
		}
		for slug, paths := range x.bySlug {
			for _, path := range paths {
				if path == candidate {
					resolved, err := x.check(slug)
					return resolved, true, err
				}
			}
		}
	}
	return "", false, nil
}

// check enforces the slug invariants after a match: one path per slug and a
// parsable file.
func (x *index) check(slug string) (string, error) {
	paths := x.bySlug[slug]
	if len(paths) > 1 {
		sorted := append([]string(nil), paths...)
		sort.Strings(sorted)
		return "", clierr.WithHint(
			clierr.New(clierr.UnsafeState, "ticket slug %q exists at %q and at %q", slug, sorted[0], sorted[1]),
			"Rename one of the files so each slug is unique.")
	}
	if err, isSkipped := x.skipped[slug]; isSkipped {
		return "", err
	}
	return slug, nil
}

func ambiguous(ref string, candidates []string) error {
	shown := candidates
	if len(shown) > ambiguityHintLimit {
		shown = shown[:ambiguityHintLimit]
	}
	return clierr.WithHint(
		clierr.New(clierr.InvalidUsage, "ticket reference %q is ambiguous", ref),
		"Use one of: %s.", strings.Join(shown, ", "))
}

// path returns the single indexed path of slug.
func (x *index) path(slug string) string {
	paths := x.bySlug[slug]
	if len(paths) != 1 {
		return ""
	}
	return paths[0]
}

// ticket returns the parsed ticket of slug.
func (x *index) ticket(slug string) (domain.Ticket, bool) {
	path := x.path(slug)
	if path == "" {
		return domain.Ticket{}, false
	}
	ticket, ok := x.tickets[path]
	return ticket, ok
}

// blockerClosed reports whether the blocker slug resolves to shipped or
// declined work. A missing, duplicated, or unparsable blocker blocks.
func (x *index) blockerClosed(slug string) bool {
	paths := x.bySlug[slug]
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		ticket, ok := x.tickets[path]
		if !ok {
			return false
		}
		if ticket.Status != domain.TicketDone && ticket.Status != domain.TicketWontfix {
			return false
		}
	}
	return true
}

// missingBlocker reports whether no file carries the blocker slug.
func (x *index) missingBlocker(slug string) bool {
	return len(x.bySlug[slug]) == 0
}

// ready reports whether a ticket is pickable work: ready-for-agent,
// unclaimed, and every blocker closed.
func (x *index) ready(ticket domain.Ticket) bool {
	if ticket.Status != domain.TicketReadyForAgent || ticket.ClaimedBy != "" {
		return false
	}
	for _, blocker := range ticket.BlockedBy {
		if !x.blockerClosed(blocker) {
			return false
		}
	}
	return true
}

// insideDirectory reports whether path is root or inside root.
func insideDirectory(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
