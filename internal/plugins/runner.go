package plugins

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/claudioscheer/code-graph-mcp/internal/events"
)

type EventSink interface {
	Emit(ctx context.Context, event events.GraphEvent) error
	Flush(ctx context.Context) error
}

type ExtractRequest struct {
	Repo     string
	Protocol string
}

type ExtractorPlugin struct {
	Name     string
	Language string
	Command  string
	Args     []string
	Env      map[string]string
}

type Runner struct {
	Stderr io.Writer
}

func (r Runner) Run(ctx context.Context, plugin ExtractorPlugin, req ExtractRequest, sink EventSink) error {
	args := append([]string{}, plugin.Args...)
	args = append(args, "--repo", req.Repo, "--protocol", req.Protocol)
	cmd := exec.CommandContext(ctx, plugin.Command, args...)
	cmd.Env = os.Environ()
	for key, value := range plugin.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		if r.Stderr != nil {
			_, _ = io.Copy(r.Stderr, stderr)
		}
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 1024*1024*50)
	for scanner.Scan() {
		event, err := events.DecodeLine(scanner.Bytes())
		if err != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("invalid extractor event: %w", err)
		}
		if err := sink.Emit(ctx, event); err != nil {
			_ = cmd.Process.Kill()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	if err := cmd.Wait(); err != nil {
		return err
	}
	return sink.Flush(ctx)
}
