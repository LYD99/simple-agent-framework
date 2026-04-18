package runtime

import (
	"context"
	"fmt"
)

type SandboxType int

const (
	SandboxDocker SandboxType = iota
	SandboxWorktree
	SandboxE2B
)

type SandboxConfig struct {
	Type       SandboxType
	Image      string // Docker image (SandboxDocker)
	WorkDir    string
	GitRepo    string // Git repository path (used by SandboxWorktree).
	BranchName string

	// E2B specific
	E2B *E2BConfig
}

type SandboxRuntime struct {
	config SandboxConfig
	inner  Runtime
}

func NewSandbox(config SandboxConfig) (*SandboxRuntime, error) {
	switch config.Type {
	case SandboxDocker:
		return &SandboxRuntime{config: config, inner: NewLocal(config.WorkDir)}, nil
	case SandboxWorktree:
		return &SandboxRuntime{config: config, inner: NewLocal(config.WorkDir)}, nil
	case SandboxE2B:
		if config.E2B == nil {
			return nil, fmt.Errorf("E2B config is required for SandboxE2B type")
		}
		e2b, err := NewE2B(*config.E2B)
		if err != nil {
			return nil, fmt.Errorf("create E2B sandbox: %w", err)
		}
		return &SandboxRuntime{config: config, inner: e2b}, nil
	default:
		return nil, fmt.Errorf("unknown sandbox type: %d", config.Type)
	}
}

func (r *SandboxRuntime) Exec(ctx context.Context, command string, args ...string) (*ExecOutput, error) {
	return r.inner.Exec(ctx, command, args...)
}

func (r *SandboxRuntime) Close() error {
	return r.inner.Close()
}
