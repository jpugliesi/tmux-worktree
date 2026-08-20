package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var ErrLockHeld = errors.New("lock is held")

type NamedLock struct {
	file *os.File
}

type MutationLock struct {
	file *os.File
}

func AcquireMutationLock(stateDir string) (*MutationLock, error) {
	return acquireMutationLock(stateDir, false)
}

func AcquireMutationLockBlocking(stateDir string) (*MutationLock, error) {
	return acquireMutationLock(stateDir, true)
}

func acquireMutationLock(stateDir string, blocking bool) (*MutationLock, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(stateDir, "mutation.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open mutation lock: %w", err)
	}
	operation := syscall.LOCK_EX
	if !blocking {
		operation |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), operation); err != nil {
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

// AcquireNamedLock gets an exclusive lock without waiting. The lock path uses
// digests, so namespace and name values cannot select a path outside stateDir.
func AcquireNamedLock(stateDir, namespace, name string) (*NamedLock, error) {
	return acquireNamedLock(stateDir, namespace, name, false)
}

// AcquireNamedLockBlocking waits until it can get an exclusive lock.
func AcquireNamedLockBlocking(stateDir, namespace, name string) (*NamedLock, error) {
	return acquireNamedLock(stateDir, namespace, name, true)
}

func AcquireEnvironmentLock(stateDir, environmentID string) (*NamedLock, error) {
	return AcquireNamedLock(stateDir, "environment", environmentID)
}

func AcquireCacheLock(stateDir, cacheKey string) (*NamedLock, error) {
	return AcquireNamedLock(stateDir, "cache", cacheKey)
}

func AcquireActivityLock(stateDir, activityID string) (*NamedLock, error) {
	return AcquireNamedLock(stateDir, "activity", activityID)
}

// ActivityLockHeld reports whether an activity lock is held at this instant.
func ActivityLockHeld(stateDir, activityID string) (bool, error) {
	lock, err := AcquireActivityLock(stateDir, activityID)
	if errors.Is(err, ErrLockHeld) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if err := lock.Release(); err != nil {
		return false, err
	}
	return false, nil
}

// File returns the locked file. A caller can pass it to a child process with
// exec.Cmd.ExtraFiles to keep the activity lock held by that process.
func (l *NamedLock) File() *os.File {
	if l == nil {
		return nil
	}
	return l.file
}

// Detach closes this process's file without unlocking it. Call Detach after a
// child starts with File in exec.Cmd.ExtraFiles. The child then owns the lock.
func (l *NamedLock) Detach() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	if err := file.Close(); err != nil {
		return fmt.Errorf("detach named lock: %w", err)
	}
	return nil
}

func (l *NamedLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("release named lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close named lock: %w", closeErr)
	}
	return nil
}

func acquireNamedLock(stateDir, namespace, name string, blocking bool) (*NamedLock, error) {
	directory := filepath.Join(stateDir, "locks", lockDigest(namespace))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("create named lock directory: %w", err)
	}
	path := filepath.Join(directory, lockDigest(name)+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open named lock: %w", err)
	}
	operation := syscall.LOCK_EX
	if !blocking {
		operation |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), operation); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: %s %q", ErrLockHeld, namespace, name)
		}
		return nil, fmt.Errorf("acquire named lock: %w", err)
	}
	return &NamedLock{file: file}, nil
}

func lockDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
