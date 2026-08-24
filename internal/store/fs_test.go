package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileAtomicWritesDataWithTheGivenPermissions(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "value.json")
	if err := WriteFileAtomic(path, []byte("first\n"), 0o600, "test value"); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("second\n"), 0o600, "test value"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "second\n" {
		t.Fatalf("file content = %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file permissions = %v", info.Mode().Perm())
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory keeps temporary files: %v", entries)
	}
}

func TestWriteFileAtomicReportsTheLabelOnFailure(t *testing.T) {
	t.Parallel()

	err := WriteFileAtomic(filepath.Join(t.TempDir(), "missing", "value.json"), []byte("data"), 0o600, "test value")
	if err == nil || !strings.Contains(err.Error(), "test value") {
		t.Fatalf("error = %v, want the label", err)
	}
}

func TestWriteFileExclusiveAtomicNeverReplacesAnExistingFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "value.md")
	if err := os.WriteFile(path, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := WriteFileExclusiveAtomic(path, []byte("replace\n"), 0o644, "Ticket")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive write error = %v, want os.ErrExist", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "keep\n" {
		t.Fatalf("exclusive write replaced content with %q", content)
	}

	entries, readDirErr := os.ReadDir(directory)
	if readDirErr != nil {
		t.Fatal(readDirErr)
	}
	if len(entries) != 1 || entries[0].Name() != "value.md" {
		t.Fatalf("exclusive write left temporary files: %v", entries)
	}
}

func TestWriteFileExclusiveAtomicWritesANewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "value.md")
	if err := WriteFileExclusiveAtomic(path, []byte("new\n"), 0o640, "Ticket"); err != nil {
		t.Fatalf("exclusive write: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new\n" {
		t.Fatalf("content = %q", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}

func TestDirectoryBytesSumsRegularFilesAndTreatsAMissingRootAsZero(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "one"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "nested", "two"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	total, err := DirectoryBytes(directory)
	if err != nil {
		t.Fatal(err)
	}
	if total != 8 {
		t.Fatalf("DirectoryBytes() = %d, want 8", total)
	}
	missing, err := DirectoryBytes(filepath.Join(directory, "does-not-exist"))
	if err != nil || missing != 0 {
		t.Fatalf("DirectoryBytes(missing) = %d, %v", missing, err)
	}
}
