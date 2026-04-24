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
	AppName                  = "opencode-agent"
	DefaultName              = "default"
	DefaultPort              = 4096
	DefaultUsername          = "opencode"
	DefaultEnvironmentPolicy = "filtered"
	DefaultRestartDelay      = 3
	DefaultHealthTimeout     = 15
	DefaultExposureHTTPSPort = 443
	DefaultExposurePath      = "/"

	ExposureProviderTailscale = "tailscale"
	ExposureModeServe         = "serve"
	ExposureModeFunnel        = "funnel"
)

var validNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

type Config struct {
	Name                       string          `json:"name"`
	OpenCodeBinary             string          `json:"opencode_binary"`
	WorkingDirectory           string          `json:"working_directory"`
	Port                       int             `json:"port"`
	Username                   string          `json:"username"`
	BindHost                   string          `json:"bind_host"`
	AdvertiseHost              string          `json:"advertise_host"`
	AdvertiseURL               string          `json:"advertise_url"`
	AllowInsecureRemoteHTTP    bool            `json:"allow_insecure_remote_http,omitempty"`
	AllowAllInterfacesFallback bool            `json:"allow_all_interfaces_fallback,omitempty"`
	EnvironmentPolicy          string          `json:"environment_policy,omitempty"`
	AllowedEnvironment         []string        `json:"allowed_environment,omitempty"`
	RestartDelaySecond         int             `json:"restart_delay_seconds"`
	HealthTimeoutSec           int             `json:"health_timeout_seconds"`
	Exposure                   *ExposureConfig `json:"exposure,omitempty"`
}

type ExposureConfig struct {
	Provider  string `json:"provider"`
	Mode      string `json:"mode,omitempty"`
	Public    bool   `json:"public,omitempty"`
	HTTPSPort int    `json:"https_port,omitempty"`
	Path      string `json:"path,omitempty"`
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
	cfg.EnvironmentPolicy = strings.TrimSpace(cfg.EnvironmentPolicy)
	if cfg.EnvironmentPolicy == "" {
		cfg.EnvironmentPolicy = DefaultEnvironmentPolicy
	}
	switch cfg.EnvironmentPolicy {
	case "filtered", "minimal", "inherit":
	default:
		return Config{}, fmt.Errorf("invalid environment policy %q", cfg.EnvironmentPolicy)
	}
	cfg.AllowedEnvironment = normalizeEnvNames(cfg.AllowedEnvironment)
	exposure, err := NormalizeExposureConfig(cfg.Exposure)
	if err != nil {
		return Config{}, err
	}
	cfg.Exposure = exposure
	if cfg.RestartDelaySecond <= 0 {
		cfg.RestartDelaySecond = DefaultRestartDelay
	}
	if cfg.HealthTimeoutSec <= 0 {
		cfg.HealthTimeoutSec = DefaultHealthTimeout
	}
	return cfg, nil
}

func NormalizeExposureConfig(exposure *ExposureConfig) (*ExposureConfig, error) {
	if exposure == nil {
		return nil, nil
	}
	normalized := *exposure
	normalized.Provider = strings.ToLower(strings.TrimSpace(normalized.Provider))
	if normalized.Provider == "" {
		return nil, nil
	}
	if normalized.Provider != ExposureProviderTailscale {
		return nil, fmt.Errorf("unsupported exposure provider %q", normalized.Provider)
	}
	normalized.Mode = strings.ToLower(strings.TrimSpace(normalized.Mode))
	if normalized.Mode == "" {
		normalized.Mode = ExposureModeServe
	}
	switch normalized.Mode {
	case ExposureModeServe:
		normalized.Public = false
	case ExposureModeFunnel:
		if !normalized.Public {
			return nil, errors.New("tailscale funnel exposure requires public=true")
		}
	default:
		return nil, fmt.Errorf("unsupported tailscale exposure mode %q", normalized.Mode)
	}
	if normalized.HTTPSPort == 0 {
		normalized.HTTPSPort = DefaultExposureHTTPSPort
	}
	if normalized.HTTPSPort < 1 || normalized.HTTPSPort > 65535 {
		return nil, errors.New("exposure https port must be between 1 and 65535")
	}
	if normalized.Mode == ExposureModeFunnel && normalized.HTTPSPort != 443 && normalized.HTTPSPort != 8443 && normalized.HTTPSPort != 10000 {
		return nil, errors.New("tailscale funnel https port must be 443, 8443, or 10000")
	}
	normalized.Path = normalizeExposurePath(normalized.Path)
	if strings.ContainsAny(normalized.Path, " \t\r\n?#") {
		return nil, fmt.Errorf("invalid exposure path %q", normalized.Path)
	}
	return &normalized, nil
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

func LogTail(paths Paths, lines int) string {
	var combined strings.Builder
	for _, path := range LogFiles(paths) {
		data, err := os.ReadFile(path) // #nosec G304 -- log paths are derived from normalized instance Paths, not arbitrary user input.
		if err != nil {
			continue
		}
		combined.Write(data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			combined.WriteByte('\n')
		}
	}
	parts := strings.Split(combined.String(), "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if lines > 0 && len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

func LogFiles(paths Paths) []string {
	files := make([]string, 0, 6)
	for i := 5; i >= 1; i-- {
		files = append(files, fmt.Sprintf("%s.%d", paths.LogPath, i))
	}
	files = append(files, paths.LogPath)
	return files
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

func IsLoopbackHost(value string) bool {
	host := strings.TrimSpace(value)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func IsAllInterfacesHost(value string) bool {
	host := strings.TrimSpace(value)
	return host == "0.0.0.0" || host == "::" || host == "[::]"
}

func IsLoopbackURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return IsLoopbackHost(parsed.Hostname())
}

func URLScheme(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Scheme)
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

func normalizeEnvNames(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func normalizeExposurePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultExposurePath
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	if len(value) > 1 {
		value = strings.TrimRight(value, "/")
	}
	return value
}
