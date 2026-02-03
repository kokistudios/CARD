package runtime

import "errors"

// InvokeMode controls how an AI runtime is launched.
type InvokeMode int

const (
	// ModeInteractive opens the runtime in interactive mode.
	ModeInteractive InvokeMode = iota
	// ModeNonInteractive runs the runtime to completion without interaction.
	ModeNonInteractive
)

// InvokeOptions configures a runtime invocation.
type InvokeOptions struct {
	SystemPrompt   string
	InitialMessage string
	WorkingDir     string
	AllowedTools   []string
	OutputDir      string
	Mode           InvokeMode
	OnStart        func()
}

// Runtime represents a supported AI runtime.
type Runtime interface {
	Name() string
	Available() error
	Invoke(opts InvokeOptions) error
	ConfigureMCP(cardBinaryPath, serverName string) error
}

var (
	ErrInterrupted   = errors.New("runtime interrupted")
	ErrPhaseComplete = errors.New("phase complete signal received")
)
