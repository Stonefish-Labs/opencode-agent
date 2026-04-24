package service

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
)

type Command struct {
	Args        []string
	IgnoreError bool
}

type Plan struct {
	GOOS              string
	Instance          string
	ServiceName       string
	UnitPath          string
	UnitContent       string
	InstallCommands   []Command
	StartCommands     []Command
	StopCommands      []Command
	UninstallCommands []Command
}

func BuildPlan(goos string, name string, exePath string) (Plan, error) {
	name, err := instance.NormalizeName(name)
	if err != nil {
		return Plan{}, err
	}
	paths, err := instance.PathsFor(name)
	if err != nil {
		return Plan{}, err
	}
	args := []string{exePath, "run", "--name", name}
	home, _ := os.UserHomeDir()
	switch goos {
	case "darwin":
		label := "com.opencode.agent." + name
		plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		uid := strconv.Itoa(os.Getuid())
		target := "gui/" + uid + "/" + label
		return Plan{
			GOOS:        goos,
			Instance:    name,
			ServiceName: label,
			UnitPath:    plistPath,
			UnitContent: launchdPlist(label, args, paths.LogPath),
			InstallCommands: []Command{
				{Args: []string{"launchctl", "bootout", "gui/" + uid, plistPath}, IgnoreError: true},
				{Args: []string{"launchctl", "bootstrap", "gui/" + uid, plistPath}},
				{Args: []string{"launchctl", "enable", target}},
				{Args: []string{"launchctl", "kickstart", "-k", target}},
			},
			StartCommands: []Command{
				{Args: []string{"launchctl", "bootstrap", "gui/" + uid, plistPath}, IgnoreError: true},
				{Args: []string{"launchctl", "kickstart", "-k", target}},
			},
			StopCommands: []Command{
				{Args: []string{"launchctl", "bootout", "gui/" + uid, plistPath}, IgnoreError: true},
			},
			UninstallCommands: []Command{
				{Args: []string{"launchctl", "bootout", "gui/" + uid, plistPath}, IgnoreError: true},
			},
		}, nil
	case "linux":
		unitName := "opencode-agent-" + name + ".service"
		unitPath := filepath.Join(home, ".config", "systemd", "user", unitName)
		return Plan{
			GOOS:        goos,
			Instance:    name,
			ServiceName: unitName,
			UnitPath:    unitPath,
			UnitContent: systemdUnit(args),
			InstallCommands: []Command{
				{Args: []string{"systemctl", "--user", "daemon-reload"}},
				{Args: []string{"systemctl", "--user", "enable", "--now", unitName}},
			},
			StartCommands: []Command{
				{Args: []string{"systemctl", "--user", "start", unitName}},
			},
			StopCommands: []Command{
				{Args: []string{"systemctl", "--user", "stop", unitName}, IgnoreError: true},
			},
			UninstallCommands: []Command{
				{Args: []string{"systemctl", "--user", "disable", "--now", unitName}, IgnoreError: true},
				{Args: []string{"systemctl", "--user", "daemon-reload"}},
			},
		}, nil
	case "windows":
		taskName := "OpenCodeAgent-" + name
		taskCommand := windowsCommandLine(args)
		return Plan{
			GOOS:        goos,
			Instance:    name,
			ServiceName: taskName,
			InstallCommands: []Command{
				{Args: []string{"schtasks", "/Create", "/TN", taskName, "/TR", taskCommand, "/SC", "ONLOGON", "/RL", "LIMITED", "/F"}},
				{Args: []string{"schtasks", "/Run", "/TN", taskName}},
			},
			StartCommands: []Command{
				{Args: []string{"schtasks", "/Run", "/TN", taskName}},
			},
			StopCommands: []Command{
				{Args: []string{"schtasks", "/End", "/TN", taskName}, IgnoreError: true},
			},
			UninstallCommands: []Command{
				{Args: []string{"schtasks", "/Delete", "/TN", taskName, "/F"}, IgnoreError: true},
			},
		}, nil
	default:
		return Plan{}, fmt.Errorf("service installation is not supported on %s", goos)
	}
}

func CurrentPlan(name string) (Plan, error) {
	exe, err := os.Executable()
	if err != nil {
		return Plan{}, err
	}
	installed := InstalledExecutablePath()
	if samePath(exe, installed) {
		return BuildPlan(runtime.GOOS, name, exe)
	}
	return BuildPlan(runtime.GOOS, name, installed)
}

func Apply(plan Plan) error {
	if plan.UnitPath != "" {
		if err := os.MkdirAll(filepath.Dir(plan.UnitPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(plan.UnitPath, []byte(plan.UnitContent), 0o600); err != nil {
			return err
		}
	}
	return RunCommands(plan.InstallCommands)
}

func Uninstall(plan Plan) error {
	_ = RunCommands(plan.StopCommands)
	err := RunCommands(plan.UninstallCommands)
	if plan.UnitPath != "" {
		_ = os.Remove(plan.UnitPath)
	}
	return err
}

func RunCommands(commands []Command) error {
	for _, command := range commands {
		if len(command.Args) == 0 {
			continue
		}
		cmd := exec.Command(command.Args[0], command.Args[1:]...) // #nosec G204 -- commands are generated internally from normalized service plans, not shell-expanded user input.
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		if err != nil && !command.IgnoreError {
			return fmt.Errorf("%s failed: %w\n%s", strings.Join(command.Args, " "), err, strings.TrimSpace(output.String()))
		}
	}
	return nil
}

func InstalledExecutablePath() string {
	name := instance.AppName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(instance.StateRoot(), "bin", name)
}

func CopyExecutable(src string) error {
	dst := InstalledExecutablePath()
	if samePath(src, dst) {
		return nil
	}
	src, err := trustedExecutableSource(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src) // #nosec G304 -- src is resolved by trustedExecutableSource as an absolute regular executable.
	if err != nil {
		return err
	}
	if err := ensurePrivateDir(filepath.Dir(dst)); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755) // #nosec G302,G304 -- copied executable must be executable; destination is under a private 0700 state directory and O_EXCL avoids symlink overwrite.
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func RemoveInstalledExecutable() {
	_ = os.Remove(InstalledExecutablePath())
}

func State(plan Plan) string {
	switch plan.GOOS {
	case "darwin", "linux":
		if plan.UnitPath != "" {
			if _, err := os.Stat(plan.UnitPath); err == nil {
				return "installed"
			}
		}
		return "not-installed"
	case "windows":
		cmd := exec.Command("schtasks", "/Query", "/TN", plan.ServiceName) // #nosec G204 -- task name is derived from NormalizeName and passed as an argv element.
		if err := cmd.Run(); err == nil {
			return "installed"
		}
		return "not-installed"
	default:
		return "unknown"
	}
}

func PlannedState(plan Plan) string {
	switch plan.GOOS {
	case "darwin", "linux", "windows":
		return "installed"
	default:
		return "unknown"
	}
}

func launchdPlist(label string, args []string, logPath string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + label + `</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, arg := range args {
		b.WriteString("    <string>" + html.EscapeString(arg) + "</string>\n")
	}
	b.WriteString(`  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>` + html.EscapeString(logPath) + `</string>
  <key>StandardErrorPath</key>
  <string>` + html.EscapeString(logPath) + `</string>
</dict>
</plist>
`)
	return b.String()
}

func systemdUnit(args []string) string {
	return `[Unit]
Description=OpenCode Agent
After=network-online.target

[Service]
Type=simple
ExecStart=` + shellCommandLine(args) + `
Restart=always
RestartSec=3
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectSystem=full
ProtectControlGroups=true
ProtectKernelModules=true
ProtectKernelTunables=true
ProtectKernelLogs=true
ProtectClock=true
LockPersonality=true
RestrictRealtime=true
RestrictSUIDSGID=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallArchitectures=native
CapabilityBoundingSet=
AmbientCapabilities=

[Install]
WantedBy=default.target
`
}

func shellCommandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\"'\\") {
		return arg
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(arg, `\`, `\\`), `"`, `\"`) + `"`
}

func windowsCommandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windowsQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func windowsQuote(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\"") {
		return arg
	}
	return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil {
		left = leftAbs
	}
	if rightErr == nil {
		right = rightAbs
	}
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func trustedExecutableSource(src string) (string, error) {
	if src == "" {
		return "", fmt.Errorf("executable source is empty")
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return "", fmt.Errorf("executable source %q is not a regular file", resolved)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("executable source %q is not executable", resolved)
	}
	return resolved, nil
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return os.Chmod(path, 0o700) // #nosec G302 -- directories need execute bits; this is owner-only access.
	}
	return nil
}
