package exposure

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
)

const defaultTailscaleBinary = "tailscale"

type Command struct {
	Args []string `json:"args"`
}

func (c Command) String() string {
	parts := make([]string, 0, len(c.Args))
	for _, arg := range c.Args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

type Runner interface {
	Run(ctx context.Context, args []string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec G204 -- commands are generated internally from normalized exposure plans, not shell-expanded user input.
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	text := output.String()
	if err != nil {
		return text, fmt.Errorf("%s failed: %w\n%s", commandString(args), err, strings.TrimSpace(text))
	}
	return text, nil
}

type TailscaleManager struct {
	Binary string
	Runner Runner
}

type TailscaleNode struct {
	DNSName      string `json:"dns_name"`
	HostName     string `json:"host_name,omitempty"`
	BackendState string `json:"backend_state,omitempty"`
}

type TailscalePlan struct {
	Provider string   `json:"provider"`
	Mode     string   `json:"mode"`
	Public   bool     `json:"public"`
	URL      string   `json:"url"`
	Host     string   `json:"host"`
	Target   string   `json:"target"`
	Command  Command  `json:"command"`
	Off      Command  `json:"off_command"`
	Warnings []string `json:"warnings,omitempty"`
}

type ApplyOptions struct {
	Yes bool
}

type ApplyResult struct {
	TailscalePlan
	Output string `json:"output,omitempty"`
}

func DefaultTailscaleManager() TailscaleManager {
	return TailscaleManager{Binary: defaultTailscaleBinary, Runner: ExecRunner{}}
}

func (m TailscaleManager) Resolve(ctx context.Context) (TailscaleNode, error) {
	output, err := m.run(ctx, []string{m.binary(), "status", "--json"})
	if err != nil {
		return TailscaleNode{}, err
	}
	var status struct {
		BackendState string `json:"BackendState"`
		Self         *struct {
			DNSName  string `json:"DNSName"`
			HostName string `json:"HostName"`
		} `json:"Self"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		return TailscaleNode{}, fmt.Errorf("parse tailscale status --json: %w", err)
	}
	if status.BackendState != "" && !strings.EqualFold(status.BackendState, "Running") {
		return TailscaleNode{}, fmt.Errorf("tailscale is not running (state: %s)", status.BackendState)
	}
	if status.Self == nil {
		return TailscaleNode{}, errors.New("tailscale status did not include this node")
	}
	dnsName := strings.TrimSuffix(strings.TrimSpace(status.Self.DNSName), ".")
	if dnsName == "" {
		return TailscaleNode{}, errors.New("tailscale status did not include a DNS name; enable MagicDNS/HTTPS certificates for Serve")
	}
	return TailscaleNode{
		DNSName:      dnsName,
		HostName:     status.Self.HostName,
		BackendState: status.BackendState,
	}, nil
}

func (m TailscaleManager) Plan(cfg instance.Config, host string, yes bool) (TailscalePlan, error) {
	if cfg.Exposure == nil {
		return TailscalePlan{}, errors.New("instance does not have exposure configured")
	}
	if cfg.Exposure.Provider != instance.ExposureProviderTailscale {
		return TailscalePlan{}, fmt.Errorf("unsupported exposure provider %q", cfg.Exposure.Provider)
	}
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return TailscalePlan{}, errors.New("tailscale DNS name is required")
	}
	target := instance.BaseURL("127.0.0.1", cfg.Port)
	externalURL, err := TailscaleURL(host, *cfg.Exposure)
	if err != nil {
		return TailscalePlan{}, err
	}
	command := tailscaleCommand(m.binary(), *cfg.Exposure, target, yes, false)
	off := tailscaleCommand(m.binary(), *cfg.Exposure, "", yes, true)
	plan := TailscalePlan{
		Provider: cfg.Exposure.Provider,
		Mode:     cfg.Exposure.Mode,
		Public:   cfg.Exposure.Public,
		URL:      externalURL,
		Host:     host,
		Target:   target,
		Command:  command,
		Off:      off,
	}
	if cfg.Exposure.Mode == instance.ExposureModeFunnel {
		plan.Warnings = append(plan.Warnings, "Tailscale Funnel exposes this agent to the public internet; keep Basic Auth enabled and use tailnet policy controls.")
	}
	return plan, nil
}

func (m TailscaleManager) Apply(ctx context.Context, cfg instance.Config, opts ApplyOptions) (ApplyResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	node, err := m.Resolve(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	plan, err := m.Plan(cfg, node.DNSName, opts.Yes)
	if err != nil {
		return ApplyResult{}, err
	}
	output, err := m.run(ctx, plan.Command.Args)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{TailscalePlan: plan, Output: strings.TrimSpace(output)}, nil
}

func (m TailscaleManager) Disable(ctx context.Context, cfg instance.Config, yes bool) (string, Command, error) {
	if cfg.Exposure == nil {
		return "", Command{}, errors.New("instance does not have exposure configured")
	}
	if cfg.Exposure.Provider != instance.ExposureProviderTailscale {
		return "", Command{}, fmt.Errorf("unsupported exposure provider %q", cfg.Exposure.Provider)
	}
	command := tailscaleCommand(m.binary(), *cfg.Exposure, "", yes, true)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output, err := m.run(ctx, command.Args)
	return strings.TrimSpace(output), command, err
}

func (m TailscaleManager) Status(ctx context.Context, mode string, jsonOutput bool) (string, Command, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = instance.ExposureModeServe
	}
	if mode != instance.ExposureModeServe && mode != instance.ExposureModeFunnel {
		return "", Command{}, fmt.Errorf("unsupported tailscale exposure mode %q", mode)
	}
	args := []string{m.binary(), mode, "status"}
	if jsonOutput {
		args = append(args, "--json")
	}
	command := Command{Args: args}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := m.run(ctx, command.Args)
	return strings.TrimSpace(output), command, err
}

func TailscaleURL(host string, exposure instance.ExposureConfig) (string, error) {
	exposurePtr, err := instance.NormalizeExposureConfig(&exposure)
	if err != nil {
		return "", err
	}
	if exposurePtr == nil {
		return "", errors.New("exposure is not configured")
	}
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return "", errors.New("host is required")
	}
	urlHost := host
	if exposurePtr.HTTPSPort != 443 {
		urlHost = net.JoinHostPort(host, strconv.Itoa(exposurePtr.HTTPSPort))
	}
	path := exposurePtr.Path
	if path == instance.DefaultExposurePath {
		path = ""
	}
	return (&url.URL{Scheme: "https", Host: urlHost, Path: path}).String(), nil
}

func PlaceholderPlan(cfg instance.Config, yes bool) (TailscalePlan, error) {
	if cfg.Exposure == nil {
		return TailscalePlan{}, errors.New("instance does not have exposure configured")
	}
	target := instance.BaseURL("127.0.0.1", cfg.Port)
	command := tailscaleCommand(defaultTailscaleBinary, *cfg.Exposure, target, yes, false)
	off := tailscaleCommand(defaultTailscaleBinary, *cfg.Exposure, "", yes, true)
	placeholderURL := "https://<tailscale-dns-name>"
	if cfg.Exposure.HTTPSPort != 443 {
		placeholderURL += ":" + strconv.Itoa(cfg.Exposure.HTTPSPort)
	}
	if cfg.Exposure.Path != instance.DefaultExposurePath {
		placeholderURL += cfg.Exposure.Path
	}
	return TailscalePlan{
		Provider: cfg.Exposure.Provider,
		Mode:     cfg.Exposure.Mode,
		Public:   cfg.Exposure.Public,
		URL:      placeholderURL,
		Host:     "<tailscale-dns-name>",
		Target:   target,
		Command:  command,
		Off:      off,
	}, nil
}

func tailscaleCommand(binary string, exposure instance.ExposureConfig, target string, yes bool, off bool) Command {
	args := []string{binary, exposure.Mode}
	if !off {
		args = append(args, "--bg")
	}
	args = append(args, fmt.Sprintf("--https=%d", exposure.HTTPSPort))
	if exposure.Path != instance.DefaultExposurePath {
		args = append(args, "--set-path="+exposure.Path)
	}
	if yes {
		args = append(args, "--yes")
	}
	if target != "" {
		args = append(args, target)
	}
	if off {
		args = append(args, "off")
	}
	return Command{Args: args}
}

func (m TailscaleManager) run(ctx context.Context, args []string) (string, error) {
	return m.runner().Run(ctx, args)
}

func (m TailscaleManager) binary() string {
	if strings.TrimSpace(m.Binary) == "" {
		return defaultTailscaleBinary
	}
	return strings.TrimSpace(m.Binary)
}

func (m TailscaleManager) runner() Runner {
	if m.Runner == nil {
		return ExecRunner{}
	}
	return m.Runner
}

func commandString(args []string) string {
	return Command{Args: args}.String()
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\r\n'\"\\$`!*?[]{}()<>|&;") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
