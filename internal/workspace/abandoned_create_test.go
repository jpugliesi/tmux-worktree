package workspace

import (
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// reserveAbandonedClaim records a claim reservation that no process
// completes, the way a killed create leaves it.
func reserveAbandonedClaim(t *testing.T, setup driftTestSetup, reservedAt time.Time) domain.Workspace {
	t.Helper()
	environment := setup.environment
	workspace := setup.service.workspaceForEnvironment("stuck", setup.template.Name, setup.template, environment, "0123456789abcdef0123456789abcdef", "twt/stuck", CreateOptions{})
	environment.Status = domain.EnvironmentClaiming
	environment.Generation++
	environment.Assignment = &domain.EnvironmentAssignment{
		Generation: environment.Generation, Kind: domain.EnvironmentAssignmentClaim,
		Phase: domain.EnvironmentAssignmentReserved, Workspace: workspace, ReservedAt: reservedAt,
	}
	if err := setup.service.environments.Save(environment); err != nil {
		t.Fatal(err)
	}
	if err := setup.service.store.Save(workspace); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestSettleAbandonedCreateRemovesAClaimThatNoProcessCompletes(t *testing.T) {
	setup := newDriftTestSetup(t, nil)
	workspace := reserveAbandonedClaim(t, setup, time.Now().UTC().Add(-time.Hour))

	_, removed, err := setup.service.SettleAbandonedCreate("stuck")
	if err != nil {
		t.Fatalf("SettleAbandonedCreate(): %v", err)
	}
	if !removed {
		t.Fatal("SettleAbandonedCreate() kept the Workspace of a claim that never completed")
	}
	if _, err := setup.service.store.Find(workspace.ID); err == nil {
		t.Fatalf("the Workspace record %s remains after the settle", workspace.ID)
	}
	environment, err := setup.service.environments.Find(setup.environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if environment.Status != domain.EnvironmentFailed || environment.Assignment != nil {
		t.Fatalf("Prepared Environment after the settle = %q with assignment %+v, want failed without assignment", environment.Status, environment.Assignment)
	}
}

func TestSettleAbandonedCreateRefusesACreateThatStillRuns(t *testing.T) {
	setup := newDriftTestSetup(t, nil)
	workspace := reserveAbandonedClaim(t, setup, time.Now().UTC().Add(-time.Hour))
	// A live claim holds the lock of its Prepared Environment.
	lock, err := store.AcquireEnvironmentLock(setup.service.options.StateDir, setup.environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	_, removed, err := setup.service.SettleAbandonedCreate("stuck")
	if clierr.CodeOf(err) != clierr.Locked || removed {
		t.Fatalf("SettleAbandonedCreate() during a live claim = removed %v, error %v; want a Locked refusal", removed, err)
	}
	found, err := setup.service.store.Find(workspace.ID)
	if err != nil || found.Status != domain.WorkspaceInitializing {
		t.Fatalf("Workspace after the refusal = %+v, %v; want it unchanged", found, err)
	}
}

func TestSettleAbandonedCreateWaitsForANewReservation(t *testing.T) {
	setup := newDriftTestSetup(t, nil)
	// The create saves the reservation and only then takes the environment
	// lock, so a new reservation without the lock can still be live.
	reserveAbandonedClaim(t, setup, time.Now().UTC())

	_, removed, err := setup.service.SettleAbandonedCreate("stuck")
	if clierr.CodeOf(err) != clierr.Locked || removed {
		t.Fatalf("SettleAbandonedCreate() on a new reservation = removed %v, error %v; want a Locked refusal", removed, err)
	}
}

func TestDoneFinishesAWorkspaceWhoseSetupStopped(t *testing.T) {
	setup := newDriftTestSetup(t, nil)
	created, err := setup.service.CreateWithOptions("stuck", setup.template.Name, setup.template, CreateOptions{})
	if err != nil {
		t.Fatalf("CreateWithOptions(): %v", err)
	}
	// The claim completed, and then the create process stopped during a
	// setup step.
	created.Status = domain.WorkspaceInitializing
	created.Steps[len(created.Steps)-1].Status = domain.StepRunning
	if err := setup.service.store.Save(created); err != nil {
		t.Fatal(err)
	}

	settled, removed, err := setup.service.SettleAbandonedCreate("stuck")
	if err != nil || removed {
		t.Fatalf("SettleAbandonedCreate() = removed %v, error %v; want a settled Workspace", removed, err)
	}
	if settled.Status != domain.WorkspaceSetupFailed {
		t.Fatalf("settled Workspace status = %q, want %q", settled.Status, domain.WorkspaceSetupFailed)
	}
	result, err := setup.service.Release("stuck", "", ReleaseOptions{Force: true})
	if err != nil {
		t.Fatalf("Release() after the settle: %v", err)
	}
	if result.Workspace.Status != domain.WorkspaceArchived {
		t.Fatalf("released Workspace status = %q, want %q", result.Workspace.Status, domain.WorkspaceArchived)
	}
}
