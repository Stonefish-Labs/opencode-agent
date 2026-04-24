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
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Stonefish-Labs/opencode-agent/internal/health"
	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
	"github.com/Stonefish-Labs/opencode-agent/internal/keychain"
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
  opencode-agent install [--name default] --workdir DIR [--port 4096]
  opencode-agent list|ps [--json]
  opencode-agent status [name] [--json]
  opencode-agent start|stop|restart [name]
  opencode-agent logs [name] [--lines 120]
  opencode-agent show-password [name]
  opencode-agent rotate-password [name]
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
	bindHost := fs.String("bind-host", "", "host/IP passed to opencode serve --hostname")
	dryRun := fs.Bool("dry-run", false, "print planned files and commands without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, plainPassword, err := buildInstallConfig(*name, *workdir, *binary, *username, *password, *advertiseHost, *bindHost, *port)
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
		printInstallPlan(stdout, cfg, paths, plainPassword, exe, service.InstalledExecutablePath(), plan)
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
	if err := service.Apply(plan); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Installed %s.\nURL: %s\nUsername: %s\nPassword: %s\n", cfg.Name, cfg.AdvertiseURL, cfg.Username, plainPassword)
	fmt.Fprintf(stdout, "\nPassword is stored in the OS keychain. Use `opencode-agent show-password %s` to show it again.\n", cfg.Name)
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
	fmt.Fprint(stdout, instance.LogTail(paths.LogPath, *lines))
	return nil
}

func showPassword(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("show-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
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
	fmt.Fprintf(stdout, "URL: %s\nUsername: %s\nPassword: %s\n", cfg.AdvertiseURL, creds.Username, creds.Password)
	return nil
}

func rotatePassword(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("rotate-password", flag.ContinueOnError)
	fs.SetOutput(stderr)
	nameFlag := fs.String("name", "", "instance name")
	restart := fs.Bool("restart", true, "restart the service after rotating")
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
	if err := keychain.Store(cfg.Name, keychain.Credentials{Username: cfg.Username, Password: password}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Password rotated for %s.\nURL: %s\nUsername: %s\nPassword: %s\n", cfg.Name, cfg.AdvertiseURL, cfg.Username, password)
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

func buildInstallConfig(name, workdir, binary, username, password, advertiseHost, bindHost string, port int) (instance.Config, string, error) {
	name, err := instance.NormalizeName(name)
	if err != nil {
		return instance.Config{}, "", err
	}
	workdir = instance.ExpandPath(strings.TrimSpace(workdir))
	if workdir == "" {
		return instance.Config{}, "", errors.New("workdir is required")
	}
	if abs, err := filepath.Abs(workdir); err == nil {
		workdir = abs
	}
	info, err := os.Stat(workdir)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("not a directory")
		}
		return instance.Config{}, "", fmt.Errorf("workdir %q is not usable: %w", workdir, err)
	}
	resolvedBinary, err := resolveOpenCodeBinary(binary)
	if err != nil {
		return instance.Config{}, "", err
	}
	advertiseHost = strings.TrimSpace(advertiseHost)
	if advertiseHost == "" {
		advertiseHost = instance.DetectTailscaleIPv4()
	}
	if advertiseHost == "" {
		advertiseHost = "127.0.0.1"
	}
	if strings.TrimSpace(bindHost) == "" {
		bindHost = advertiseHost
	}
	if strings.TrimSpace(username) == "" {
		username = instance.DefaultUsername
	}
	if password == "" {
		password, err = generatePassword()
		if err != nil {
			return instance.Config{}, "", err
		}
	}
	cfg, err := instance.NormalizeConfig(instance.Config{
		Name:               name,
		OpenCodeBinary:     resolvedBinary,
		WorkingDirectory:   workdir,
		Port:               port,
		Username:           username,
		BindHost:           bindHost,
		AdvertiseHost:      advertiseHost,
		AdvertiseURL:       instance.BaseURL(advertiseHost, port),
		RestartDelaySecond: instance.DefaultRestartDelay,
		HealthTimeoutSec:   instance.DefaultHealthTimeout,
	})
	return cfg, password, err
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
	fmt.Fprintln(tw, "NAME\tSTATUS\tPID\tURL\tWORKDIR\tLOCAL\tTAILNET\tSERVICE")
	for _, report := range reports {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			report.Name,
			statusWord(report),
			report.PID,
			report.AdvertiseURL,
			report.WorkingDir,
			okWord(report.LocalHealth.OK),
			okWord(report.TailnetHealth.OK),
			report.ServiceState,
		)
	}
	_ = tw.Flush()
}

func printStatus(w io.Writer, report health.Report) {
	fmt.Fprintf(w, "Name: %s\n", report.Name)
	fmt.Fprintf(w, "URL: %s\n", report.AdvertiseURL)
	fmt.Fprintf(w, "Workdir: %s\n", report.WorkingDir)
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
}

func printInstallPlan(w io.Writer, cfg instance.Config, paths instance.Paths, password, currentExe, installedExe string, plan service.Plan) {
	fmt.Fprintf(w, "Config: %s\n%s\n", paths.ConfigPath, mustJSON(cfg))
	fmt.Fprintf(w, "Executable: copy %s -> %s\n", currentExe, installedExe)
	fmt.Fprintf(w, "Generated password: %s\n", password)
	if plan.UnitPath != "" {
		fmt.Fprintf(w, "\nUnit file: %s\n%s\n", plan.UnitPath, plan.UnitContent)
	}
	fmt.Fprintln(w, "\nCommands:")
	for _, command := range plan.InstallCommands {
		fmt.Fprintf(w, "  %s\n", strings.Join(command.Args, " "))
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
	if result.OK {
		return fmt.Sprintf("ok %s (%dms)", result.URL, result.DurationMS)
	}
	if result.Detail == "" {
		result.Detail = "failed"
	}
	return fmt.Sprintf("failed %s: %s (%dms)", result.URL, result.Detail, result.DurationMS)
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
