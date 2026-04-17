package runtime

import (
	"bytes"
	"context"
	"os/exec"
)

type LocalRuntime struct {
	workDir string
}

func NewLocal(workDir string) *LocalRuntime {
	return &LocalRuntime{workDir: workDir}
}

func (r *LocalRuntime) Exec(ctx context.Context, command string, args ...string) (*ExecOutput, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = r.workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, err
		}
	}
	return &ExecOutput{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, nil
}

func (r *LocalRuntime) Close() error { return nil }
