package runtime

import "errors"

type InvokeMode int

const (
	ModeInteractive InvokeMode = iota
	ModeNonInteractive
)

type InvokeOptions struct {
	SystemPrompt   string
	InitialMessage string
	WorkingDir     string
	AllowedTools   []string
	OutputDir      string
	Mode           InvokeMode
	OnStart        func()
	MCPServerName  string
	CardBinaryPath string
}

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
