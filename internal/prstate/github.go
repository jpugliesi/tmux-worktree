package prstate

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// GitHubResolver reads PR state through the gh CLI, which accepts the PR URL
// verbatim.
type GitHubResolver struct {
	lookPath func(string) (string, error)
	run      runCommand
}

func NewGitHubResolver() *GitHubResolver {
	return &GitHubResolver{lookPath: exec.LookPath, run: realRun}
}

func (r *GitHubResolver) Host() string { return "github.com" }

type githubView struct {
	State             string `json:"state"`
	IsDraft           bool   `json:"isDraft"`
	ReviewDecision    string `json:"reviewDecision"`
	UpdatedAt         string `json:"updatedAt"`
	StatusCheckRollup []struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"statusCheckRollup"`
}

func (r *GitHubResolver) Fetch(ctx context.Context, prURL string) (PRState, error) {
	if _, err := r.lookPath("gh"); err != nil {
		return PRState{}, fmt.Errorf("install the gh CLI to see GitHub PR state")
	}
	output, err := r.run(ctx, "gh", "pr", "view", prURL,
		"--json", "state,isDraft,reviewDecision,updatedAt,statusCheckRollup")
	if err != nil {
		return PRState{}, err
	}
	var view githubView
	if err := json.Unmarshal(output, &view); err != nil {
		return PRState{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	state := PRState{Checks: ChecksNone, ReviewDecision: ReviewUnknown}
	switch strings.ToUpper(view.State) {
	case "MERGED":
		state.State = StateMerged
	case "CLOSED":
		state.State = StateClosed
	case "OPEN":
		state.State = StateOpen
		if view.IsDraft {
			state.State = StateDraft
		}
	default:
		state.State = StateUnknown
	}
	switch strings.ToUpper(view.ReviewDecision) {
	case "APPROVED":
		state.ReviewDecision = ReviewApproved
	case "CHANGES_REQUESTED":
		state.ReviewDecision = ReviewChangesRequested
	case "REVIEW_REQUIRED":
		state.ReviewDecision = ReviewRequired
	}
	state.Checks = rollupChecks(len(view.StatusCheckRollup), func(index int) (string, string) {
		item := view.StatusCheckRollup[index]
		return item.Status, item.Conclusion
	})
	state.UpdatedAt = parseForgeTime(view.UpdatedAt)
	return state, nil
}
