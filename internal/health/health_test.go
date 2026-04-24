package health

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
	"github.com/Stonefish-Labs/opencode-agent/internal/keychain"
)

func TestCheckHealthSuccessAndFailure(t *testing.T) {
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer failServer.Close()

	if got := Check(okServer.URL, nil, time.Second); !got.OK {
		t.Fatalf("expected ok health: %#v", got)
	}
	got := Check(failServer.URL, &keychain.Credentials{Username: "u", Password: "p"}, time.Second)
	if got.OK || got.StatusCode != http.StatusUnauthorized || !got.RequiresAuth {
		t.Fatalf("expected auth failure classification: %#v", got)
	}
}

func TestBuildReportSeparatesLocalAndTailnet(t *testing.T) {
	t.Setenv("OPENCODE_AGENT_CONFIG_DIR", t.TempDir())
	t.Setenv("OPENCODE_AGENT_STATE_DIR", t.TempDir())
	t.Setenv("OPENCODE_AGENT_TEST_KEYRING", "memory")
	keychain.ResetMemory()

	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer local.Close()
	tailnet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusServiceUnavailable)
	}))
	defer tailnet.Close()

	localPort := local.Listener.Addr().(*net.TCPAddr).Port
	cfg := instance.Config{
		Name:             "default",
		OpenCodeBinary:   "/bin/opencode",
		WorkingDirectory: t.TempDir(),
		Port:             localPort,
		Username:         "opencode",
		BindHost:         "127.0.0.1",
		AdvertiseHost:    "127.0.0.1",
		AdvertiseURL:     tailnet.URL,
	}
	cfg, err := instance.NormalizeConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := instance.SaveConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	instance.SaveState(paths, instance.State{PID: os.Getpid(), BindHost: "127.0.0.1", StartedAt: time.Now()})
	if err := keychain.Store("default", keychain.Credentials{Username: "opencode", Password: "secret"}); err != nil {
		t.Fatal(err)
	}

	report := BuildReport(cfg, paths, "service", "installed")
	if !report.ProcessAlive {
		t.Fatalf("expected process alive")
	}
	if !report.LocalHealth.OK {
		t.Fatalf("local health should be ok: %#v", report.LocalHealth)
	}
	if report.TailnetHealth.OK || report.TailnetHealth.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("tailnet health should fail separately: %#v", report.TailnetHealth)
	}
}
