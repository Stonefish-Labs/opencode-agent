package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Stonefish-Labs/opencode-agent/internal/health"
	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
	"github.com/Stonefish-Labs/opencode-agent/internal/keychain"
	"github.com/Stonefish-Labs/opencode-agent/internal/process"
)

type Running struct {
	Cmd  *exec.Cmd
	Done <-chan error
}

func Run(ctx context.Context, cfg instance.Config, paths instance.Paths, creds keychain.Credentials, output io.Writer) error {
	closeLog, err := configureLogging(paths, output)
	if err != nil {
		return err
	}
	defer closeLog()
	log.Printf("agent %s starting: workdir=%s advertised=%s", cfg.Name, cfg.WorkingDirectory, cfg.AdvertiseURL)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		proc, bindHost, err := Start(ctx, cfg, paths, creds, output)
		if err != nil {
			log.Printf("start failed: %v", err)
			instance.SaveState(paths, instance.State{LastError: err.Error()})
			if !sleepOrDone(ctx, time.Duration(cfg.RestartDelaySecond)*time.Second) {
				return nil
			}
			continue
		}

		select {
		case <-ctx.Done():
			log.Printf("stopping opencode pid=%d", proc.Cmd.Process.Pid)
			process.Default.Terminate(proc.Cmd.Process.Pid)
			select {
			case <-proc.Done:
			case <-time.After(3 * time.Second):
				process.Default.Kill(proc.Cmd.Process.Pid)
				<-proc.Done
			}
			instance.ClearState(paths)
			return nil
		case err := <-proc.Done:
			state := instance.State{BindHost: bindHost, LastExit: time.Now().Format(time.RFC3339)}
			if proc.Cmd.ProcessState != nil {
				state.PID = proc.Cmd.ProcessState.Pid()
				state.LastExit = fmt.Sprintf("%s: %s", state.LastExit, proc.Cmd.ProcessState.String())
			}
			if err != nil {
				state.LastError = err.Error()
				log.Printf("opencode exited: %v", err)
			} else {
				log.Printf("opencode exited")
			}
			instance.SaveState(paths, state)
			if !sleepOrDone(ctx, time.Duration(cfg.RestartDelaySecond)*time.Second) {
				return nil
			}
		}
	}
}

func Start(ctx context.Context, cfg instance.Config, paths instance.Paths, creds keychain.Credentials, output io.Writer) (*Running, string, error) {
	candidates := BindCandidates(cfg)
	var lastErr error
	for index, bindHost := range candidates {
		cmd := BuildCommand(cfg, creds, bindHost, output)
		if err := cmd.Start(); err != nil {
			lastErr = fmt.Errorf("start with bind host %s: %w", bindHost, err)
			continue
		}
		instance.SaveState(paths, instance.State{PID: cmd.Process.Pid, BindHost: bindHost, StartedAt: time.Now()})
		log.Printf("started opencode pid=%d bind=%s advertised=%s", cmd.Process.Pid, bindHost, cfg.AdvertiseURL)

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		localBase := instance.LocalBaseURLForBind(bindHost, cfg.Port)
		healthy, exited := waitUntilHealthOrExit(ctx, localBase, creds, time.Duration(cfg.HealthTimeoutSec)*time.Second, done)
		if healthy {
			return &Running{Cmd: cmd, Done: done}, bindHost, nil
		}
		if exited != nil {
			lastErr = fmt.Errorf("opencode exited before %s became healthy: %w", localBase, exited)
			continue
		}
		if index < len(candidates)-1 {
			log.Printf("bind=%s did not become healthy; retrying with %s", bindHost, candidates[index+1])
			process.Default.Terminate(cmd.Process.Pid)
			waitForProcessExit(cmd, done, 3*time.Second)
			continue
		}
		log.Printf("opencode pid=%d is alive, but %s did not become healthy; leaving process running for diagnostics", cmd.Process.Pid, localBase)
		return &Running{Cmd: cmd, Done: done}, bindHost, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no bind candidates available")
	}
	return nil, "", lastErr
}

func BuildCommand(cfg instance.Config, creds keychain.Credentials, bindHost string, output io.Writer) *exec.Cmd {
	cmd := exec.Command(cfg.OpenCodeBinary, "serve", "--hostname", bindHost, "--port", strconv.Itoa(cfg.Port), "--print-logs")
	cmd.Dir = cfg.WorkingDirectory
	cmd.Env = append(os.Environ(),
		"OPENCODE_SERVER_USERNAME="+creds.Username,
		"OPENCODE_SERVER_PASSWORD="+creds.Password,
		"OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER=true",
	)
	writer := redactingWriter{writer: output, password: creds.Password}
	cmd.Stdout = writer
	cmd.Stderr = writer
	process.Default.Configure(cmd)
	return cmd
}

func Stop(paths instance.Paths, timeout time.Duration) {
	state := instance.LoadState(paths)
	if state.PID <= 0 || !process.Default.Alive(state.PID) {
		instance.ClearState(paths)
		return
	}
	process.Default.Terminate(state.PID)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !process.Default.Alive(state.PID) {
			instance.ClearState(paths)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	process.Default.Kill(state.PID)
	instance.ClearState(paths)
}

func BindCandidates(cfg instance.Config) []string {
	seen := map[string]bool{}
	candidates := []string{}
	add := func(host string) {
		host = strings.TrimSpace(host)
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		candidates = append(candidates, host)
	}
	add(cfg.BindHost)
	if instance.IsTailscaleIPv4(cfg.BindHost) {
		add("0.0.0.0")
	}
	return candidates
}

func waitUntilHealthOrExit(ctx context.Context, baseURL string, creds keychain.Credentials, timeout time.Duration, done <-chan error) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		result := health.Check(baseURL, &creds, 2*time.Second)
		if result.OK {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		timer := time.NewTimer(300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case err := <-done:
			timer.Stop()
			if err == nil {
				err = errors.New("process exited")
			}
			return false, err
		case <-timer.C:
		}
	}
}

func waitForProcessExit(cmd *exec.Cmd, done <-chan error, timeout time.Duration) {
	select {
	case <-done:
	case <-time.After(timeout):
		process.Default.Kill(cmd.Process.Pid)
		<-done
	}
}

func configureLogging(paths instance.Paths, output io.Writer) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(paths.LogPath), 0o700); err != nil {
		return func() {}, err
	}
	file, err := os.OpenFile(paths.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return func() {}, err
	}
	log.SetOutput(io.MultiWriter(output, file))
	return func() { _ = file.Close() }, nil
}

func sleepOrDone(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type redactingWriter struct {
	writer   io.Writer
	password string
}

func (w redactingWriter) Write(p []byte) (int, error) {
	text := string(p)
	if w.password != "" {
		text = strings.ReplaceAll(text, w.password, "[redacted]")
	}
	_, err := w.writer.Write([]byte(text))
	return len(p), err
}
