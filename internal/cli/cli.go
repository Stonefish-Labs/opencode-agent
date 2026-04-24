package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Stonefish-Labs/opencode-agent/internal/exposure"
	"github.com/Stonefish-Labs/opencode-agent/internal/health"
	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
	"github.com/Stonefish-Labs/opencode-agent/internal/keychain"
	"github.com/Stonefish-Labs/opencode-agent/internal/projectconfig"
	"github.com/Stonefish-Labs/opencode-agent/internal/security"
	"github.com/Stonefish-Labs/opencode-agent/internal/service"
	"github.com/Stonefish-Labs/opencode-agent/internal/supervisor"
)

var Version = "dev"

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 2
	}
	var err error
	switch args[0] {
	case "install":
		err = install(args[1:], stdout, stderr)
	case "run":
		err = run(args[1:], stdout, stderr)
	case "list", "ps":
		err = list(args[1:], stdout, stderr)
	case "status", "inspect":
		err = status(args[1:], stdout, stderr)
	case "expose":
		err = expose(args[1:], stdout, stderr)
	case "start":
		err = serviceAction(args[1:], "start", stdout, stderr)
	case "stop":
		err = serviceAction(args[1:], "stop", stdout, stderr)
	case "restart":
		if err = serviceAction(args[1:], "stop", stdout, stderr); err == nil {
			err = serviceAction(args[1:], "start", stdout, stderr)
		}
	case "logs":
		err = logs(args[1:], stdout, stderr)
	case "show-password":
		err = showPassword(args[1:], stdout, stderr)
	case "rotate-password":
		err = rotatePassword(args[1:], stdout, stderr)
	case "uninstall":
		err = uninstall(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "opencode-agent %s\n", Version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `OpenCode Agent manages named local OpenCode server agents.

Usage:
  opencode-agent install [--name default] --workdir DIR [--port 4096] [--advertise-url https://...]
  opencode-agent list|ps [--json]
  opencode-agent status [name] [--json]
  opencode-agent expose tailscale [name] [--mode serve|funnel] [--public]
  opencode-agent expose status|off [name]
  opencode-agent start|stop|restart [name]
  opencode-agent logs [name] [--lines 120]
  opencode-agent show-password [name] --reveal
  opencode-agent rotate-password [name] [--reveal]
  opencode-agent uninstall [name] [--purge]

`)
}

func install(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", instance.DefaultName, "instance name")
	workdir := fs.String("workdir", mustGetwd(), "OpenCode working directory")
	port := fs.Int("port", instance.DefaultPort, "OpenCode HTTP port")
	binary := fs.String("binary", "", "opencode binary path")
	username := fs.String("username", instance.DefaultUsername, "Basic Auth username")
	password := fs.String("password", "", "Basic Auth password; generated when omitted")
	advertiseHost := fs.String("advertise-host", "", "Tailnet host or IP advertised to clients")
	advertiseURL := fs.String("advertise-url", "", "external HTTPS URL advertised to clients")
	bindHost := fs.String("bind-host", "", "host/IP passed to opencode serve --hostname")
	exposeProvider := fs.String("expose", "", "exposure provider: tailscale")
	tailscaleMode := fs.String("tailscale-mode", instance.ExposureModeServe, "Tailscale exposure mode: serve or funnel")
	tailscalePublic := fs.Bool("tailscale-public", false, "confirm public internet exposure for Tailscale Funnel")
	tailscaleHTTPSPort := fs.Int("tailscale-https-port", instance.DefaultExposureHTTPSPort, "Tailscale HTTPS listen port")
	tailscalePath := fs.String("tailscale-path", instance.DefaultExposurePath, "Tailscale Serve/Funnel URL path")
	tailscaleYes := fs.Bool("tailscale-yes", true, "pass --yes to tailscale to avoid interactive prompts")
	allowInsecureRemoteHTTP := fs.Bool("allow-insecure-remote-http", false, "allow non-loopback http:// advertised URLs")
	allowAllInterfacesFallback := fs.Bool("allow-all-interfaces-fallback", false, "allow Tailnet bind fallback to 0.0.0.0")
	environmentPolicy := fs.String("environment-policy", instance.DefaultEnvironmentPolicy, "child environment policy: filtered, minimal, or inherit")
	var allowedEnv stringListFlag
	fs.Var(&allowedEnv, "allow-env", "environment variable name to pass to opencode; repeatable")
	noProjectConfigSeed := fs.Bool("no-project-config-seed", false, "do not create a secure project opencode.json when missing")
	reveal := fs.Bool("reveal", false, "print the generated or provided password")
	dryRun := fs.Bool("dry-run", false, "print planned files and commands without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, plainPassword, passwordGenerated, err := buildInstallConfig(installConfigInput{
		Name:          *name,
		Workdir:       *workdir,
		Binary:        *binary,
		Username:      *username,
		PlainPassword: *password,
		AdvertiseHost: *advertiseHost,
		AdvertiseURL:  *advertiseURL,
		BindHost:      *bindHost,
		Exposure: buildExposureInput{
			Provider:  *exposeProvider,
			Mode:      *tailscaleMode,
			Public:    *tailscalePublic,
			HTTPSPort: *tailscaleHTTPSPort,
			Path:      *tailscalePath,
		},
		Port:                       *port,
		GeneratePassword:           !*dryRun,
		AllowInsecureRemoteHTTP:    *allowInsecureRemoteHTTP,
		AllowAllInterfacesFallback: *allowAllInterfacesFallback,
		EnvironmentPolicy:          *environmentPolicy,
		AllowedEnvironment:         allowedEnv,
	})
	if err != nil {
		return err
	}
	var exposurePlan *exposure.TailscalePlan
	if cfg.Exposure != nil {
		if *dryRun {
			plan, err := exposure.PlaceholderPlan(cfg, *tailscaleYes)
			if err != nil {
				return err
			}
			exposurePlan = &plan
		} else {
			plan, err := prepareTailscaleExposure(context.Background(), &cfg, *tailscaleYes)
			if err != nil {
				return err
			}
			exposurePlan = &plan
		}
	}
	projectReport, err := projectconfig.Prepare(cfg.WorkingDirectory, projectconfig.Options{DryRun: *dryRun, Seed: !*noProjectConfigSeed})
	if err != nil {
		return err
	}
	paths, err := instance.PathsFor(cfg.Name)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	plan, err := service.BuildPlan(runtime.GOOS, cfg.Name, service.InstalledExecutablePath())
	if err != nil {
		return err
	}
	if *dryRun {
		printInstallPlan(stdout, cfg, paths, passwordPlanText(passwordGenerated, *password != ""), projectReport, exe, service.InstalledExecutablePath(), plan, exposurePlan)
		return nil
	}
	if paths, err = instance.SaveConfig(cfg); err != nil {
		return err
	}
	if err := keychain.Store(cfg.Name, keychain.Credentials{Username: cfg.Username, Password: plainPassword}); err != nil {
		return err
	}
	if err := service.CopyExecutable(exe); err != nil {
		return err
	}
	if cfg.Exposure != nil {
		result, err := exposure.DefaultTailscaleManager().Apply(context.Background(), cfg, exposure.ApplyOptions{Yes: *tailscaleYes})
		if err != nil {
			return err
		}
		exposurePlan = &result.TailscalePlan
	}
	if err := service.Apply(plan); err != nil {
		if cfg.Exposure != nil {
			_, _, _ = exposure.DefaultTailscaleManager().Disable(context.Background(), cfg, *tailscaleYes)
		}
		return err
	}
	fmt.Fprintf(stdout, "Installed %s.\nURL: %s\nUsername: %s\n", cfg.Name, cfg.AdvertiseURL, cfg.Username)
	if *reveal {
		fmt.Fprintf(stdout, "Password: %s\n", plainPassword)
	} else {
		fmt.Fprintln(stdout, "Password: stored in the OS keychain (use `opencode-agent show-password --reveal` to print it).")
	}
	printProjectConfigSummary(stdout, projectReport)
	printExposureSummary(stdout, exposurePlan)
	printWarnings(stdout, health.BuildReport(cfg, paths, plan.ServiceName, service.PlannedState(plan)).Warnings)
	_ = paths
	return nil
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", instance.DefaultName, "instance name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, paths, err := instance.LoadConfig(*name)
	if err != nil {
		return err
	}
	creds, err := keychain.Load(cfg.Name)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return supervisor.Run(ctx, cfg, paths, creds, stdout)
}

func list(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	reports, err := reportsForAll()
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, reports)
	}
	printList(stdout, reports)
	return nil
}

func status(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := pickName(*nameFlag, fs.Args())
	cfg, paths, err := instance.LoadConfig(name)
	if err != nil {
		return err
	}
	report := reportFor(cfg, paths)
	if *jsonOutput {
		return writeJSON(stdout, report)
	}
	printStatus(stdout, report)
	return nil
}

func expose(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: opencode-agent expose tailscale|status|off [name]")
	}
	switch args[0] {
	case "tailscale":
		return exposeTailscale(args[1:], stdout, stderr)
	case "status":
		return exposeStatus(args[1:], stdout, stderr)
	case "off":
		return exposeOff(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown expose command %q", args[0])
	}
}

func exposeTailscale(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("expose tailscale", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	mode := fs.String("mode", instance.ExposureModeServe, "Tailscale exposure mode: serve or funnel")
	public := fs.Bool("public", false, "confirm public internet exposure for Tailscale Funnel")
	httpsPort := fs.Int("https-port", instance.DefaultExposureHTTPSPort, "Tailscale HTTPS listen port")
	path := fs.String("path", instance.DefaultExposurePath, "Tailscale Serve/Funnel URL path")
	yes := fs.Bool("yes", true, "pass --yes to tailscale to avoid interactive prompts")
	dryRun := fs.Bool("dry-run", false, "print planned config and commands without writing")
	restart := fs.Bool("restart", true, "restart the service if the bind host changes")
	split := splitInterspersedFlags(args, map[string]bool{
		"name":       true,
		"mode":       true,
		"https-port": true,
		"path":       true,
	})
	if err := fs.Parse(split.Flags); err != nil {
		return err
	}
	name := pickName(*nameFlag, split.Positionals)
	cfg, paths, err := instance.LoadConfig(name)
	if err != nil {
		return err
	}
	exposureConfig, err := buildExposureConfig(buildExposureInput{
		Provider:  instance.ExposureProviderTailscale,
		Mode:      *mode,
		Public:    *public,
		HTTPSPort: *httpsPort,
		Path:      *path,
	})
	if err != nil {
		return err
	}
	originalBind := cfg.BindHost
	cfg.Exposure = exposureConfig
	cfg.BindHost = "127.0.0.1"
	if *dryRun {
		plan, err := exposure.PlaceholderPlan(cfg, *yes)
		if err != nil {
			return err
		}
		printExposePlan(stdout, cfg, paths, &plan, originalBind != cfg.BindHost, *restart)
		return nil
	}
	plan, err := prepareTailscaleExposure(context.Background(), &cfg, *yes)
	if err != nil {
		return err
	}
	result, err := exposure.DefaultTailscaleManager().Apply(context.Background(), cfg, exposure.ApplyOptions{Yes: *yes})
	if err != nil {
		return err
	}
	if paths, err = instance.SaveConfig(cfg); err != nil {
		_, _, _ = exposure.DefaultTailscaleManager().Disable(context.Background(), cfg, *yes)
		return err
	}
	if originalBind != cfg.BindHost && *restart {
		if err := restartIfInstalled(cfg, paths, stdout); err != nil {
			return fmt.Errorf("tailscale exposure configured, but restart failed: %w", err)
		}
	}
	fmt.Fprintf(stdout, "Tailscale %s exposure configured for %s.\nURL: %s\n", cfg.Exposure.Mode, cfg.Name, result.URL)
	printExposureSummary(stdout, &plan)
	return nil
}

func exposeStatus(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("expose status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	jsonOutput := fs.Bool("json", false, "print JSON")
	split := splitInterspersedFlags(args, map[string]bool{"name": true})
	if err := fs.Parse(split.Flags); err != nil {
		return err
	}
	name := pickName(*nameFlag, split.Positionals)
	cfg, _, err := instance.LoadConfig(name)
	if err != nil {
		return err
	}
	status := map[string]any{
		"name":          cfg.Name,
		"advertise_url": cfg.AdvertiseURL,
		"exposure":      cfg.Exposure,
	}
	if cfg.Exposure != nil && cfg.Exposure.Provider == instance.ExposureProviderTailscale {
		output, command, err := exposure.DefaultTailscaleManager().Status(context.Background(), cfg.Exposure.Mode, *jsonOutput)
		status["tailscale_command"] = command.Args
		status["tailscale_output"] = output
		if err != nil {
			status["tailscale_error"] = err.Error()
		}
	}
	if *jsonOutput {
		return writeJSON(stdout, status)
	}
	if cfg.Exposure == nil {
		fmt.Fprintf(stdout, "Exposure: none\nURL: %s\n", cfg.AdvertiseURL)
		return nil
	}
	fmt.Fprintf(stdout, "Exposure: %s %s\nURL: %s\n", cfg.Exposure.Provider, cfg.Exposure.Mode, cfg.AdvertiseURL)
	if command, ok := status["tailscale_command"].([]string); ok && len(command) > 0 {
		fmt.Fprintf(stdout, "Tailscale status command: %s\n", exposure.Command{Args: command}.String())
	}
	if output, ok := status["tailscale_output"].(string); ok && output != "" {
		fmt.Fprintf(stdout, "\n%s\n", output)
	}
	if errText, ok := status["tailscale_error"].(string); ok && errText != "" {
		fmt.Fprintf(stdout, "Tailscale status error: %s\n", errText)
	}
	return nil
}

func exposeOff(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("expose off", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	yes := fs.Bool("yes", true, "pass --yes to tailscale to avoid interactive prompts")
	dryRun := fs.Bool("dry-run", false, "print planned config and commands without writing")
	split := splitInterspersedFlags(args, map[string]bool{"name": true})
	if err := fs.Parse(split.Flags); err != nil {
		return err
	}
	name := pickName(*nameFlag, split.Positionals)
	cfg, paths, err := instance.LoadConfig(name)
	if err != nil {
		return err
	}
	if cfg.Exposure == nil {
		fmt.Fprintf(stdout, "Exposure is already off for %s.\n", cfg.Name)
		return nil
	}
	if cfg.Exposure.Provider != instance.ExposureProviderTailscale {
		return fmt.Errorf("unsupported exposure provider %q", cfg.Exposure.Provider)
	}
	if *dryRun {
		plan, err := exposure.PlaceholderPlan(cfg, *yes)
		if err != nil {
			return err
		}
		printExposeOffPlan(stdout, cfg, paths, plan.Off)
		return nil
	}
	output, command, err := exposure.DefaultTailscaleManager().Disable(context.Background(), cfg, *yes)
	if err != nil {
		return err
	}
	cfg.Exposure = nil
	cfg.AdvertiseHost = "127.0.0.1"
	cfg.AdvertiseURL = instance.BaseURL(cfg.AdvertiseHost, cfg.Port)
	if paths, err = instance.SaveConfig(cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Exposure disabled for %s.\nURL: %s\nCommand: %s\n", cfg.Name, cfg.AdvertiseURL, command.String())
	if output != "" {
		fmt.Fprintf(stdout, "%s\n", output)
	}
	_ = paths
	return nil
}

func serviceAction(args []string, action string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := pickName(*nameFlag, fs.Args())
	cfg, paths, err := instance.LoadConfig(name)
	if err != nil {
		return err
	}
	plan, err := service.CurrentPlan(cfg.Name)
	if err != nil {
		return err
	}
	switch action {
	case "start":
		err = service.RunCommands(plan.StartCommands)
	case "stop":
		err = service.RunCommands(plan.StopCommands)
		supervisor.Stop(paths, 3*time.Second)
	default:
		err = fmt.Errorf("unsupported action %q", action)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s requested for %s (%s)\n", action, cfg.Name, cfg.AdvertiseURL)
	return nil
}

func logs(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	lines := fs.Int("lines", 120, "lines to print")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := pickName(*nameFlag, fs.Args())
	_, paths, err := instance.LoadConfig(name)
	if err != nil {
		return err
	}
	fmt.Fprint(stdout, instance.LogTail(paths, *lines))
	return nil
}

func showPassword(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("show-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	reveal := fs.Bool("reveal", false, "print the password")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := pickName(*nameFlag, fs.Args())
	cfg, _, err := instance.LoadConfig(name)
	if err != nil {
		return err
	}
	creds, err := keychain.Load(cfg.Name)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "URL: %s\nUsername: %s\n", cfg.AdvertiseURL, creds.Username)
	if *reveal {
		fmt.Fprintf(stdout, "Password: %s\n", creds.Password)
	} else {
		fmt.Fprintln(stdout, "Password: stored in the OS keychain (rerun with --reveal to print it).")
	}
	return nil
}

func rotatePassword(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("rotate-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	restart := fs.Bool("restart", true, "restart the service after rotating")
	reveal := fs.Bool("reveal", false, "print the new password")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := pickName(*nameFlag, fs.Args())
	cfg, _, err := instance.LoadConfig(name)
	if err != nil {
		return err
	}
	password, err := generatePassword()
	if err != nil {
		return err
	}
	oldCreds, _ := keychain.Load(cfg.Name)
	createdAt := oldCreds.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	if err := keychain.Store(cfg.Name, keychain.Credentials{Username: cfg.Username, Password: password, CreatedAt: createdAt, RotatedAt: time.Now()}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Password rotated for %s.\nURL: %s\nUsername: %s\n", cfg.Name, cfg.AdvertiseURL, cfg.Username)
	if *reveal {
		fmt.Fprintf(stdout, "Password: %s\n", password)
	} else {
		fmt.Fprintln(stdout, "Password: stored in the OS keychain (rerun with --reveal to print it).")
	}
	if *restart {
		_ = serviceAction([]string{cfg.Name}, "stop", io.Discard, stderr)
		if err := serviceAction([]string{cfg.Name}, "start", io.Discard, stderr); err != nil {
			return fmt.Errorf("password rotated, but restart failed: %w", err)
		}
		fmt.Fprintln(stdout, "Restart requested.")
	}
	return nil
}

func uninstall(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	purge := fs.Bool("purge", false, "remove config, state, and keychain password")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name := pickName(*nameFlag, fs.Args())
	cfg, paths, err := instance.LoadConfig(name)
	if err != nil {
		return err
	}
	plan, err := service.CurrentPlan(cfg.Name)
	if err != nil {
		return err
	}
	supervisor.Stop(paths, 3*time.Second)
	if err := service.Uninstall(plan); err != nil {
		return err
	}
	if *purge {
		_ = instance.RemoveConfig(cfg.Name)
		_ = os.RemoveAll(paths.StateDir)
		keychain.Delete(cfg.Name)
	}
	if configs, _, _ := instance.ListConfigs(); len(configs) == 0 {
		service.RemoveInstalledExecutable()
	}
	fmt.Fprintf(stdout, "Uninstalled %s.\n", cfg.Name)
	return nil
}

type installConfigInput struct {
	Name                       string
	Workdir                    string
	Binary                     string
	Username                   string
	PlainPassword              string
	AdvertiseHost              string
	AdvertiseURL               string
	BindHost                   string
	Exposure                   buildExposureInput
	Port                       int
	GeneratePassword           bool
	AllowInsecureRemoteHTTP    bool
	AllowAllInterfacesFallback bool
	EnvironmentPolicy          string
	AllowedEnvironment         []string
}

type buildExposureInput struct {
	Provider  string
	Mode      string
	Public    bool
	HTTPSPort int
	Path      string
}

func buildInstallConfig(input installConfigInput) (instance.Config, string, bool, error) {
	name, err := instance.NormalizeName(input.Name)
	if err != nil {
		return instance.Config{}, "", false, err
	}
	workdir := instance.ExpandPath(strings.TrimSpace(input.Workdir))
	if workdir == "" {
		return instance.Config{}, "", false, errors.New("workdir is required")
	}
	if abs, err := filepath.Abs(workdir); err == nil {
		workdir = abs
	}
	info, err := os.Stat(workdir)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("not a directory")
		}
		return instance.Config{}, "", false, fmt.Errorf("workdir %q is not usable: %w", workdir, err)
	}
	resolvedBinary, err := resolveOpenCodeBinary(input.Binary)
	if err != nil {
		return instance.Config{}, "", false, err
	}
	advertiseHost := strings.TrimSpace(input.AdvertiseHost)
	advertiseURL := strings.TrimSpace(input.AdvertiseURL)
	exposureConfig, err := buildExposureConfig(input.Exposure)
	if err != nil {
		return instance.Config{}, "", false, err
	}
	if exposureConfig != nil && (advertiseHost != "" || advertiseURL != "") {
		return instance.Config{}, "", false, errors.New("--advertise-host and --advertise-url cannot be used with --expose tailscale")
	}
	if advertiseHost != "" && advertiseURL != "" {
		return instance.Config{}, "", false, errors.New("--advertise-host and --advertise-url cannot be used together")
	}
	if advertiseURL != "" {
		parsed, err := url.Parse(advertiseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return instance.Config{}, "", false, fmt.Errorf("invalid advertise url %q", advertiseURL)
		}
		advertiseHost = parsed.Hostname()
	}
	if exposureConfig != nil {
		advertiseHost = "127.0.0.1"
		advertiseURL = instance.BaseURL(advertiseHost, input.Port)
	} else if advertiseHost == "" {
		advertiseHost = "127.0.0.1"
	}
	if advertiseURL == "" {
		advertiseURL = instance.BaseURL(advertiseHost, input.Port)
	}
	if instance.URLScheme(advertiseURL) == "http" && !instance.IsLoopbackURL(advertiseURL) && !input.AllowInsecureRemoteHTTP {
		return instance.Config{}, "", false, fmt.Errorf("refusing to advertise non-loopback HTTP URL %s; use --advertise-url https://... or pass --allow-insecure-remote-http", advertiseURL)
	}
	bindHost := strings.TrimSpace(input.BindHost)
	if bindHost == "" {
		if exposureConfig != nil {
			bindHost = "127.0.0.1"
		} else {
			bindHost = advertiseHost
		}
	}
	if exposureConfig != nil {
		if bindHost != "127.0.0.1" {
			return instance.Config{}, "", false, errors.New("--expose tailscale requires OpenCode to bind to 127.0.0.1")
		}
		bindHost = "127.0.0.1"
	}
	username := input.Username
	if strings.TrimSpace(username) == "" {
		username = instance.DefaultUsername
	}
	password := input.PlainPassword
	passwordGenerated := password == ""
	if password == "" {
		if input.GeneratePassword {
			password, err = generatePassword()
			if err != nil {
				return instance.Config{}, "", false, err
			}
		}
	}
	cfg, err := instance.NormalizeConfig(instance.Config{
		Name:                       name,
		OpenCodeBinary:             resolvedBinary,
		WorkingDirectory:           workdir,
		Port:                       input.Port,
		Username:                   username,
		BindHost:                   bindHost,
		AdvertiseHost:              advertiseHost,
		AdvertiseURL:               advertiseURL,
		AllowInsecureRemoteHTTP:    input.AllowInsecureRemoteHTTP,
		AllowAllInterfacesFallback: input.AllowAllInterfacesFallback,
		EnvironmentPolicy:          input.EnvironmentPolicy,
		AllowedEnvironment:         input.AllowedEnvironment,
		RestartDelaySecond:         instance.DefaultRestartDelay,
		HealthTimeoutSec:           instance.DefaultHealthTimeout,
		Exposure:                   exposureConfig,
	})
	return cfg, password, passwordGenerated, err
}

func buildExposureConfig(input buildExposureInput) (*instance.ExposureConfig, error) {
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider == "" {
		return nil, nil
	}
	if provider != instance.ExposureProviderTailscale {
		return nil, fmt.Errorf("unsupported exposure provider %q", input.Provider)
	}
	return instance.NormalizeExposureConfig(&instance.ExposureConfig{
		Provider:  provider,
		Mode:      input.Mode,
		Public:    input.Public,
		HTTPSPort: input.HTTPSPort,
		Path:      input.Path,
	})
}

func prepareTailscaleExposure(ctx context.Context, cfg *instance.Config, yes bool) (exposure.TailscalePlan, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	manager := exposure.DefaultTailscaleManager()
	node, err := manager.Resolve(ctx)
	if err != nil {
		return exposure.TailscalePlan{}, err
	}
	plan, err := manager.Plan(*cfg, node.DNSName, yes)
	if err != nil {
		return exposure.TailscalePlan{}, err
	}
	cfg.AdvertiseHost = plan.Host
	cfg.AdvertiseURL = plan.URL
	return plan, nil
}

func restartIfInstalled(cfg instance.Config, paths instance.Paths, stdout io.Writer) error {
	plan, err := service.CurrentPlan(cfg.Name)
	if err != nil {
		return nil
	}
	if service.State(plan) != "installed" {
		return nil
	}
	_ = service.RunCommands(plan.StopCommands)
	supervisor.Stop(paths, 3*time.Second)
	if err := service.RunCommands(plan.StartCommands); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Restart requested for %s.\n", cfg.Name)
	return nil
}

func reportsForAll() ([]health.Report, error) {
	configs, pathsList, err := instance.ListConfigs()
	if err != nil {
		return nil, err
	}
	reports := make([]health.Report, 0, len(configs))
	for i, cfg := range configs {
		reports = append(reports, reportFor(cfg, pathsList[i]))
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Name < reports[j].Name })
	return reports, nil
}

func reportFor(cfg instance.Config, paths instance.Paths) health.Report {
	plan, err := service.CurrentPlan(cfg.Name)
	serviceName := ""
	serviceState := "unknown"
	if err == nil {
		serviceName = plan.ServiceName
		serviceState = service.State(plan)
	}
	return health.BuildReport(cfg, paths, serviceName, serviceState)
}

func printList(w io.Writer, reports []health.Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATUS\tPID\tURL\tWORKDIR\tLOCAL\tTAILNET\tWARN\tSERVICE")
	for _, report := range reports {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%d\t%s\n",
			report.Name,
			statusWord(report),
			report.PID,
			report.AdvertiseURL,
			report.WorkingDir,
			okWord(report.LocalHealth.OK),
			okWord(report.TailnetHealth.OK),
			len(report.Warnings),
			report.ServiceState,
		)
	}
	_ = tw.Flush()
}

func printStatus(w io.Writer, report health.Report) {
	fmt.Fprintf(w, "Name: %s\n", report.Name)
	fmt.Fprintf(w, "URL: %s\n", report.AdvertiseURL)
	if report.Exposure != nil {
		fmt.Fprintf(w, "Exposure: %s %s", report.Exposure.Provider, report.Exposure.Mode)
		if report.Exposure.Public {
			fmt.Fprint(w, " (public)")
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Workdir: %s\n", report.WorkingDir)
	fmt.Fprintf(w, "Auth: %s", report.AuthMode)
	if !report.CredentialCreatedAt.IsZero() {
		fmt.Fprintf(w, " (credential age %dd", report.CredentialAgeDays)
		if !report.CredentialRotatedAt.IsZero() {
			fmt.Fprintf(w, ", rotated %s", report.CredentialRotatedAt.Format(time.RFC3339))
		}
		fmt.Fprint(w, ")")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Service: %s (%s)\n", report.ServiceName, report.ServiceState)
	fmt.Fprintf(w, "Process: %s\n", processText(report.ProcessAlive, report.PID))
	fmt.Fprintf(w, "Local health: %s\n", healthText(report.LocalHealth))
	fmt.Fprintf(w, "Tailnet health: %s\n", healthText(report.TailnetHealth))
	if report.LastExit != "" {
		fmt.Fprintf(w, "Last exit: %s\n", report.LastExit)
	}
	if report.LastError != "" {
		fmt.Fprintf(w, "Last error: %s\n", report.LastError)
	}
	printWarnings(w, report.Warnings)
}

func printInstallPlan(w io.Writer, cfg instance.Config, paths instance.Paths, passwordPlan string, projectReport projectconfig.Report, currentExe, installedExe string, plan service.Plan, exposurePlan *exposure.TailscalePlan) {
	fmt.Fprintf(w, "Config: %s\n%s\n", paths.ConfigPath, mustJSON(cfg))
	fmt.Fprintf(w, "Executable: copy %s -> %s\n", currentExe, installedExe)
	fmt.Fprintf(w, "Password: %s\n", passwordPlan)
	printProjectConfigSummary(w, projectReport)
	printWarnings(w, projectReport.Warnings)
	printExposureSummary(w, exposurePlan)
	if plan.UnitPath != "" {
		fmt.Fprintf(w, "\nUnit file: %s\n%s\n", plan.UnitPath, plan.UnitContent)
	}
	fmt.Fprintln(w, "\nCommands:")
	for _, command := range plan.InstallCommands {
		fmt.Fprintf(w, "  %s\n", strings.Join(command.Args, " "))
	}
}

func printExposureSummary(w io.Writer, plan *exposure.TailscalePlan) {
	if plan == nil {
		return
	}
	fmt.Fprintf(w, "\nExposure: %s %s\n", plan.Provider, plan.Mode)
	if plan.Public {
		fmt.Fprintln(w, "Exposure scope: public internet through Tailscale Funnel")
	} else {
		fmt.Fprintln(w, "Exposure scope: tailnet-only through Tailscale Serve")
	}
	fmt.Fprintf(w, "Exposure URL: %s\n", plan.URL)
	fmt.Fprintf(w, "Exposure target: %s\n", plan.Target)
	fmt.Fprintf(w, "Exposure command: %s\n", plan.Command.String())
	if len(plan.Warnings) > 0 {
		for _, warning := range plan.Warnings {
			fmt.Fprintf(w, "Exposure warning: %s\n", warning)
		}
	}
}

func printExposePlan(w io.Writer, cfg instance.Config, paths instance.Paths, plan *exposure.TailscalePlan, bindChanged bool, restart bool) {
	fmt.Fprintf(w, "Config: %s\n%s\n", paths.ConfigPath, mustJSON(cfg))
	printExposureSummary(w, plan)
	if bindChanged {
		fmt.Fprintln(w, "Bind host change: OpenCode will bind to 127.0.0.1 for Tailscale proxying.")
		if restart {
			fmt.Fprintln(w, "Service restart: planned because the bind host changes.")
		}
	}
}

func printExposeOffPlan(w io.Writer, cfg instance.Config, paths instance.Paths, command exposure.Command) {
	cfg.Exposure = nil
	cfg.AdvertiseHost = "127.0.0.1"
	cfg.AdvertiseURL = instance.BaseURL(cfg.AdvertiseHost, cfg.Port)
	fmt.Fprintf(w, "Config: %s\n%s\n", paths.ConfigPath, mustJSON(cfg))
	fmt.Fprintf(w, "\nExposure off command: %s\n", command.String())
}

func printProjectConfigSummary(w io.Writer, report projectconfig.Report) {
	switch {
	case report.Seeded:
		fmt.Fprintf(w, "Project config: created %s with ask-by-default OpenCode permissions.\n", report.SeedPath)
	case report.WouldSeed:
		fmt.Fprintf(w, "Project config: would create %s with ask-by-default OpenCode permissions.\n", report.SeedPath)
	default:
		for _, file := range report.Files {
			if file.Kind == "project" && filepath.Base(file.Path) == "opencode.json" && file.Exists {
				fmt.Fprintf(w, "Project config: preserving existing %s.\n", file.Path)
				return
			}
		}
		fmt.Fprintln(w, "Project config: secure seeding disabled or no changes needed.")
	}
}

func statusWord(report health.Report) string {
	if report.ProcessAlive && report.TailnetHealth.OK {
		return "running"
	}
	if report.ProcessAlive && report.LocalHealth.OK {
		return "local-only"
	}
	if report.ProcessAlive {
		return "unhealthy"
	}
	return "stopped"
}

func okWord(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}

func processText(ok bool, pid int) string {
	if ok {
		return fmt.Sprintf("alive pid=%d", pid)
	}
	if pid > 0 {
		return fmt.Sprintf("not alive (last pid=%d)", pid)
	}
	return "not running"
}

func healthText(result health.Result) string {
	if result.Skipped {
		if result.Warning != "" {
			return fmt.Sprintf("skipped %s: %s", result.URL, result.Warning)
		}
		return fmt.Sprintf("skipped %s", result.URL)
	}
	if result.OK {
		return fmt.Sprintf("ok %s (%dms)", result.URL, result.DurationMS)
	}
	if result.Detail == "" {
		result.Detail = "failed"
	}
	return fmt.Sprintf("failed %s: %s (%dms)", result.URL, result.Detail, result.DurationMS)
}

func passwordPlanText(generated bool, provided bool) string {
	if provided {
		return "[provided; not printed during dry-run]"
	}
	if generated {
		return "[generated at install time; not printed during dry-run]"
	}
	return "[not configured]"
}

func printWarnings(w io.Writer, warnings []security.Warning) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprintln(w, "\nWarnings:")
	for _, warning := range warnings {
		location := warning.Field
		if warning.Path != "" {
			location = warning.Path
			if warning.Field != "" {
				location += " " + warning.Field
			}
		}
		if location != "" {
			fmt.Fprintf(w, "  [%s] %s: %s\n", warning.Code, location, warning.Message)
		} else {
			fmt.Fprintf(w, "  [%s] %s\n", warning.Code, warning.Message)
		}
	}
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "=") {
		return fmt.Errorf("invalid environment variable name %q", value)
	}
	*f = append(*f, value)
	return nil
}

func pickName(flagName string, args []string) string {
	if strings.TrimSpace(flagName) != "" {
		return flagName
	}
	if len(args) > 0 {
		return args[0]
	}
	return instance.DefaultName
}

type splitArgs struct {
	Flags       []string
	Positionals []string
}

func splitInterspersedFlags(args []string, valueFlags map[string]bool) splitArgs {
	var split splitArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			split.Positionals = append(split.Positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			split.Flags = append(split.Flags, arg)
			name := normalizedFlagName(arg)
			if valueFlags[name] && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
				split.Flags = append(split.Flags, args[i])
			}
			continue
		}
		split.Positionals = append(split.Positionals, arg)
	}
	return split
}

func normalizedFlagName(arg string) string {
	arg = strings.TrimLeft(arg, "-")
	name, _, _ := strings.Cut(arg, "=")
	return name
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func resolveOpenCodeBinary(configured string) (string, error) {
	requested := strings.TrimSpace(configured)
	if requested == "" {
		requested = "opencode"
	}
	if looksLikePath(requested) {
		expanded := instance.ExpandPath(requested)
		if isExecutableFile(expanded) {
			return expanded, nil
		}
		return "", fmt.Errorf("opencode binary %q is not executable", requested)
	}
	names := []string{requested}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(requested), ".exe") {
		names = append(names, requested+".exe")
	}
	for _, name := range names {
		if resolved, err := exec.LookPath(name); err == nil {
			return resolved, nil
		}
	}
	for _, dir := range commonBinaryDirs() {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if isExecutableFile(candidate) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("%q was not found in PATH or common OpenCode install locations", requested)
}

func commonBinaryDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(home, ".opencode", "bin"), filepath.Join(home, ".local", "bin")}
	switch runtime.GOOS {
	case "darwin":
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin")
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			dirs = append(dirs, filepath.Join(localAppData, "Programs", "OpenCode"))
		}
	default:
		dirs = append(dirs, "/usr/local/bin", "/usr/bin")
	}
	return dirs
}

func looksLikePath(value string) bool {
	return filepath.IsAbs(value) || strings.Contains(value, "/") || strings.Contains(value, `\`)
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func mustJSON(value any) string {
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data)
}
