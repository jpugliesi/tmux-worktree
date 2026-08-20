package agent

import (
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestRegisterReloadsProjectInsideMutationLock(t *testing.T) {
	service := NewService(t.TempDir(), "")
	project := domain.Project{
		Version: domain.ProjectVersion,
		ID:      "project-that-was-removed",
		Name:    "removed",
		Status:  domain.ProjectActive,
	}

	_, err := service.Register(project, "command", "test", "", "", []string{"true"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Register after Project removal error = %v", err)
	}
	agents, listErr := service.List(project.ID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(agents) != 0 {
		t.Fatalf("Register saved an Agent Session for a removed Project: %+v", agents)
	}
}

func TestValidateRegistrationRejectsUnsupportedTranscriptLink(t *testing.T) {
	service := NewService(t.TempDir(), "")
	project := domain.Project{ID: "project-one", Name: "project-one", Status: domain.ProjectActive}
	err := service.ValidateRegistration(project, "cursor", "", "cursor-session", []string{"cursor-agent", "resume"})
	if err == nil || !strings.Contains(err.Error(), "does not support verifiable linked transcripts") {
		t.Fatalf("Cursor transcript registration error = %v", err)
	}
	if err := service.ValidateRegistration(project, "cursor", "", "", []string{"cursor-agent", "resume"}); err != nil {
		t.Fatalf("Cursor registration without a transcript link: %v", err)
	}
}
