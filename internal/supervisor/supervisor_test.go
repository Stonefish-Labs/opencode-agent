package supervisor

import (
	"context"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
	"github.com/Stonefish-Labs/opencode-agent/internal/keychain"
	"github.com/Stonefish-Labs/opencode-agent/internal/process"
)

func TestBindCandidatesTailnetFallbackRequiresOptIn(t *testing.T) {
	cfg := instance.Config{BindHost: "100.64.1.2"}
	got := BindCandidates(cfg)
	if len(got) != 1 || got[0] != "100.64.1.2" {
		t.Fatalf("bind candidates = %#v", got)
	}
	cfg.AllowAllInterfacesFallback = true
	got = BindCandidates(cfg)
	if len(got) != 2 || got[0] != "100.64.1.2" || got[1] != "0.0.0.0" {
		t.Fatalf("fallback bind candidates = %#v", got)
	}
}

func TestBuildEnvironmentFiltersSecretsByDefault(t *testing.T) {
	cfg := instance.Config{EnvironmentPolicy: "filtered", AllowedEnvironment: []string{"SAFE_ALLOWED"}}
	got := BuildEnvironment(cfg, keychain.Credentials{Username: "u", Password: "p"}, []string{
		"PATH=/bin",
		"HOME=/home/me",
		"AWS_SECRET_ACCESS_KEY=secret",
		"SAFE_ALLOWED=x",
		"HTTP_PROXY=http://proxy",
		"OPENCODE_SERVER_PASSWORD=parent",
	})
	joined := strings.Join(got, "\n")
	for _, want := range []string{"PATH=/bin", "HOME=/home/me", "SAFE_ALLOWED=x", "HTTP_PROXY=http://proxy", "OPENCODE_SERVER_USERNAME=u", "OPENCODE_SERVER_PASSWORD=p"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("environment missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "AWS_SECRET_ACCESS_KEY") || strings.Contains(joined, "parent") {
		t.Fatalf("environment leaked secret values:\n%s", joined)
	}
}

func TestRedactSanitizesCommonSecretShapes(t *testing.T) {
	input := "Authorization: Bearer abc123\nhttps://user:pass@example.test\npassword=hunter2\nBasic " + base64.StdEncoding.EncodeToString([]byte("opencode:hunter2"))
	got := Redact(input, []string{"hunter2", "opencode:hunter2", base64.StdEncoding.EncodeToString([]byte("opencode:hunter2"))})
	for _, leaked := range []string{"abc123", "user:pass", "hunter2", base64.StdEncoding.EncodeToString([]byte("opencode:hunter2"))} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q:\n%s", leaked, got)
		}
	}
}

func TestStartHandlesExitedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell script")
	}
	resetSupervisorEnv(t)
	cfg, paths := supervisorConfig(t, tempScript(t, "opencode-exit", "#!/bin/sh\nexit 42\n"))
	proc, _, err := Start(contextWithTimeout(t), cfg, paths, keychain.Credentials{Username: "opencode", Password: "secret"}, os.Stdout)
	if err == nil {
		if proc != nil && proc.Cmd != nil && proc.Cmd.Process != nil {
			process.Default.Kill(proc.Cmd.Process.Pid)
		}
		t.Fatalf("expected start error for exited child")
	}
}

func TestStartKeepsAliveUnhealthyProcessForDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell script")
	}
	resetSupervisorEnv(t)
	cfg, paths := supervisorConfig(t, tempScript(t, "opencode-sleep", "#!/bin/sh\nsleep 10\n"))
	proc, _, err := Start(contextWithTimeout(t), cfg, paths, keychain.Credentials{Username: "opencode", Password: "secret"}, os.Stdout)
	if err != nil {
		t.Fatalf("expected alive unhealthy process to stay up: %v", err)
	}
	if proc == nil || proc.Cmd == nil || proc.Cmd.Process == nil || !process.Default.Alive(proc.Cmd.Process.Pid) {
		t.Fatalf("expected child process to be alive")
	}
	process.Default.Kill(proc.Cmd.Process.Pid)
	<-proc.Done
}

func supervisorConfig(t *testing.T, binary string) (instance.Config, instance.Paths) {
	t.Helper()
	cfg, err := instance.NormalizeConfig(instance.Config{
		Name:               "default",
		OpenCodeBinary:     binary,
		WorkingDirectory:   t.TempDir(),
		Port:               freePort(t),
		Username:           "opencode",
		BindHost:           "127.0.0.1",
		AdvertiseHost:      "127.0.0.1",
		HealthTimeoutSec:   1,
		RestartDelaySecond: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := instance.SaveConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, paths
}

func resetSupervisorEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OPENCODE_AGENT_CONFIG_DIR", t.TempDir())
	t.Setenv("OPENCODE_AGENT_STATE_DIR", t.TempDir())
}

func tempScript(t *testing.T, name string, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func contextWithTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
