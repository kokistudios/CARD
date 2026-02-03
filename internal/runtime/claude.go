package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kokistudios/card/internal/signal"
)

const (
	claudeSignalPollInterval = 500 * time.Millisecond
	claudeSignalGracePeriod  = 2 * time.Second
)

// ClaudeRuntime implements the Runtime interface for Claude Code.
type ClaudeRuntime struct {
	Path string
}

func (c *ClaudeRuntime) Name() string {
	return "claude"
}

// Available checks if the Claude CLI exists at the configured path.
func (c *ClaudeRuntime) Available() error {
	return claudeAvailableAt(c.pathOrDefault())
}

// Invoke launches Claude Code with the given options.
func (c *ClaudeRuntime) Invoke(opts InvokeOptions) error {
	claudePath := c.pathOrDefault()
	if err := claudeAvailableAt(claudePath); err != nil {
		return err
	}

	env := os.Environ()
	if opts.OutputDir != "" {
		env = append(env, "CARD_OUTPUT_DIR="+opts.OutputDir)
	}

	if opts.Mode == ModeNonInteractive {
		return c.invokeNonInteractive(claudePath, env, opts)
	}
	return c.invokeInteractive(claudePath, env, opts)
}

func (c *ClaudeRuntime) ConfigureMCP(cardBinaryPath, serverName string) error {
	claudePath := c.pathOrDefault()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	settingsPath := filepath.Join(homeDir, ".claude.json")

	type mcpServerConfig struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	type claudeSettings struct {
		MCPServers map[string]mcpServerConfig `json:"mcpServers"`
	}

	var settings claudeSettings
	settingsData, err := os.ReadFile(settingsPath)
	if err == nil {
		if err := json.Unmarshal(settingsData, &settings); err != nil {
			settings = claudeSettings{MCPServers: make(map[string]mcpServerConfig)}
		}
	} else {
		settings = claudeSettings{MCPServers: make(map[string]mcpServerConfig)}
	}

	if settings.MCPServers == nil {
		settings.MCPServers = make(map[string]mcpServerConfig)
	}

	alternateName := "card"
	if serverName == "card" {
		alternateName = "card-dev"
	}

	if existing, ok := settings.MCPServers[serverName]; ok {
		if existing.Command == cardBinaryPath {
			if _, hasAlternate := settings.MCPServers[alternateName]; hasAlternate {
				exec.Command(claudePath, "mcp", "remove", alternateName, "-s", "user").Run()
				exec.Command(claudePath, "mcp", "remove", alternateName, "-s", "local").Run()
			}
			return nil
		}
	}

	if _, hasAlternate := settings.MCPServers[alternateName]; hasAlternate {
		exec.Command(claudePath, "mcp", "remove", alternateName, "-s", "user").Run()
		exec.Command(claudePath, "mcp", "remove", alternateName, "-s", "local").Run()
	}

	if existing, ok := settings.MCPServers[serverName]; ok {
		if existing.Command != cardBinaryPath {
			exec.Command(claudePath, "mcp", "remove", serverName, "-s", "user").Run()
			exec.Command(claudePath, "mcp", "remove", serverName, "-s", "local").Run()
		}
	}

	cmd := exec.Command(claudePath, "mcp", "add", "--scope", "user", serverName, "--", cardBinaryPath, "mcp-serve")
	if _, err := cmd.CombinedOutput(); err != nil {
		return nil
	}

	return nil
}

func (c *ClaudeRuntime) pathOrDefault() string {
	if c.Path == "" {
		return "claude"
	}
	return c.Path
}

func claudeAvailableAt(path string) error {
	_, err := exec.LookPath(path)
	if err != nil {
		return fmt.Errorf("claude CLI not found at %q — install Claude Code (https://claude.ai/code) to use CARD sessions", path)
	}
	return nil
}

func (c *ClaudeRuntime) invokeInteractive(claudePath string, env []string, opts InvokeOptions) error {
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

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- cmd.Wait()
	}()

	if opts.OutputDir != "" {
		ticker := time.NewTicker(claudeSignalPollInterval)
		defer ticker.Stop()

		for {
			select {
			case err := <-doneCh:
				return claudeHandleProcessExit(err)

			case <-ticker.C:
				sig, sigErr := signal.CheckPhaseComplete(opts.OutputDir)
				if sigErr != nil {
					continue
				}
				if sig != nil && sig.Status == "complete" {
					_ = cmd.Process.Signal(syscall.SIGTERM)

					select {
					case <-doneCh:
					case <-time.After(claudeSignalGracePeriod):
						_ = cmd.Process.Kill()
						<-doneCh
					}

					return ErrPhaseComplete
				}
			}
		}
	}

	err := <-doneCh
	return claudeHandleProcessExit(err)
}

func (c *ClaudeRuntime) invokeNonInteractive(claudePath string, env []string, opts InvokeOptions) error {
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
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start claude: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		if claudeIsInterrupt(err) {
			return ErrInterrupted
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("claude exited with code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("failed to run claude: %w", err)
	}

	return nil
}

func claudeHandleProcessExit(err error) error {
	if err == nil {
		return nil
	}
	if claudeIsInterrupt(err) {
		return ErrInterrupted
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return fmt.Errorf("claude exited with code %d", exitErr.ExitCode())
	}
	return fmt.Errorf("failed to run claude: %w", err)
}

func claudeIsInterrupt(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.Signal() == syscall.SIGINT || status.Signal() == syscall.SIGTERM
		}
	}
	return false
}
