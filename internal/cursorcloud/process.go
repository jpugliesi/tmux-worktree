package cursorcloud

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

const (
	maximumHarnessOutput = 1024 * 1024
	maximumHarnessLogs   = 64 * 1024
)

// ProcessRunner calls the installed, Bun-compiled Cursor SDK harness. It does
// not use a shell or download a runtime.
type ProcessRunner struct {
	Executable string
}

func (r ProcessRunner) Run(ctx context.Context, request []byte) ([]byte, []byte, error) {
	if r.Executable == "" {
		return nil, nil, fmt.Errorf("the Cursor Cloud harness executable is not set")
	}
	stdout := newLimitedBuffer(maximumHarnessOutput)
	stderr := newLimitedBuffer(maximumHarnessLogs)
	command := exec.CommandContext(ctx, r.Executable)
	command.Stdin = bytes.NewReader(request)
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if stdout.exceeded {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("Cursor Cloud harness output is larger than %d bytes", maximumHarnessOutput)
	}
	if stderr.exceeded {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("Cursor Cloud harness logs are larger than %d bytes", maximumHarnessLogs)
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.exceeded = true
		return len(data), nil
	}
	if len(data) > remaining {
		b.exceeded = true
		_, _ = b.buffer.Write(data[:remaining])
		return len(data), nil
	}
	return b.buffer.Write(data)
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}
