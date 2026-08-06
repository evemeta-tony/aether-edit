// services/orchestrator/internal/engine/ffmpegadapter/exec.go
//
// Process execution seam. The real implementation shells out via os/exec
// argv arrays only (S3: never a shell). Tests inject a fake CommandRunner at
// this seam; the real execer ships and is the default.
package ffmpegadapter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// Process is a started child process whose stdout is streamed by the caller.
type Process interface {
	// Stdout is the process standard output (the ffmpeg progress pipe).
	Stdout() io.Reader
	// Wait blocks until exit and returns the captured stderr tail plus the
	// exit error, if any.
	Wait() (stderr []byte, err error)
	// Kill terminates the process.
	Kill() error
}

// CommandRunner executes external binaries.
type CommandRunner interface {
	// Output runs argv to completion and returns stdout. Used for the
	// buildconf gate and for ffprobe.
	Output(ctx context.Context, name string, args []string) (stdout []byte, stderr []byte, err error)
	// Start launches argv and returns a handle whose stdout is streamed.
	// Used for ffmpeg encodes with -progress pipe:1.
	Start(ctx context.Context, name string, args []string) (Process, error)
}

// OSRunner is the production CommandRunner backed by os/exec.
type OSRunner struct{}

// Output implements CommandRunner.
func (OSRunner) Output(ctx context.Context, name string, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Start implements CommandRunner.
func (OSRunner) Start(ctx context.Context, name string, args []string) (Process, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &osProcess{cmd: cmd, stdout: stdout, stderr: &stderr}, nil
}

type osProcess struct {
	cmd    *exec.Cmd
	stdout io.Reader
	stderr *bytes.Buffer
}

func (p *osProcess) Stdout() io.Reader { return p.stdout }

func (p *osProcess) Wait() ([]byte, error) {
	err := p.cmd.Wait()
	return p.stderr.Bytes(), err
}

func (p *osProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
