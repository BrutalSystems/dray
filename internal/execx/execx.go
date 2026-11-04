// Package execx runs external programs (docker, aws, kubectl, git, gh) with
// no shell in between, so an argument is never parsed as shell syntax.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Cmd is one program to run.
type Cmd struct {
	Name string
	Args []string
	// Dir is the working folder; empty is the current one.
	Dir string
	// Stdin is fed to the program; values such as passwords and secrets travel
	// here or in Env, never in Args, where other users of the machine see them.
	Stdin io.Reader
	// Env is added to the current environment.
	Env []string
	// Stream, when set, receives the program's stdout and stderr as it runs,
	// for long steps such as a build. Stdout is then not captured.
	Stream io.Writer
}

// String is the command line, for messages. It never includes Env or Stdin.
func (c Cmd) String() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// Runner runs a command and returns its stdout.
type Runner func(ctx context.Context, c Cmd) ([]byte, error)

// Error is a program that exited with a non-zero code.
type Error struct {
	Cmd    string
	Code   int
	Stderr string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("%s: exit %d", e.Cmd, e.Code)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += "\n" + tail(s, 2000)
	}
	return msg
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// ExitCode is the exit code of a program that ran and failed, or -1.
func ExitCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}

// Run runs c for real.
func Run(ctx context.Context, c Cmd) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...) //nolint:gosec // callers pass fixed programs; no shell
	cmd.Dir = c.Dir
	cmd.Stdin = c.Stdin
	if len(c.Env) > 0 {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	var stdout, stderr bytes.Buffer
	if c.Stream != nil {
		cmd.Stdout = c.Stream
		cmd.Stderr = io.MultiWriter(c.Stream, &stderr)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.Bytes(), &Error{Cmd: c.String(), Code: exit.ExitCode(), Stderr: stderr.String()}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return nil, fmt.Errorf("%s is not installed (not found on PATH)", c.Name)
	}
	return nil, fmt.Errorf("%s: %w", c.String(), err)
}
