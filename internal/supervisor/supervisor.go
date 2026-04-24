package supervisor

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Stonefish-Labs/opencode-agent/internal/health"
	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
	"github.com/Stonefish-Labs/opencode-agent/internal/keychain"
	"github.com/Stonefish-Labs/opencode-agent/internal/process"
)

const (
	maxLogSizeBytes = 10 * 1024 * 1024
	maxLogFiles     = 5
)

type Running struct {
	Cmd  *exec.Cmd
	Done <-chan error
}

func Run(ctx context.Context, cfg instance.Config, paths instance.Paths, creds keychain.Credentials, output io.Writer) error {
	logOutput, closeLog, err := configureLogging(paths, output, creds)
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

		proc, bindHost, err := Start(ctx, cfg, paths, creds, logOutput)
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
	cmd := exec.Command(cfg.OpenCodeBinary, "serve", "--hostname", bindHost, "--port", strconv.Itoa(cfg.Port), "--print-logs") // #nosec G204 -- OpenCodeBinary is resolved to an executable at install time and never shell-expanded.
	cmd.Dir = cfg.WorkingDirectory
	cmd.Env = BuildEnvironment(cfg, creds, os.Environ())
	writer := redactingWriter{writer: output, secrets: secretsForRedaction(creds)}
	cmd.Stdout = writer
	cmd.Stderr = writer
	process.Default.Configure(cmd)
	return cmd
}

func BuildEnvironment(cfg instance.Config, creds keychain.Credentials, parent []string) []string {
	env := []string{}
	allowed := map[string]bool{}
	for _, name := range cfg.AllowedEnvironment {
		allowed[name] = true
	}
	for _, entry := range parent {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" || strings.HasPrefix(name, "OPENCODE_SERVER_") {
			continue
		}
		switch cfg.EnvironmentPolicy {
		case "inherit":
			env = append(env, entry)
		case "minimal":
			if allowed[name] || isEssentialEnv(name) {
				env = append(env, entry)
			}
		default:
			if allowed[name] || isSafeFilteredEnv(name) {
				env = append(env, entry)
			}
		}
	}
	env = appendOrReplaceEnv(env,
		"OPENCODE_SERVER_USERNAME="+creds.Username,
		"OPENCODE_SERVER_PASSWORD="+creds.Password,
		"OPENCODE_EXPERIMENTAL_DISABLE_FILEWATCHER=true",
	)
	return env
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
	if cfg.AllowAllInterfacesFallback && instance.IsTailscaleIPv4(cfg.BindHost) {
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

func configureLogging(paths instance.Paths, output io.Writer, creds keychain.Credentials) (io.Writer, func(), error) {
	if err := os.MkdirAll(filepath.Dir(paths.LogPath), 0o700); err != nil {
		return output, func() {}, err
	}
	file, err := newRotatingLog(paths.LogPath, maxLogSizeBytes, maxLogFiles)
	if err != nil {
		return output, func() {}, err
	}
	writer := redactingWriter{writer: io.MultiWriter(output, file), secrets: secretsForRedaction(creds)}
	log.SetOutput(writer)
	return writer, func() { _ = file.Close() }, nil
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
	writer  io.Writer
	secrets []string
}

func (w redactingWriter) Write(p []byte) (int, error) {
	text := Redact(string(p), w.secrets)
	_, err := w.writer.Write([]byte(text))
	return len(p), err
}

var (
	headerSecretPattern   = regexp.MustCompile(`(?i)\b(authorization|cookie|set-cookie)\s*[:=]\s*([^\s]+.*)`)
	bearerPattern         = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	keyValueSecretPattern = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(password|passwd|secret|token|api[_-]?key|credential|session)[A-Za-z0-9_.-]*)\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,;]+)`)
	urlUserinfoPattern    = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`)
	controlPattern        = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
)

func Redact(text string, secrets []string) string {
	text = controlPattern.ReplaceAllString(text, "")
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	text = urlUserinfoPattern.ReplaceAllString(text, "${1}[redacted]@")
	text = bearerPattern.ReplaceAllString(text, "Bearer [redacted]")
	text = headerSecretPattern.ReplaceAllString(text, "$1: [redacted]")
	text = keyValueSecretPattern.ReplaceAllString(text, "$1=[redacted]")
	return capLines(text, 8192)
}

func secretsForRedaction(creds keychain.Credentials) []string {
	values := []string{creds.Password}
	if creds.Username != "" && creds.Password != "" {
		pair := creds.Username + ":" + creds.Password
		values = append(values, pair, base64.StdEncoding.EncodeToString([]byte(pair)))
	}
	return values
}

func capLines(text string, limit int) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if len(line) > limit {
			suffix := ""
			if strings.HasSuffix(line, "\n") {
				suffix = "\n"
				line = strings.TrimSuffix(line, "\n")
			}
			lines[i] = line[:limit] + "... [truncated]" + suffix
		}
	}
	return strings.Join(lines, "")
}

func appendOrReplaceEnv(env []string, entries ...string) []string {
	for _, entry := range entries {
		name, _, _ := strings.Cut(entry, "=")
		replaced := false
		for i, existing := range env {
			existingName, _, _ := strings.Cut(existing, "=")
			if existingName == name {
				env[i] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, entry)
		}
	}
	return env
}

func isSafeFilteredEnv(name string) bool {
	if isSecretEnvName(name) {
		return false
	}
	return isEssentialEnv(name) ||
		strings.HasPrefix(name, "LC_") ||
		strings.HasPrefix(name, "XDG_") ||
		strings.HasSuffix(name, "_PROXY") ||
		strings.HasSuffix(name, "_proxy")
}

func isEssentialEnv(name string) bool {
	switch name {
	case "PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TZ",
		"TMPDIR", "TMP", "TEMP", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "NODE_EXTRA_CA_CERTS":
		return true
	default:
		return false
	}
}

func isSecretEnvName(name string) bool {
	upper := strings.ToUpper(name)
	for _, part := range []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "API_KEY", "APIKEY", "CREDENTIAL", "SESSION"} {
		if strings.Contains(upper, part) {
			return true
		}
	}
	return strings.HasSuffix(upper, "_KEY") || strings.Contains(upper, "_KEY_")
}

type rotatingLog struct {
	mu        sync.Mutex
	path      string
	maxBytes  int64
	retention int
	file      *os.File
	size      int64
}

func newRotatingLog(path string, maxBytes int64, retention int) (*rotatingLog, error) {
	logFile := &rotatingLog{path: path, maxBytes: maxBytes, retention: retention}
	if err := logFile.open(); err != nil {
		return nil, err
	}
	if logFile.size > maxBytes {
		if err := logFile.rotate(); err != nil {
			_ = logFile.Close()
			return nil, err
		}
	}
	return logFile, nil
}

func (l *rotatingLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	if l.size+int64(len(p)) > l.maxBytes && l.size > 0 {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := l.file.Write(p)
	l.size += int64(n)
	return n, err
}

func (l *rotatingLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *rotatingLog) open() error {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	l.file = file
	l.size = info.Size()
	return nil
}

func (l *rotatingLog) rotate() error {
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			return err
		}
		l.file = nil
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", l.path, l.retention))
	for i := l.retention - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", l.path, i)
		newPath := fmt.Sprintf("%s.%d", l.path, i+1)
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				return err
			}
		}
	}
	if _, err := os.Stat(l.path); err == nil {
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			return err
		}
	}
	return l.open()
}
