package runtime

import "context"

type Runtime interface {
	Exec(ctx context.Context, command string, args ...string) (*ExecOutput, error)
	Close() error
}

type ExecOutput struct {
	Stdout   string
	Stderr   string
	ExitCode int
}
