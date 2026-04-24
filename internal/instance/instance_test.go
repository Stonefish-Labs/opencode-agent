package instance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{name: "", want: "default"},
		{name: "work-api", want: "work-api"},
		{name: "Project_1.dev", want: "Project_1.dev"},
		{name: "../bad", wantErr: true},
		{name: "-bad", wantErr: true},
		{name: "bad name", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeName(tt.name)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeName: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPathsForUsesNamedInstanceFiles(t *testing.T) {
	resetEnv(t)
	paths, err := PathsFor("api")
	if err != nil {
		t.Fatalf("PathsFor: %v", err)
	}
	if !strings.HasSuffix(paths.ConfigPath, filepath.Join("instances", "api.json")) {
		t.Fatalf("config path = %q", paths.ConfigPath)
	}
	if !strings.HasSuffix(paths.StatePath, filepath.Join("instances", "api", "state.json")) {
		t.Fatalf("state path = %q", paths.StatePath)
	}
}

func TestSaveConfigDoesNotPersistPasswordAndListConfigs(t *testing.T) {
	resetEnv(t)
	cfg, err := NormalizeConfig(Config{
		Name:             "default",
		OpenCodeBinary:   "/bin/opencode",
		WorkingDirectory: t.TempDir(),
		Port:             4096,
		Username:         "opencode",
		BindHost:         "100.64.1.2",
		AdvertiseHost:    "100.64.1.2",
	})
	if err != nil {
		t.Fatalf("NormalizeConfig: %v", err)
	}
	paths, err := SaveConfig(cfg)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	data, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "password") {
		t.Fatalf("config should not contain password fields: %s", data)
	}

	configs, pathsList, err := ListConfigs()
	if err != nil {
		t.Fatalf("ListConfigs: %v", err)
	}
	if len(configs) != 1 || configs[0].Name != "default" {
		t.Fatalf("configs = %#v", configs)
	}
	if len(pathsList) != 1 || pathsList[0].ConfigPath != paths.ConfigPath {
		t.Fatalf("paths = %#v", pathsList)
	}
}

func TestNormalizeExposureConfig(t *testing.T) {
	exposure, err := NormalizeExposureConfig(&ExposureConfig{
		Provider: ExposureProviderTailscale,
	})
	if err != nil {
		t.Fatalf("NormalizeExposureConfig: %v", err)
	}
	if exposure.Mode != ExposureModeServe || exposure.HTTPSPort != 443 || exposure.Path != "/" || exposure.Public {
		t.Fatalf("unexpected serve defaults: %#v", exposure)
	}

	exposure, err = NormalizeExposureConfig(&ExposureConfig{
		Provider:  ExposureProviderTailscale,
		Mode:      ExposureModeFunnel,
		Public:    true,
		HTTPSPort: 8443,
		Path:      "opencode/",
	})
	if err != nil {
		t.Fatalf("NormalizeExposureConfig funnel: %v", err)
	}
	if !exposure.Public || exposure.Path != "/opencode" {
		t.Fatalf("unexpected funnel config: %#v", exposure)
	}

	if _, err := NormalizeExposureConfig(&ExposureConfig{Provider: ExposureProviderTailscale, Mode: ExposureModeFunnel}); err == nil {
		t.Fatalf("expected funnel without public confirmation to fail")
	}
	if _, err := NormalizeExposureConfig(&ExposureConfig{Provider: ExposureProviderTailscale, Mode: ExposureModeFunnel, Public: true, HTTPSPort: 8080}); err == nil {
		t.Fatalf("expected invalid funnel port to fail")
	}
}

func TestStateRoundTrip(t *testing.T) {
	resetEnv(t)
	paths, err := PathsFor("default")
	if err != nil {
		t.Fatal(err)
	}
	SaveState(paths, State{PID: 123, BindHost: "127.0.0.1"})
	got := LoadState(paths)
	if got.PID != 123 || got.BindHost != "127.0.0.1" {
		blob, _ := json.Marshal(got)
		t.Fatalf("state = %s", blob)
	}
	ClearState(paths)
	if got := LoadState(paths); got.PID != 0 {
		t.Fatalf("expected cleared state, got %#v", got)
	}
}

func TestLogTailReadsRotatedLogs(t *testing.T) {
	resetEnv(t)
	paths, err := PathsFor("default")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LogPath+".1", []byte("old1\nold2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LogPath, []byte("new1\nnew2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := LogTail(paths, 3)
	for _, want := range []string{"old2", "new1", "new2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tail missing %q: %q", want, got)
		}
	}
}

func resetEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OPENCODE_AGENT_CONFIG_DIR", t.TempDir())
	t.Setenv("OPENCODE_AGENT_STATE_DIR", t.TempDir())
}
