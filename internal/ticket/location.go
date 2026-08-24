package ticket

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

const (
	closedDirectoryName = "closed"
	closedMarkerName    = ".twt-closed"
)

const closedMarkerContent = "twt closed tickets\n"

// ticketLocation is the logical location of one Ticket. Project stays
// independent from the physical closed directory.
type ticketLocation struct {
	Closed  bool
	Project string
}

// classifyTicketPath maps each supported path shape to its logical location.
// It does not read the file or infer location from frontmatter.
func classifyTicketPath(home, path string) (ticketLocation, error) {
	root := filepath.Clean(home)
	relative, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ticketLocation{}, fmt.Errorf("ticket path %q is not under Tickets home %q", path, root)
	}
	parts := strings.Split(relative, string(filepath.Separator))
	switch len(parts) {
	case 1:
		return ticketLocation{}, nil
	case 2:
		if parts[0] == closedDirectoryName {
			return ticketLocation{Closed: true}, nil
		}
		return ticketLocation{Project: parts[0]}, nil
	case 3:
		if parts[0] == closedDirectoryName {
			return ticketLocation{Closed: true, Project: parts[1]}, nil
		}
	}
	return ticketLocation{}, fmt.Errorf("ticket path %q has an unsupported location under Tickets home %q", path, root)
}

// canonicalTicketPath returns the one supported path for a Ticket state.
func canonicalTicketPath(home string, status domain.TicketStatus, project, slug string) string {
	parts := []string{filepath.Clean(home)}
	if closedStatus(status) {
		parts = append(parts, closedDirectoryName)
	}
	if project != "" {
		parts = append(parts, project)
	}
	return filepath.Join(append(parts, slug+".md")...)
}

func reservedProjectName(name string) bool {
	return strings.EqualFold(name, "templates") || strings.EqualFold(name, closedDirectoryName)
}

// closedRootExists validates the marker that distinguishes the closed Ticket
// tree from a legacy Project named closed.
func closedRootExists(home string) (bool, error) {
	home = filepath.Clean(home)
	entries, readErr := os.ReadDir(home)
	if errors.Is(readErr, os.ErrNotExist) {
		return false, nil
	}
	if readErr != nil {
		return false, fmt.Errorf("inspect Tickets home for the closed directory: %w", readErr)
	}
	entryName := ""
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), closedDirectoryName) {
			entryName = entry.Name()
			break
		}
	}
	if entryName == "" {
		return false, nil
	}
	root := filepath.Join(home, entryName)
	if entryName != closedDirectoryName {
		return false, closedRootConflict(root, fmt.Sprintf("its name must be exactly %q", closedDirectoryName))
	}
	info, err := os.Lstat(root)
	if err != nil {
		return false, fmt.Errorf("inspect closed Tickets directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, closedRootConflict(root, "it is not a regular directory")
	}
	marker := filepath.Join(root, closedMarkerName)
	markerInfo, err := os.Lstat(marker)
	if errors.Is(err, os.ErrNotExist) {
		return false, closedRootConflict(root, fmt.Sprintf("it has no %s marker", closedMarkerName))
	}
	if err != nil {
		return false, fmt.Errorf("inspect closed Tickets marker: %w", err)
	}
	if markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() {
		return false, closedRootConflict(root, fmt.Sprintf("its %s marker is not a regular file", closedMarkerName))
	}
	markerContent, err := os.ReadFile(marker)
	if err != nil {
		return false, fmt.Errorf("read closed Tickets marker: %w", err)
	}
	if string(markerContent) != closedMarkerContent {
		return false, closedRootConflict(root, fmt.Sprintf("its %s marker has unexpected content", closedMarkerName))
	}
	return true, nil
}

// ensureClosedRoot creates the marked closed Ticket tree when it is missing.
// A dry run performs all conflict checks but writes nothing.
func ensureClosedRoot(home string, dryRun bool) error {
	exists, err := closedRootExists(home)
	if err != nil || exists || dryRun {
		return err
	}
	root := filepath.Join(filepath.Clean(home), closedDirectoryName)
	if err := os.Mkdir(root, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			_, validateErr := closedRootExists(home)
			return validateErr
		}
		return fmt.Errorf("create closed Tickets directory: %w", err)
	}
	marker := filepath.Join(root, closedMarkerName)
	if err := store.WriteFileExclusiveAtomic(marker, []byte(closedMarkerContent), 0o644, "closed Tickets marker"); err != nil {
		if errors.Is(err, os.ErrExist) {
			_, validateErr := closedRootExists(home)
			return validateErr
		}
		return err
	}
	return nil
}

// projectDirectoryExists accepts only a real directory at the Project path.
// It does not follow a symbolic link out of Tickets home.
func projectDirectoryExists(home, project string) (bool, error) {
	return regularTicketDirectory(filepath.Join(filepath.Clean(home), project), "Project")
}

func regularTicketDirectory(path, label string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s directory: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, clierr.New(clierr.UnsafeState, "the %s path %q is not a regular directory", label, path)
	}
	return true, nil
}

// ensureTicketDirectory validates the active Project and prepares the
// canonical parent directory. A dry run performs the same checks but does not
// create the closed tree or its Project mirror.
func ensureTicketDirectory(home string, status domain.TicketStatus, project string, dryRun bool) error {
	if project != "" {
		exists, err := projectDirectoryExists(home, project)
		if err != nil {
			return err
		}
		if !exists {
			return projectMissing(project)
		}
	}
	if !closedStatus(status) {
		return nil
	}
	if err := ensureClosedRoot(home, dryRun); err != nil || project == "" {
		return err
	}
	directory := filepath.Join(filepath.Clean(home), closedDirectoryName, project)
	exists, err := regularTicketDirectory(directory, "closed Project")
	if err != nil || exists || dryRun {
		return err
	}
	if err := os.Mkdir(directory, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			_, validateErr := regularTicketDirectory(directory, "closed Project")
			return validateErr
		}
		return fmt.Errorf("create closed Project directory: %w", err)
	}
	return nil
}

func closedRootConflict(path, reason string) error {
	return clierr.WithHint(
		clierr.New(clierr.UnsafeState, "the reserved closed Tickets path %q cannot be used because %s", path, reason),
		"Move or rename the existing path, then run 'twt tickets init'.")
}
