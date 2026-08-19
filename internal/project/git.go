package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func (s *Service) ensureCache(p domain.Project, repositoryName string) error {
	spec, repository, err := repositoryFor(p, repositoryName)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(repository.CachePath); statErr == nil && info.IsDir() {
		if err := validateCacheMarker(repository.CachePath, spec.Clone.URL); err != nil {
			return err
		}
		origin, err := output(repository.CachePath, "git", "remote", "get-url", "origin")
		if err != nil {
			return fmt.Errorf("read origin for cache %q: %w", repositoryName, err)
		}
		if origin != spec.Clone.URL {
			return fmt.Errorf("cache %q has origin %q, but the Project requires %q", repositoryName, origin, spec.Clone.URL)
		}
		if err := s.ensureRemotes(repository.CachePath, spec.Remotes); err != nil {
			return err
		}
		return fetchOrigin(repository.CachePath, spec.Clone.Depth, spec.DefaultBranch)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect repository cache: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(repository.CachePath), 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	temporary, err := os.MkdirTemp(filepath.Dir(repository.CachePath), ".twt2-cache-*")
	if err != nil {
		return fmt.Errorf("create temporary cache path: %w", err)
	}
	if err := os.Remove(temporary); err != nil {
		return fmt.Errorf("prepare temporary cache path: %w", err)
	}
	defer os.RemoveAll(temporary)
	args := []string{"clone", "--bare"}
	if spec.Clone.Depth > 0 {
		args = append(args, "--depth", fmt.Sprint(spec.Clone.Depth))
	}
	args = append(args, spec.Clone.URL, temporary)
	if err := run("", "git", args...); err != nil {
		return fmt.Errorf("clone repository %q: %w", repositoryName, err)
	}
	marker := map[string]string{"owner": "twt2", "url": spec.Clone.URL}
	if err := writeJSON(filepath.Join(temporary, "twt2-ownership.json"), marker, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, repository.CachePath); err != nil {
		return fmt.Errorf("publish repository cache: %w", err)
	}
	if err := s.ensureRemotes(repository.CachePath, spec.Remotes); err != nil {
		return err
	}
	return fetchOrigin(repository.CachePath, spec.Clone.Depth, spec.DefaultBranch)
}

func (s *Service) ensureRemotes(cachePath string, remotes map[string]string) error {
	names := make([]string, 0, len(remotes))
	for name := range remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		url := remotes[name]
		existing, err := output(cachePath, "git", "remote", "get-url", name)
		if err == nil {
			if existing != url {
				return fmt.Errorf("cache remote %q is %q, but the Project requires %q", name, existing, url)
			}
			continue
		}
		if err := run(cachePath, "git", "remote", "add", name, url); err != nil {
			return fmt.Errorf("add cache remote %q: %w", name, err)
		}
	}
	return nil
}

func (s *Service) ensureCheckout(p domain.Project, repositoryName string) error {
	spec, repository, err := repositoryFor(p, repositoryName)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(repository.Path); statErr == nil && info.IsDir() {
		commonDir, err := output(repository.Path, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return fmt.Errorf("checkout path %q exists but is not a Git worktree", repository.Path)
		}
		if filepath.Clean(commonDir) != filepath.Clean(repository.CachePath) {
			return fmt.Errorf("checkout path %q belongs to a different repository cache", repository.Path)
		}
		branch, err := output(repository.Path, "git", "branch", "--show-current")
		if err != nil || branch != repository.Branch {
			return fmt.Errorf("checkout path %q is on branch %q, expected %q", repository.Path, branch, repository.Branch)
		}
		return nil
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect checkout path: %w", statErr)
	}
	if err := os.MkdirAll(filepath.Dir(repository.Path), 0o755); err != nil {
		return fmt.Errorf("create Project directory: %w", err)
	}
	startPoint := "HEAD"
	defaultBranch := spec.DefaultBranch
	if defaultBranch == "" {
		defaultBranch, err = output(repository.CachePath, "git", "symbolic-ref", "--short", "HEAD")
		if err != nil {
			return fmt.Errorf("find default branch for repository %q: %w", repositoryName, err)
		}
	}
	startPoint = "refs/remotes/origin/" + defaultBranch
	if err := run(repository.CachePath, "git", "worktree", "add", "-b", repository.Branch, repository.Path, startPoint); err != nil {
		return fmt.Errorf("create checkout for repository %q: %w", repositoryName, err)
	}
	return nil
}

func fetchOrigin(cachePath string, depth int, defaultBranch string) error {
	if defaultBranch == "" {
		var err error
		defaultBranch, err = output(cachePath, "git", "symbolic-ref", "--short", "HEAD")
		if err != nil {
			return fmt.Errorf("find default branch for repository cache: %w", err)
		}
	}
	args := []string{"fetch", "--prune", "--no-tags"}
	if depth > 0 {
		args = append(args, "--depth", fmt.Sprint(depth))
	}
	args = append(args, "origin", "+refs/heads/"+defaultBranch+":refs/remotes/origin/"+defaultBranch)
	if err := run(cachePath, "git", args...); err != nil {
		return fmt.Errorf("refresh repository cache from origin: %w", err)
	}
	return nil
}

func (s *Service) cachePath(name, url string) string {
	digest := sha256.Sum256([]byte(url))
	return filepath.Join(s.options.DataDir, "caches", name+"-"+hex.EncodeToString(digest[:6])+".git")
}

func repositoryFor(p domain.Project, name string) (domain.RepositorySpec, domain.ProjectRepository, error) {
	var spec *domain.RepositorySpec
	var repository *domain.ProjectRepository
	for index := range p.TemplateSnapshot.Repositories {
		if p.TemplateSnapshot.Repositories[index].Name == name {
			copy := p.TemplateSnapshot.Repositories[index]
			spec = &copy
			break
		}
	}
	for index := range p.Repositories {
		if p.Repositories[index].Name == name {
			copy := p.Repositories[index]
			repository = &copy
			break
		}
	}
	if spec == nil || repository == nil {
		return domain.RepositorySpec{}, domain.ProjectRepository{}, fmt.Errorf("repository %q is not in the saved Project snapshot", name)
	}
	return *spec, *repository, nil
}

func validateCacheMarker(cachePath, expectedURL string) error {
	data, err := os.ReadFile(filepath.Join(cachePath, "twt2-ownership.json"))
	if err != nil {
		return fmt.Errorf("cache %q has no valid twt2 ownership marker", cachePath)
	}
	var marker struct {
		Owner string `json:"owner"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(data, &marker); err != nil || marker.Owner != "twt2" || marker.URL != expectedURL {
		return fmt.Errorf("cache %q has a conflicting twt2 ownership marker", cachePath)
	}
	return nil
}

func branchIsPublished(cachePath, branch string) (bool, error) {
	refs, err := output(cachePath, "git", "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return false, fmt.Errorf("list refs for branch %q: %w", branch, err)
	}
	branchRef := "refs/heads/" + branch
	for _, ref := range strings.Split(refs, "\n") {
		if ref == "" || ref == branchRef || strings.HasPrefix(ref, "refs/heads/twt2/") {
			continue
		}
		command := exec.Command("git", "merge-base", "--is-ancestor", branchRef, ref)
		command.Dir = cachePath
		if err := command.Run(); err == nil {
			return true, nil
		} else if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			continue
		} else {
			return false, fmt.Errorf("compare branch %q with %q: %w", branch, ref, err)
		}
	}
	return false, nil
}

func refExists(cachePath, ref string) (bool, error) {
	command := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	command.Dir = cachePath
	if err := command.Run(); err == nil {
		return true, nil
	} else if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
		return false, nil
	} else {
		return false, fmt.Errorf("inspect Git ref %q: %w", ref, err)
	}
}
