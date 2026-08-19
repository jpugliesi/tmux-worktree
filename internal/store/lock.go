package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type MutationLock struct {
	file *os.File
}

func AcquireMutationLock(stateDir string) (*MutationLock, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(stateDir, "mutation.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open mutation lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another twt2 change is in progress")
		}
		return nil, fmt.Errorf("acquire mutation lock: %w", err)
	}
	return &MutationLock{file: file}, nil
}

func (l *MutationLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("release mutation lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close mutation lock: %w", closeErr)
	}
	return nil
}
