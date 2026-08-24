package workspace

import (
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// runSteps runs each setup step that has not succeeded. It owns the shared
// step choreography: report progress, mark the step running, save, run the
// step, stamp the result, and save again.
//
// The save function persists the record that owns the steps with the given
// update time. The run function receives the step as the loop found it,
// before the engine marks it running. The fail function persists a failure
// and returns the error that the caller must see.
func (s *Service) runSteps(
	steps []domain.SetupStep,
	save func(now time.Time) error,
	run func(step domain.SetupStep) error,
	fail func(now time.Time, cause error) error,
) error {
	for index := range steps {
		step := &steps[index]
		if step.Status == domain.StepSucceeded {
			continue
		}
		s.report("Step %d of %d: %s", index+1, len(steps), step.ID)
		found := *step
		now := s.now()
		step.Status = domain.StepRunning
		step.Attempts++
		step.StartedAt = &now
		step.FinishedAt = nil
		step.Error = ""
		if err := save(now); err != nil {
			return err
		}
		runErr := run(found)
		finished := s.now()
		step.FinishedAt = &finished
		if runErr != nil {
			step.Status = domain.StepFailed
			step.Error = runErr.Error()
			return fail(finished, runErr)
		}
		step.Status = domain.StepSucceeded
		if err := save(finished); err != nil {
			return err
		}
	}
	return nil
}
