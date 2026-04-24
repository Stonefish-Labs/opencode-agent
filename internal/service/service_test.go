package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPlanPerInstanceNames(t *testing.T) {
	tests := []struct {
		goos        string
		serviceName string
		command     string
	}{
		{goos: "darwin", serviceName: "com.opencode.agent.api", command: "launchctl"},
		{goos: "linux", serviceName: "opencode-agent-api.service", command: "systemctl"},
		{goos: "windows", serviceName: "OpenCodeAgent-api", command: "schtasks"},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			t.Setenv("OPENCODE_AGENT_CONFIG_DIR", t.TempDir())
			t.Setenv("OPENCODE_AGENT_STATE_DIR", t.TempDir())
			plan, err := BuildPlan(tt.goos, "api", "/opt/opencode-agent")
			if err != nil {
				t.Fatalf("BuildPlan: %v", err)
			}
			if plan.ServiceName != tt.serviceName {
				t.Fatalf("service name = %q, want %q", plan.ServiceName, tt.serviceName)
			}
			if len(plan.InstallCommands) == 0 || plan.InstallCommands[0].Args[0] != tt.command {
				t.Fatalf("install commands = %#v", plan.InstallCommands)
			}
			if tt.goos != "windows" && !strings.Contains(plan.UnitContent, "--name") {
				t.Fatalf("unit should include named run entrypoint: %s", plan.UnitContent)
			}
		})
	}
}

func TestBuildPlanRejectsInvalidName(t *testing.T) {
	if _, err := BuildPlan("linux", "../bad", "/opt/opencode-agent"); err == nil {
		t.Fatalf("expected invalid name error")
	}
}

func TestStateReportsUnitFilePresence(t *testing.T) {
	t.Setenv("OPENCODE_AGENT_CONFIG_DIR", t.TempDir())
	t.Setenv("OPENCODE_AGENT_STATE_DIR", t.TempDir())
	plan, err := BuildPlan("linux", "statecheck", "/opt/opencode-agent")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(plan.UnitPath)
	t.Cleanup(func() { _ = os.Remove(plan.UnitPath) })
	if got := State(plan); got != "not-installed" {
		t.Fatalf("state before unit exists = %q", got)
	}
	if err := os.MkdirAll(filepath.Dir(plan.UnitPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan.UnitPath, []byte(plan.UnitContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := State(plan); got != "installed" {
		t.Fatalf("state after unit exists = %q", got)
	}
}
