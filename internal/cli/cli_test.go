package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
	"github.com/Stonefish-Labs/opencode-agent/internal/keychain"
)

func TestInstallDryRunShowsNamedPlan(t *testing.T) {
	resetCLIEnv(t)
	binary := tempExecutable(t, "opencode")
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"install",
		"--dry-run",
		"--name", "api",
		"--workdir", t.TempDir(),
		"--binary", binary,
		"--advertise-host", "100.64.1.2",
		"--allow-insecure-remote-http",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"api.json", "100.64.1.2", "[generated at install time", "Project config", "--name"} {
		if !strings.Contains(output, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Password: old") {
		t.Fatalf("dry-run should not print real passwords:\n%s", output)
	}
}

func TestInstallRejectsInsecureRemoteHTTPByDefault(t *testing.T) {
	resetCLIEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"install",
		"--dry-run",
		"--workdir", t.TempDir(),
		"--binary", tempExecutable(t, "opencode"),
		"--advertise-host", "100.64.1.2",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected insecure remote HTTP to be rejected; stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--allow-insecure-remote-http") {
		t.Fatalf("stderr should mention explicit escape hatch: %s", stderr.String())
	}
}

func TestInstallAcceptsHTTPSAdvertiseURL(t *testing.T) {
	resetCLIEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"install",
		"--dry-run",
		"--workdir", t.TempDir(),
		"--binary", tempExecutable(t, "opencode"),
		"--advertise-url", "https://agent.example.test/opencode",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "https://agent.example.test/opencode") {
		t.Fatalf("dry-run output missing advertise URL:\n%s", stdout.String())
	}
}

func TestInstallDryRunWithTailscaleServeExposure(t *testing.T) {
	resetCLIEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"install",
		"--dry-run",
		"--workdir", t.TempDir(),
		"--binary", tempExecutable(t, "opencode"),
		"--expose", "tailscale",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		`"provider": "tailscale"`,
		`"mode": "serve"`,
		"Exposure scope: tailnet-only",
		"tailscale serve --bg --https=443 --yes http://127.0.0.1:4096",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, output)
		}
	}
}

func TestInstallRejectsTailscaleFunnelWithoutPublicConfirmation(t *testing.T) {
	resetCLIEnv(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"install",
		"--dry-run",
		"--workdir", t.TempDir(),
		"--binary", tempExecutable(t, "opencode"),
		"--expose", "tailscale",
		"--tailscale-mode", "funnel",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected funnel without confirmation to fail; stdout=%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "public=true") {
		t.Fatalf("stderr should mention public confirmation: %s", stderr.String())
	}
}

func TestExposeTailscaleDryRun(t *testing.T) {
	resetCLIEnv(t)
	cfg, err := instance.NormalizeConfig(instance.Config{
		Name:             "api",
		OpenCodeBinary:   tempExecutable(t, "opencode"),
		WorkingDirectory: t.TempDir(),
		Port:             4101,
		Username:         "opencode",
		BindHost:         "127.0.0.1",
		AdvertiseHost:    "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"expose",
		"tailscale",
		"api",
		"--dry-run",
		"--path", "/opencode",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		`"provider": "tailscale"`,
		`"path": "/opencode"`,
		"tailscale serve --bg --https=443 --set-path=/opencode --yes http://127.0.0.1:4101",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, output)
		}
	}
}

func TestExposeOffDryRun(t *testing.T) {
	resetCLIEnv(t)
	cfg, err := instance.NormalizeConfig(instance.Config{
		Name:             "api",
		OpenCodeBinary:   tempExecutable(t, "opencode"),
		WorkingDirectory: t.TempDir(),
		Port:             4101,
		Username:         "opencode",
		BindHost:         "127.0.0.1",
		AdvertiseHost:    "machine.tailnet.ts.net",
		AdvertiseURL:     "https://machine.tailnet.ts.net/opencode",
		Exposure: &instance.ExposureConfig{
			Provider:  instance.ExposureProviderTailscale,
			Mode:      instance.ExposureModeServe,
			HTTPSPort: 443,
			Path:      "/opencode",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"expose", "off", "api", "--dry-run"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%s", code, stderr.String())
	}
	output := stdout.String()
	if strings.Contains(output, `"exposure"`) {
		t.Fatalf("dry-run off config should remove exposure:\n%s", output)
	}
	if !strings.Contains(output, "tailscale serve --https=443 --set-path=/opencode --yes off") {
		t.Fatalf("dry-run off missing tailscale off command:\n%s", output)
	}
}

func TestListJSONAndStatus(t *testing.T) {
	resetCLIEnv(t)
	cfg, err := instance.NormalizeConfig(instance.Config{
		Name:             "api",
		OpenCodeBinary:   tempExecutable(t, "opencode"),
		WorkingDirectory: t.TempDir(),
		Port:             49999,
		Username:         "opencode",
		BindHost:         "127.0.0.1",
		AdvertiseHost:    "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := keychain.Store("api", keychain.Credentials{Username: "opencode", Password: "secret"}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code=%d stderr=%s", code, stderr.String())
	}
	var reports []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &reports); err != nil {
		t.Fatalf("list was not json: %s", stdout.String())
	}
	if len(reports) != 1 || reports[0]["name"] != "api" {
		t.Fatalf("reports = %#v", reports)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"status", "api"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Name: api") {
		t.Fatalf("status output = %s", stdout.String())
	}
}

func TestShowAndRotatePassword(t *testing.T) {
	resetCLIEnv(t)
	cfg, err := instance.NormalizeConfig(instance.Config{
		Name:             "default",
		OpenCodeBinary:   tempExecutable(t, "opencode"),
		WorkingDirectory: t.TempDir(),
		Port:             4096,
		Username:         "opencode",
		BindHost:         "127.0.0.1",
		AdvertiseHost:    "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instance.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := keychain.Store("default", keychain.Credentials{Username: "opencode", Password: "old"}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"show-password"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show-password code=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "old") {
		t.Fatalf("show-password should hide secret without --reveal: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"show-password", "--reveal"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show-password --reveal code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "old") {
		t.Fatalf("show-password --reveal output = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"rotate-password", "--restart=false"}, &stdout, &stderr); code != 0 {
		t.Fatalf("rotate-password code=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "Password: old") {
		t.Fatalf("rotate-password should not reveal secrets by default: %s", stdout.String())
	}
	creds, err := keychain.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if creds.Password == "old" || creds.Password == "" {
		t.Fatalf("password was not rotated: %#v", creds)
	}
	if creds.CreatedAt.IsZero() || creds.RotatedAt.IsZero() {
		t.Fatalf("credential metadata was not recorded: %#v", creds)
	}
}

func resetCLIEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OPENCODE_AGENT_CONFIG_DIR", t.TempDir())
	t.Setenv("OPENCODE_AGENT_STATE_DIR", t.TempDir())
	t.Setenv("OPENCODE_AGENT_TEST_KEYRING", "memory")
	keychain.ResetMemory()
}

func tempExecutable(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(""), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
}
