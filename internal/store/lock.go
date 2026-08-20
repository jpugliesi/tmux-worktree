package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

var ErrLockHeld = errors.New("lock is held")

// lockRetryInterval is the wait between attempts of a bounded blocking acquire.
const lockRetryInterval = 100 * time.Millisecond

// lockHolder describes the process that holds a lock file. twt2 writes this
// value into the lock file for better messages. Correctness comes from flock.
type lockHolder struct {
	PID        int    `json:"pid"`
	Command    string `json:"command"`
	AcquiredAt string `json:"acquiredAt"`
}

// writeLockHolder records this process in the lock file. A failure changes no
// lock behavior, so this function does not report one.
func writeLockHolder(file *os.File) {
	holder := lockHolder{
		PID:        os.Getpid(),
		Command:    strings.Join(os.Args, " "),
		AcquiredAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(holder)
	if err != nil {
		return
	}
	if err := file.Truncate(0); err != nil {
		return
	}
	_, _ = file.WriteAt(data, 0)
}

// lockHolderDescription reads the holder of the lock file at path. It returns an
// empty string when the file has no usable holder record.
func lockHolderDescription(path string) string {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	var holder lockHolder
	if err := json.Unmarshal(data, &holder); err != nil || holder.PID == 0 {
		return ""
	}
	command := holder.Command
	if strings.TrimSpace(command) == "" {
		command = "unknown command"
	}
	return fmt.Sprintf("process %d: %s", holder.PID, command)
}

type NamedLock struct {
	file *os.File
}

type MutationLock struct {
	file *os.File
}

func AcquireMutationLock(stateDir string) (*MutationLock, error) {
	return acquireMutationLock(stateDir, false)
}

// AcquireMutationLockBlocking waits for the mutation lock. Without a timeout it
// waits without a limit. With a timeout it retries until the deadline and then
// reports the process that holds the lock.
func AcquireMutationLockBlocking(stateDir string, timeout ...time.Duration) (*MutationLock, error) {
	if len(timeout) == 0 {
		return acquireMutationLock(stateDir, true)
	}
	return acquireMutationLockWithin(stateDir, timeout[0])
}

func acquireMutationLock(stateDir string, blocking bool) (*MutationLock, error) {
	file, err := openMutationLock(stateDir)
	if err != nil {
		return nil, err
	}
	operation := syscall.LOCK_EX
	if !blocking {
		operation |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), operation); err != nil {
		name := file.Name()
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, mutationLockHeldError(name)
		}
		return nil, fmt.Errorf("acquire mutation lock: %w", err)
	}
	writeLockHolder(file)
	return &MutationLock{file: file}, nil
}

// acquireMutationLockWithin keeps one open file and retries until the deadline.
func acquireMutationLockWithin(stateDir string, timeout time.Duration) (*MutationLock, error) {
	file, err := openMutationLock(stateDir)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		flockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if flockErr == nil {
			writeLockHolder(file)
			return &MutationLock{file: file}, nil
		}
		if !errors.Is(flockErr, syscall.EWOULDBLOCK) && !errors.Is(flockErr, syscall.EAGAIN) {
			file.Close()
			return nil, fmt.Errorf("acquire mutation lock: %w", flockErr)
		}
		if !time.Now().Add(lockRetryInterval).Before(deadline) {
			name := file.Name()
			file.Close()
			return nil, mutationLockHeldError(name)
		}
		time.Sleep(lockRetryInterval)
	}
}

func openMutationLock(stateDir string) (*os.File, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(stateDir, "mutation.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open mutation lock: %w", err)
	}
	return file, nil
}

func mutationLockHeldError(path string) error {
	if holder := lockHolderDescription(path); holder != "" {
		return clierr.New(clierr.Locked, "another twt2 change is in progress (%s)", holder)
	}
	return clierr.New(clierr.Locked, "another twt2 change is in progress")
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
			if holder := lockHolderDescription(path); holder != "" {
				return nil, clierr.Wrap(clierr.Locked, fmt.Errorf("%w: %s %q (%s)", ErrLockHeld, namespace, name, holder))
			}
			return nil, clierr.Wrap(clierr.Locked, fmt.Errorf("%w: %s %q", ErrLockHeld, namespace, name))
		}
		return nil, fmt.Errorf("acquire named lock: %w", err)
	}
	writeLockHolder(file)
	return &NamedLock{file: file}, nil
}

func lockDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
