package prstate

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// OriginResolver reads PR state through the origin CLI (the Cursor forge),
// which accepts the pull URL verbatim.
// Verified against origin 2026.08: 'origin pr view URL --json
// status,mergedAt,updatedAt' and 'origin pr checks URL --json
// status,conclusion'. The view fields carry no review decision, so it stays
// unknown for origin PRs.
type OriginResolver struct {
	lookPath func(string) (string, error)
	run      runCommand
}

func NewOriginResolver() *OriginResolver {
	return &OriginResolver{lookPath: exec.LookPath, run: realRun}
}

func (r *OriginResolver) Host() string { return "origin.cursor.com" }

type originView struct {
	Status    string `json:"status"`
	MergedAt  string `json:"mergedAt"`
	UpdatedAt string `json:"updatedAt"`
}

type originCheck struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

func (r *OriginResolver) Fetch(ctx context.Context, prURL string) (PRState, error) {
	if _, err := r.lookPath("origin"); err != nil {
		return PRState{}, fmt.Errorf("install the origin CLI to see origin PR state")
	}
	output, err := r.run(ctx, "origin", "pr", "view", prURL, "--json", "status,mergedAt,updatedAt")
	if err != nil {
		return PRState{}, err
	}
	var view originView
	if err := json.Unmarshal(output, &view); err != nil {
		return PRState{}, fmt.Errorf("parse origin pr view output: %w", err)
	}
	state := PRState{ReviewDecision: ReviewUnknown, Checks: ChecksUnknown}
	switch strings.ToLower(view.Status) {
	case "merged":
		state.State = StateMerged
	case "closed":
		state.State = StateClosed
		if view.MergedAt != "" {
			state.State = StateMerged
		}
	case "open":
		state.State = StateOpen
	case "draft":
		state.State = StateDraft
	default:
		state.State = StateUnknown
	}
	state.UpdatedAt = parseForgeTime(view.UpdatedAt)
	// Checks are a second, best-effort call: a failure leaves them unknown.
	if checksOutput, err := r.run(ctx, "origin", "pr", "checks", prURL, "--json", "status,conclusion"); err == nil {
		var checks []originCheck
		if err := json.Unmarshal(checksOutput, &checks); err == nil {
			state.Checks = rollupChecks(len(checks), func(index int) (string, string) {
				return checks[index].Status, checks[index].Conclusion
			})
		}
	}
	return state, nil
}

// rollupChecks folds per-check status/conclusion pairs into one value: any
// failure wins, then any pending, then pass; no checks is none.
func rollupChecks(count int, at func(int) (string, string)) Checks {
	if count == 0 {
		return ChecksNone
	}
	pending := false
	for index := 0; index < count; index++ {
		status, conclusion := at(index)
		switch strings.ToUpper(conclusion) {
		case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			return ChecksFail
		}
		switch strings.ToUpper(status) {
		case "COMPLETED", "":
		default:
			pending = true
		}
	}
	if pending {
		return ChecksPending
	}
	return ChecksPass
}

func parseForgeTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
