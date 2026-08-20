package store

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMutationLockRefusesConcurrentWriter(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireMutationLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireMutationLock(stateDir); err == nil || !strings.Contains(err.Error(), "another twt change") {
		t.Fatalf("second lock error = %v", err)
	}
	if _, err := AcquireMutationLock(stateDir); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("second lock error = %v, want ErrLockHeld", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
	second, err := AcquireMutationLock(stateDir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestBlockingMutationLockWaitsForRelease(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireMutationLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	var second *MutationLock
	go func() {
		var err error
		second, err = AcquireMutationLockBlocking(stateDir)
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("blocking mutation lock returned before release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocking mutation lock did not return after release")
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestMutationLockContentionNamesTheHolder(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireMutationLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	_, err = AcquireMutationLock(stateDir)
	if err == nil {
		t.Fatal("second lock did not fail")
	}
	want := fmt.Sprintf("process %d: %s", os.Getpid(), strings.Join(os.Args, " "))
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("second lock error = %v, want text %q", err, want)
	}
}

func TestNamedLockContentionNamesTheHolder(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireNamedLock(stateDir, "environment", "held")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	_, err = AcquireNamedLock(stateDir, "environment", "held")
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("second lock error = %v", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("process %d", os.Getpid())) {
		t.Fatalf("second lock error = %v, want the holder process", err)
	}
}

func TestBoundedMutationLockWaitsForRelease(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireMutationLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	var second *MutationLock
	go func() {
		var err error
		second, err = AcquireMutationLockWithTimeout(stateDir, 2*time.Second)
		result <- err
	}()
	time.Sleep(250 * time.Millisecond)
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bounded mutation lock did not return after release")
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestBoundedMutationLockStopsAtItsDeadline(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireMutationLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	started := time.Now()
	_, err = AcquireMutationLockWithTimeout(stateDir, 300*time.Millisecond)
	if err == nil {
		t.Fatal("bounded mutation lock did not fail")
	}
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("timeout error = %v, want ErrLockHeld", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("bounded mutation lock waited %s", elapsed)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("process %d", os.Getpid())) {
		t.Fatalf("timeout error = %v, want the holder process", err)
	}
}

func TestNamedLockRejectsConcurrentUseAndKeepsNamesInsideTheStateDirectory(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireNamedLock(stateDir, "environment", "../../outside")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if !strings.HasPrefix(first.File().Name(), filepath.Join(stateDir, "locks")+string(filepath.Separator)) {
		t.Fatalf("lock path = %q", first.File().Name())
	}
	if filepath.Base(first.File().Name()) == "outside.lock" {
		t.Fatalf("untrusted name is visible in lock path: %q", first.File().Name())
	}
	if _, err := AcquireNamedLock(stateDir, "environment", "../../outside"); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("second lock error = %v", err)
	}
}

func TestNamedBlockingLockWaitsForRelease(t *testing.T) {
	stateDir := t.TempDir()
	first, err := AcquireNamedLock(stateDir, "cache", "repository")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	var second *NamedLock
	go func() {
		var err error
		second, err = AcquireNamedLockBlocking(stateDir, "cache", "repository")
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("blocking lock returned before release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocking lock did not return after release")
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestActivityLockExposesAnInheritableFileAndDetectsHeldState(t *testing.T) {
	stateDir := t.TempDir()
	activity, err := AcquireActivityLock(stateDir, "prepare-example")
	if err != nil {
		t.Fatal(err)
	}
	if activity.File() == nil {
		t.Fatal("activity lock has no file")
	}
	held, err := ActivityLockHeld(stateDir, "prepare-example")
	if err != nil || !held {
		t.Fatalf("ActivityLockHeld() = %v, %v", held, err)
	}
	if err := activity.Release(); err != nil {
		t.Fatal(err)
	}
	held, err = ActivityLockHeld(stateDir, "prepare-example")
	if err != nil || held {
		t.Fatalf("ActivityLockHeld() after release = %v, %v", held, err)
	}
}

func TestActivityLockCanPassToAChildProcess(t *testing.T) {
	if os.Getenv("TWT_ACTIVITY_LOCK_CHILD") == "1" {
		inherited := os.NewFile(3, "inherited-activity-lock")
		if inherited == nil {
			os.Exit(2)
		}
		defer inherited.Close()
		fmt.Println("ready")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}

	stateDir := t.TempDir()
	activity, err := AcquireActivityLock(stateDir, "child-activity")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestActivityLockCanPassToAChildProcess$")
	command.Env = append(os.Environ(), "TWT_ACTIVITY_LOCK_CHILD=1")
	command.ExtraFiles = []*os.File{activity.File()}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child ready output = %q, %v", line, err)
	}
	if err := activity.Detach(); err != nil {
		t.Fatal(err)
	}
	held, err := ActivityLockHeld(stateDir, "child-activity")
	if err != nil || !held {
		t.Fatalf("ActivityLockHeld() while child runs = %v, %v", held, err)
	}
	if _, err := stdin.Write([]byte("stop\n")); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	held, err = ActivityLockHeld(stateDir, "child-activity")
	if err != nil || held {
		t.Fatalf("ActivityLockHeld() after child exits = %v, %v", held, err)
	}
}

func TestNamedLocksSerializeConcurrentWriters(t *testing.T) {
	stateDir := t.TempDir()
	var active int
	var maximum int
	var mutex sync.Mutex
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			lock, err := AcquireNamedLockBlocking(stateDir, "environment", "shared")
			if err != nil {
				t.Errorf("AcquireNamedLockBlocking() error = %v", err)
				return
			}
			mutex.Lock()
			active++
			if active > maximum {
				maximum = active
			}
			mutex.Unlock()
			time.Sleep(time.Millisecond)
			mutex.Lock()
			active--
			mutex.Unlock()
			if err := lock.Release(); err != nil {
				t.Errorf("Release() error = %v", err)
			}
		}()
	}
	group.Wait()
	if maximum != 1 {
		t.Fatalf("maximum concurrent writers = %d", maximum)
	}

	entries, err := os.ReadDir(filepath.Join(stateDir, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("named lock directory is empty")
	}
}
