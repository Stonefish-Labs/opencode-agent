package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	AppName              = "opencode-agent"
	DefaultName          = "default"
	DefaultPort          = 4096
	DefaultUsername      = "opencode"
	DefaultRestartDelay  = 3
	DefaultHealthTimeout = 15
)

var validNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

type Config struct {
	Name               string `json:"name"`
	OpenCodeBinary     string `json:"opencode_binary"`
	WorkingDirectory   string `json:"working_directory"`
	Port               int    `json:"port"`
	Username           string `json:"username"`
	BindHost           string `json:"bind_host"`
	AdvertiseHost      string `json:"advertise_host"`
	AdvertiseURL       string `json:"advertise_url"`
	RestartDelaySecond int    `json:"restart_delay_seconds"`
	HealthTimeoutSec   int    `json:"health_timeout_seconds"`
}

type State struct {
	PID       int       `json:"pid"`
	BindHost  string    `json:"bind_host"`
	StartedAt time.Time `json:"started_at"`
	LastExit  string    `json:"last_exit,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

type Paths struct {
	Name       string
	ConfigDir  string
	StateDir   string
	ConfigPath string
	StatePath  string
	LogPath    string
	BinPath    string
}

func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = DefaultName
	}
	if !validNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid instance name %q; use letters, numbers, dot, underscore, or dash", name)
	}
	if strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid instance name %q", name)
	}
	return name, nil
}

func PathsFor(name string) (Paths, error) {
	name, err := NormalizeName(name)
	if err != nil {
		return Paths{}, err
	}
	stateDir := filepath.Join(StateRoot(), "instances", name)
	return Paths{
		Name:       name,
		ConfigDir:  filepath.Join(ConfigRoot(), "instances"),
		StateDir:   stateDir,
		ConfigPath: filepath.Join(ConfigRoot(), "instances", name+".json"),
		StatePath:  filepath.Join(stateDir, "state.json"),
		LogPath:    filepath.Join(stateDir, "agent.log"),
		BinPath:    filepath.Join(StateRoot(), "bin", executableName()),
	}, nil
}

func NormalizeConfig(cfg Config) (Config, error) {
	name, err := NormalizeName(cfg.Name)
	if err != nil {
		return Config{}, err
	}
	cfg.Name = name
	cfg.OpenCodeBinary = ExpandPath(strings.TrimSpace(cfg.OpenCodeBinary))
	if cfg.OpenCodeBinary == "" {
		cfg.OpenCodeBinary = "opencode"
	}
	cfg.WorkingDirectory = ExpandPath(strings.TrimSpace(cfg.WorkingDirectory))
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return Config{}, errors.New("port must be between 1 and 65535")
	}
	if strings.TrimSpace(cfg.Username) == "" {
		cfg.Username = DefaultUsername
	}
	cfg.AdvertiseHost = strings.TrimSpace(cfg.AdvertiseHost)
	if cfg.AdvertiseHost == "" {
		cfg.AdvertiseHost = HostFromURL(cfg.AdvertiseURL)
	}
	if cfg.AdvertiseHost == "" {
		cfg.AdvertiseHost = "127.0.0.1"
	}
	cfg.AdvertiseURL = strings.TrimSpace(cfg.AdvertiseURL)
	if cfg.AdvertiseURL == "" {
		cfg.AdvertiseURL = BaseURL(cfg.AdvertiseHost, cfg.Port)
	}
	if strings.TrimSpace(cfg.BindHost) == "" {
		cfg.BindHost = cfg.AdvertiseHost
	}
	if cfg.RestartDelaySecond <= 0 {
		cfg.RestartDelaySecond = DefaultRestartDelay
	}
	if cfg.HealthTimeoutSec <= 0 {
		cfg.HealthTimeoutSec = DefaultHealthTimeout
	}
	return cfg, nil
}

func LoadConfig(name string) (Config, Paths, error) {
	paths, err := PathsFor(name)
	if err != nil {
		return Config{}, Paths{}, err
	}
	data, err := os.ReadFile(paths.ConfigPath)
	if err != nil {
		return Config{}, paths, fmt.Errorf("read config %s: %w", paths.ConfigPath, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, paths, fmt.Errorf("parse config %s: %w", paths.ConfigPath, err)
	}
	cfg.Name = paths.Name
	cfg, err = NormalizeConfig(cfg)
	return cfg, paths, err
}

func SaveConfig(cfg Config) (Paths, error) {
	cfg, err := NormalizeConfig(cfg)
	if err != nil {
		return Paths{}, err
	}
	paths, err := PathsFor(cfg.Name)
	if err != nil {
		return Paths{}, err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Paths{}, err
	}
	if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
		return Paths{}, err
	}
	tmp := paths.ConfigPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return Paths{}, err
	}
	return paths, os.Rename(tmp, paths.ConfigPath)
}

func RemoveConfig(name string) error {
	paths, err := PathsFor(name)
	if err != nil {
		return err
	}
	return os.Remove(paths.ConfigPath)
}

func LoadState(paths Paths) State {
	data, err := os.ReadFile(paths.StatePath)
	if err != nil {
		return State{}
	}
	var state State
	_ = json.Unmarshal(data, &state)
	return state
}

func SaveState(paths Paths, state State) {
	_ = os.MkdirAll(paths.StateDir, 0o700)
	data, _ := json.MarshalIndent(state, "", "  ")
	_ = os.WriteFile(paths.StatePath, data, 0o600)
}

func ClearState(paths Paths) {
	_ = os.Remove(paths.StatePath)
}

func ListConfigs() ([]Config, []Paths, error) {
	dir := filepath.Join(ConfigRoot(), "instances")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	configs := []Config{}
	pathsList := []Paths{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		cfg, paths, err := LoadConfig(name)
		if err != nil {
			continue
		}
		configs = append(configs, cfg)
		pathsList = append(pathsList, paths)
	}
	return configs, pathsList, nil
}

func ConfigRoot() string {
	if override := os.Getenv("OPENCODE_AGENT_CONFIG_DIR"); override != "" {
		return override
	}
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, AppName)
		}
	}
	return filepath.Join(homeDir(), ".config", AppName)
}

func StateRoot() string {
	if override := os.Getenv("OPENCODE_AGENT_STATE_DIR"); override != "" {
		return override
	}
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, AppName)
		}
	}
	return filepath.Join(homeDir(), ".local", "state", AppName)
}

func LogTail(path string, lines int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	parts := strings.Split(string(data), "\n")
	if lines > 0 && len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

func DetectTailscaleIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if IsTailscaleIPv4(ip.String()) {
			return ip.String()
		}
	}
	return ""
}

func IsTailscaleIPv4(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value)).To4()
	return ip != nil && ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127
}

func BaseURL(host string, port int) string {
	return "http://" + net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
}

func HostFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func HealthURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("invalid base url")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		parsed.Path = "/global/health"
	} else {
		parsed.Path = path + "/global/health"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func LocalBaseURLForBind(bindHost string, port int) string {
	host := strings.TrimSpace(bindHost)
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return BaseURL(host, port)
}

func ExpandPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir(), strings.TrimPrefix(path, "~/"))
	}
	return path
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return home
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return AppName + ".exe"
	}
	return AppName
}
