package claude

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/kokistudios/card/internal/signal"
)

// ErrInterrupted is returned when Claude is terminated by an interrupt signal (Ctrl+C).
var ErrInterrupted = errors.New("claude interrupted")

// ErrPhaseComplete is returned when Claude exits due to a phase complete signal.
var ErrPhaseComplete = errors.New("phase complete signal received")

const (
	// signalPollInterval is how often we check for phase complete signals.
	signalPollInterval = 500 * time.Millisecond

	// signalGracePeriod is how long we wait for Claude to exit cleanly after SIGTERM.
	signalGracePeriod = 2 * time.Second
)

// InvokeMode controls how Claude Code is launched.
type InvokeMode int

const (
	// ModeInteractive opens Claude in interactive mode with the initial message
	// appended to the system prompt. The developer must type the first message
	// to start, but gets full dialogue throughout.
	ModeInteractive InvokeMode = iota

	// ModeNonInteractive runs Claude via -p (print mode) to completion.
	// No developer interaction. Use for phases that don't need dialogue.
	ModeNonInteractive
)

// InvokeOptions configures a Claude Code CLI invocation.
type InvokeOptions struct {
	SystemPrompt   string
	InitialMessage string
	WorkingDir     string     // repo path for Claude Code to operate in
	AllowedTools   []string   // constrain Claude Code's tool access per phase
	OutputDir      string     // where artifacts should be written
	ClaudePath     string     // path to claude binary (defaults to "claude")
	Mode           InvokeMode // interactive or non-interactive
	OnStart        func()     // called just before the Claude process starts (e.g., to stop a spinner)
}

// Available checks if the claude CLI is on PATH.
func Available() error {
	return AvailableAt("claude")
}

// AvailableAt checks if the claude CLI exists at the given path.
func AvailableAt(path string) error {
	_, err := exec.LookPath(path)
	if err != nil {
		return fmt.Errorf("claude CLI not found at %q — install Claude Code (https://claude.ai/code) to use CARD sessions", path)
	}
	return nil
}

// Invoke launches Claude Code with the given options.
func Invoke(opts InvokeOptions) error {
	claudePath := opts.ClaudePath
	if claudePath == "" {
		claudePath = "claude"
	}

	if err := AvailableAt(claudePath); err != nil {
		return err
	}

	env := os.Environ()
	if opts.OutputDir != "" {
		env = append(env, "CARD_OUTPUT_DIR="+opts.OutputDir)
	}

	if opts.Mode == ModeNonInteractive {
		return invokeNonInteractive(claudePath, env, opts)
	}
	return invokeInteractive(claudePath, env, opts)
}

// invokeInteractive opens Claude in interactive mode.
// The system prompt and initial message are both set as system prompt context,
// so the developer has full dialogue capability from the start.
//
// When OutputDir is set, this function polls for a phase complete signal file.
// If Claude calls card_phase_complete with status "complete", this function
// sends SIGTERM to gracefully terminate Claude and returns ErrPhaseComplete.
func invokeInteractive(claudePath string, env []string, opts InvokeOptions) error {
	args := []string{}

	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	}

	if opts.InitialMessage != "" {
		args = append(args, "--append-system-prompt", opts.InitialMessage)
	}

	if opts.AllowedTools != nil {
		for _, tool := range opts.AllowedTools {
			args = append(args, "--allowedTools", tool)
		}
	}

	cmd := exec.Command(claudePath, args...)
	if opts.WorkingDir != "" {
		cmd.Dir = opts.WorkingDir
	}
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		if opts.OnStart != nil {
			opts.OnStart()
		}
		return fmt.Errorf("failed to start claude: %w", err)
	}
	if opts.OnStart != nil {
		opts.OnStart()
	}

	// Create channel for process completion
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cmd.Wait()
	}()

	// Only poll for signals if OutputDir is set (i.e., we're in a CARD session phase)
	if opts.OutputDir != "" {
		ticker := time.NewTicker(signalPollInterval)
		defer ticker.Stop()

		for {
			select {
			case err := <-doneCh:
				// Process finished naturally (Ctrl+C, completion, or crash)
				return handleProcessExit(err)

			case <-ticker.C:
				// Check for phase complete signal
				sig, sigErr := signal.CheckPhaseComplete(opts.OutputDir)
				if sigErr != nil {
					// Signal file read error - continue polling
					continue
				}
				if sig != nil && sig.Status == "complete" {
					// Signal received - gracefully terminate Claude
					_ = cmd.Process.Signal(syscall.SIGTERM)

					// Wait for clean exit with timeout
					select {
					case <-doneCh:
						// Exited cleanly after SIGTERM
					case <-time.After(signalGracePeriod):
						// Force kill if still running
						_ = cmd.Process.Kill()
						<-doneCh
					}

					return ErrPhaseComplete
				}
				// sig.Status is "blocked" or "needs_input" - continue running
			}
		}
	}

	// No OutputDir - wait for natural completion (legacy behavior)
	err := <-doneCh
	return handleProcessExit(err)
}

// handleProcessExit converts exec errors to appropriate return values.
func handleProcessExit(err error) error {
	if err == nil {
		return nil
	}
	if isInterrupt(err) {
		return ErrInterrupted
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("claude exited with code %d", exitErr.ExitCode())
	}
	return fmt.Errorf("failed to run claude: %w", err)
}

// invokeNonInteractive runs Claude via -p (print mode) to completion.
// Output is suppressed to allow a spinner to show progress. No developer interaction.
// OnStart is NOT called during non-interactive mode - the caller should stop the
// spinner after Invoke returns.
func invokeNonInteractive(claudePath string, env []string, opts InvokeOptions) error {
	args := []string{"-p", opts.InitialMessage}

	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	}

	if opts.AllowedTools != nil {
		for _, tool := range opts.AllowedTools {
			args = append(args, "--allowedTools", tool)
		}
	}

	cmd := exec.Command(claudePath, args...)
	if opts.WorkingDir != "" {
		cmd.Dir = opts.WorkingDir
	}
	cmd.Env = env
	// Discard output - spinner provides visual feedback, artifact is rendered after completion
	// Must connect streams (not leave nil) or Claude Code may not function correctly
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude: %w", err)
	}
	// Don't call OnStart - let spinner run during entire execution
	if err := cmd.Wait(); err != nil {
		if isInterrupt(err) {
			return ErrInterrupted
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("claude exited with code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("failed to run claude: %w", err)
	}

	return nil
}

// isInterrupt checks if an exec error was caused by an interrupt signal.
func isInterrupt(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.Signal() == syscall.SIGINT || status.Signal() == syscall.SIGTERM
		}
	}
	return false
}
