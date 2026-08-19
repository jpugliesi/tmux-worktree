package store

import (
	"strings"
	"testing"
)

func TestMutationLockRefusesConcurrentWriter(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireMutationLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireMutationLock(stateDir); err == nil || !strings.Contains(err.Error(), "another twt2 change") {
		t.Fatalf("second lock error = %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireMutationLock(stateDir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}
