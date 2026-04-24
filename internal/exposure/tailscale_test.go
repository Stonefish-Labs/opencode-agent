package exposure

import (
	"context"
	"strings"
	"testing"

	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
)

type fakeRunner struct {
	output string
	err    error
	args   [][]string
}

func (r *fakeRunner) Run(ctx context.Context, args []string) (string, error) {
	r.args = append(r.args, append([]string(nil), args...))
	return r.output, r.err
}

func TestTailscalePlanServeCommand(t *testing.T) {
	cfg := instance.Config{
		Name: "default",
		Port: 4096,
		Exposure: &instance.ExposureConfig{
			Provider:  instance.ExposureProviderTailscale,
			Mode:      instance.ExposureModeServe,
			HTTPSPort: 443,
			Path:      instance.DefaultExposurePath,
		},
	}
	plan, err := (TailscaleManager{}).Plan(cfg, "machine.tailnet.ts.net.", true)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.URL != "https://machine.tailnet.ts.net" {
		t.Fatalf("URL = %q", plan.URL)
	}
	got := plan.Command.String()
	for _, want := range []string{"tailscale serve", "--bg", "--https=443", "--yes", "http://127.0.0.1:4096"} {
		if !strings.Contains(got, want) {
			t.Fatalf("command missing %q: %s", want, got)
		}
	}
	if plan.Off.String() != "tailscale serve --https=443 --yes off" {
		t.Fatalf("off command = %s", plan.Off.String())
	}
}

func TestTailscalePlanFunnelPathAndPort(t *testing.T) {
	cfg := instance.Config{
		Name: "default",
		Port: 4096,
		Exposure: &instance.ExposureConfig{
			Provider:  instance.ExposureProviderTailscale,
			Mode:      instance.ExposureModeFunnel,
			Public:    true,
			HTTPSPort: 8443,
			Path:      "/opencode",
		},
	}
	plan, err := (TailscaleManager{}).Plan(cfg, "machine.tailnet.ts.net", true)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.URL != "https://machine.tailnet.ts.net:8443/opencode" {
		t.Fatalf("URL = %q", plan.URL)
	}
	if got := plan.Command.String(); !strings.Contains(got, "tailscale funnel --bg --https=8443 --set-path=/opencode --yes http://127.0.0.1:4096") {
		t.Fatalf("command = %s", got)
	}
	if len(plan.Warnings) == 0 {
		t.Fatalf("expected public funnel warning")
	}
}

func TestResolveParsesTailscaleStatus(t *testing.T) {
	runner := &fakeRunner{output: `{"BackendState":"Running","Self":{"DNSName":"machine.tailnet.ts.net.","HostName":"machine"}}`}
	node, err := (TailscaleManager{Runner: runner}).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if node.DNSName != "machine.tailnet.ts.net" || node.HostName != "machine" {
		t.Fatalf("node = %#v", node)
	}
	if len(runner.args) != 1 || strings.Join(runner.args[0], " ") != "tailscale status --json" {
		t.Fatalf("args = %#v", runner.args)
	}
}
