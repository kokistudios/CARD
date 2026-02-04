package runtime

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	ossignal "os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kokistudios/card/internal/signal"
)

const (
	codexSignalPollInterval = 500 * time.Millisecond
	codexSignalGracePeriod  = 2 * time.Second
)

type CodexRuntime struct {
	Path string
}

func (c *CodexRuntime) Name() string {
	return "codex"
}

func (c *CodexRuntime) Available() error {
	return codexAvailableAt(c.pathOrDefault())
}

func (c *CodexRuntime) Invoke(opts InvokeOptions) error {
	codexPath := c.pathOrDefault()
	if err := codexAvailableAt(codexPath); err != nil {
		return err
	}

	env := os.Environ()
	if opts.OutputDir != "" {
		env = append(env, "CARD_OUTPUT_DIR="+opts.OutputDir)
	}

	if opts.Mode == ModeNonInteractive {
		return c.invokeNonInteractive(codexPath, env, opts)
	}
	return c.invokeInteractive(codexPath, env, opts)
}

func (c *CodexRuntime) ConfigureMCP(cardBinaryPath, serverName string) error {
	codexPath := c.pathOrDefault()
	cmd := exec.Command(codexPath, "mcp", "add", serverName, "--", cardBinaryPath, "mcp-serve")
	if _, err := cmd.CombinedOutput(); err != nil {
		return nil
	}
	return nil
}

func (c *CodexRuntime) pathOrDefault() string {
	if c.Path == "" {
		return "codex"
	}
	return c.Path
}

func codexAvailableAt(path string) error {
	_, err := exec.LookPath(path)
	if err != nil {
		return fmt.Errorf("codex CLI not found at %q — install Codex CLI to use CARD sessions", path)
	}
	return nil
}

func (c *CodexRuntime) invokeInteractive(codexPath string, env []string, opts InvokeOptions) error {
	prompt := codexPrompt(opts.SystemPrompt, opts.InitialMessage)
	sandbox := MapToolsToSandbox(opts.AllowedTools)
	// Use --no-alt-screen to run in inline mode, preventing TUI conflicts with CARD's
	// phase banners. Without this, Codex's alternate screen mode causes display corruption
	// when CARD prints UI elements before/after runtime invocation.
	args := []string{"--no-alt-screen", "--sandbox", sandbox}
	if prompt != "" {
		args = append(args, prompt)
	}

	cmd := exec.Command(codexPath, args...)
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
		return fmt.Errorf("failed to start codex: %w", err)
	}
	if opts.OnStart != nil {
		opts.OnStart()
	}

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cmd.Wait()
	}()

	if opts.OutputDir != "" {
		ticker := time.NewTicker(codexSignalPollInterval)
		defer ticker.Stop()

		for {
			select {
			case err := <-doneCh:
				return codexHandleProcessExit(err)

			case <-ticker.C:
				sig, sigErr := signal.CheckPhaseComplete(opts.OutputDir)
				if sigErr != nil {
					continue
				}
				if sig != nil && sig.Status == "complete" {
					_ = cmd.Process.Signal(syscall.SIGTERM)

					select {
					case <-doneCh:
					case <-time.After(codexSignalGracePeriod):
						_ = cmd.Process.Kill()
						<-doneCh
					}

					codexResetTerminal()
					return ErrPhaseComplete
				}
			}
		}
	}

	err := <-doneCh
	return codexHandleProcessExit(err)
}

func (c *CodexRuntime) invokeNonInteractive(codexPath string, env []string, opts InvokeOptions) error {
	prompt := codexPrompt(opts.SystemPrompt, opts.InitialMessage)
	sandbox := MapToolsToSandbox(opts.AllowedTools)
	// Use exec with --full-auto for approval-free non-interactive execution.
	// Pass prompt via stdin with "-" to avoid command-line length limits.
	args := []string{"exec", "--full-auto", "--sandbox", sandbox, "-"}

	cmd := exec.Command(codexPath, args...)
	if opts.WorkingDir != "" {
		cmd.Dir = opts.WorkingDir
	}
	cmd.Env = env
	cmd.Stdin = strings.NewReader(prompt)
	// Keep stdout/stderr visible so we can see progress and debug issues
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start codex: %w", err)
	}

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cmd.Wait()
	}()

	// Set up signal handling for Ctrl+C during non-interactive mode.
	// Since Codex isn't connected to the terminal, SIGINT goes to CARD.
	// We need to catch it and terminate Codex gracefully.
	sigCh := make(chan os.Signal, 1)
	ossignal.Notify(sigCh, os.Interrupt)
	defer ossignal.Stop(sigCh)

	// Poll for phase complete signal, same as interactive mode
	if opts.OutputDir != "" {
		ticker := time.NewTicker(codexSignalPollInterval)
		defer ticker.Stop()

		for {
			select {
			case err := <-doneCh:
				return codexHandleProcessExit(err)

			case <-sigCh:
				// Ctrl+C received - terminate Codex and return interrupted
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-doneCh:
				case <-time.After(codexSignalGracePeriod):
					_ = cmd.Process.Kill()
					<-doneCh
				}
				codexResetTerminal()
				return ErrInterrupted

			case <-ticker.C:
				sig, sigErr := signal.CheckPhaseComplete(opts.OutputDir)
				if sigErr != nil {
					continue
				}
				if sig != nil && sig.Status == "complete" {
					_ = cmd.Process.Signal(syscall.SIGTERM)

					select {
					case <-doneCh:
					case <-time.After(codexSignalGracePeriod):
						_ = cmd.Process.Kill()
						<-doneCh
					}

					codexResetTerminal()
					return ErrPhaseComplete
				}
			}
		}
	}

	// No OutputDir - still need to handle Ctrl+C
	for {
		select {
		case err := <-doneCh:
			return codexHandleProcessExit(err)
		case <-sigCh:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-doneCh:
			case <-time.After(codexSignalGracePeriod):
				_ = cmd.Process.Kill()
				<-doneCh
			}
			codexResetTerminal()
			return ErrInterrupted
		}
	}
}

func codexPrompt(systemPrompt, initialMessage string) string {
	switch {
	case systemPrompt != "" && initialMessage != "":
		return systemPrompt + "\n\n" + initialMessage
	case systemPrompt != "":
		return systemPrompt
	default:
		return initialMessage
	}
}

func codexHandleProcessExit(err error) error {
	// Reset terminal state after Codex exits. Codex's TUI can leave the terminal
	// in raw mode where \n doesn't include carriage return, causing CARD's
	// lipgloss-rendered boxes to display with characters pushed to the right.
	codexResetTerminal()

	if err == nil {
		return nil
	}
	if codexIsInterrupt(err) {
		return ErrInterrupted
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("codex exited with code %d", exitErr.ExitCode())
	}
	return fmt.Errorf("failed to run codex: %w", err)
}

// codexResetTerminal resets the terminal to a sane state after Codex exits. Codex can
// leave the terminal in raw mode where \n doesn't reset cursor to column 0.
func codexResetTerminal() {
	// Use stty sane to reset terminal to normal cooked mode
	cmd := exec.Command("stty", "sane")
	cmd.Stdin = os.Stdin
	_ = cmd.Run()

	// Also print reset escape sequence and carriage return to be safe
	fmt.Fprint(os.Stderr, "\033[0m\r")
}

func codexIsInterrupt(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.Signal() == syscall.SIGINT || status.Signal() == syscall.SIGTERM
		}
	}
	return false
}
