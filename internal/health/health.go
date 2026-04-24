package health

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
	"github.com/Stonefish-Labs/opencode-agent/internal/keychain"
	"github.com/Stonefish-Labs/opencode-agent/internal/process"
	"github.com/Stonefish-Labs/opencode-agent/internal/projectconfig"
	"github.com/Stonefish-Labs/opencode-agent/internal/security"
)

type Result struct {
	URL             string `json:"url"`
	OK              bool   `json:"ok"`
	Detail          string `json:"detail,omitempty"`
	Warning         string `json:"warning,omitempty"`
	Skipped         bool   `json:"skipped,omitempty"`
	StatusCode      int    `json:"status_code,omitempty"`
	DurationMS      int64  `json:"duration_ms"`
	RequiresAuth    bool   `json:"requires_auth"`
	CredentialsSent bool   `json:"credentials_sent,omitempty"`
}

type CheckOptions struct {
	Credentials             *keychain.Credentials
	AllowCredentials        bool
	AllowInsecureRemoteHTTP bool
	ExpectedHost            string
}

type Report struct {
	Name                string                   `json:"name"`
	ConfigPath          string                   `json:"config_path"`
	StatePath           string                   `json:"state_path"`
	LogPath             string                   `json:"log_path"`
	ServiceName         string                   `json:"service_name"`
	ServiceState        string                   `json:"service_state"`
	Username            string                   `json:"username"`
	PasswordStored      bool                     `json:"password_stored"`
	AuthMode            string                   `json:"auth_mode"`
	CredentialCreatedAt time.Time                `json:"credential_created_at,omitempty"`
	CredentialRotatedAt time.Time                `json:"credential_rotated_at,omitempty"`
	CredentialAgeDays   int                      `json:"credential_age_days,omitempty"`
	AdvertiseURL        string                   `json:"advertise_url"`
	BindURL             string                   `json:"bind_url"`
	Exposure            *instance.ExposureConfig `json:"exposure,omitempty"`
	WorkingDir          string                   `json:"working_directory"`
	ProjectConfig       projectconfig.Report     `json:"project_config"`
	PID                 int                      `json:"pid"`
	ProcessAlive        bool                     `json:"process_alive"`
	RuntimeBind         string                   `json:"runtime_bind_host,omitempty"`
	LocalHealth         Result                   `json:"local_health"`
	TailnetHealth       Result                   `json:"tailnet_health"`
	Warnings            []security.Warning       `json:"warnings,omitempty"`
	LastExit            string                   `json:"last_exit,omitempty"`
	LastError           string                   `json:"last_error,omitempty"`
	CheckedAt           time.Time                `json:"checked_at"`
}

func Check(baseURL string, creds *keychain.Credentials, timeout time.Duration) Result {
	return CheckWithOptions(baseURL, timeout, CheckOptions{
		Credentials:             creds,
		AllowCredentials:        creds != nil,
		AllowInsecureRemoteHTTP: true,
	})
}

func CheckWithOptions(baseURL string, timeout time.Duration, opts CheckOptions) Result {
	target, err := instance.HealthURL(baseURL)
	result := Result{URL: target, RequiresAuth: opts.Credentials != nil}
	if err != nil {
		result.Detail = "invalid url: " + err.Error()
		return result
	}
	parsed, err := url.Parse(target)
	if err != nil {
		result.Detail = "invalid url: " + err.Error()
		return result
	}
	if opts.ExpectedHost != "" && !sameHost(parsed.Hostname(), opts.ExpectedHost) {
		result.Skipped = true
		result.Warning = fmt.Sprintf("health URL host %q does not match expected host %q", parsed.Hostname(), opts.ExpectedHost)
		result.Detail = "skipped: host mismatch"
		return result
	}
	sendCreds := opts.Credentials != nil && opts.AllowCredentials
	if sendCreds && parsed.Scheme == "http" && !instance.IsLoopbackHost(parsed.Hostname()) && !opts.AllowInsecureRemoteHTTP {
		result.Skipped = true
		result.Warning = "skipped credentialed health check for non-loopback HTTP URL"
		result.Detail = "skipped: insecure remote http"
		return result
	}
	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		result.Detail = "request build: " + err.Error()
		return result
	}
	req.Close = true
	if sendCreds {
		req.SetBasicAuth(opts.Credentials.Username, opts.Credentials.Password)
		result.CredentialsSent = true
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: timeout / 2, KeepAlive: 0}).DialContext,
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: timeout,
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Do(req) // #nosec G704 -- health URLs come from instance config, reject host mismatches, disable proxies/redirects, and skip credentialed remote HTTP by default.
	result.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Detail = "transport: " + err.Error()
		return result
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.OK = true
		return result
	}
	result.Detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
	return result
}

func BuildReport(cfg instance.Config, paths instance.Paths, serviceName string, serviceState string) Report {
	state := instance.LoadState(paths)
	creds, credErr := keychain.Load(cfg.Name)
	passwordStored := credErr == nil
	var credsPtr *keychain.Credentials
	if credErr == nil {
		credsPtr = &creds
	}
	warnings := reportWarnings(cfg, passwordStored)
	projectReport := projectconfig.Audit(cfg.WorkingDirectory)
	warnings = append(warnings, projectReport.Warnings...)
	runtimeBind := state.BindHost
	if runtimeBind == "" {
		runtimeBind = cfg.BindHost
	}
	localBase := instance.LocalBaseURLForBind(runtimeBind, cfg.Port)
	now := time.Now()
	ageDays := 0
	if !creds.CreatedAt.IsZero() {
		ageDays = int(now.Sub(creds.CreatedAt).Hours() / 24)
	}
	return Report{
		Name:                cfg.Name,
		ConfigPath:          paths.ConfigPath,
		StatePath:           paths.StatePath,
		LogPath:             paths.LogPath,
		ServiceName:         serviceName,
		ServiceState:        serviceState,
		Username:            cfg.Username,
		PasswordStored:      passwordStored,
		AuthMode:            "shared-basic",
		CredentialCreatedAt: creds.CreatedAt,
		CredentialRotatedAt: creds.RotatedAt,
		CredentialAgeDays:   ageDays,
		AdvertiseURL:        cfg.AdvertiseURL,
		BindURL:             instance.BaseURL(runtimeBind, cfg.Port),
		Exposure:            cfg.Exposure,
		WorkingDir:          cfg.WorkingDirectory,
		ProjectConfig:       projectReport,
		PID:                 state.PID,
		ProcessAlive:        process.Default.Alive(state.PID),
		RuntimeBind:         state.BindHost,
		LocalHealth:         CheckWithOptions(localBase, 3*time.Second, CheckOptions{Credentials: credsPtr, AllowCredentials: credsPtr != nil, AllowInsecureRemoteHTTP: true}),
		TailnetHealth:       CheckWithOptions(cfg.AdvertiseURL, 5*time.Second, CheckOptions{Credentials: credsPtr, AllowCredentials: credsPtr != nil, AllowInsecureRemoteHTTP: cfg.AllowInsecureRemoteHTTP, ExpectedHost: expectedAdvertiseHost(cfg)}),
		Warnings:            warnings,
		LastExit:            state.LastExit,
		LastError:           state.LastError,
		CheckedAt:           now,
	}
}

func reportWarnings(cfg instance.Config, passwordStored bool) []security.Warning {
	warnings := []security.Warning{
		security.Warn("auth.shared_basic", "auth_mode", "OpenCode currently uses one shared Basic Auth credential per instance; use an HTTPS identity-aware proxy for per-client identity and rate limits."),
	}
	if !passwordStored {
		warnings = append(warnings, security.Warn("auth.password_missing", "password_stored", "No Basic Auth password was found in the OS keychain."))
	}
	if instance.URLScheme(cfg.AdvertiseURL) == "http" && !instance.IsLoopbackURL(cfg.AdvertiseURL) && !cfg.AllowInsecureRemoteHTTP {
		warnings = append(warnings, security.Warn("network.insecure_remote_http", "advertise_url", "Non-loopback HTTP is not safe for remote access; advertise an HTTPS proxy/tunnel URL or pass --allow-insecure-remote-http explicitly."))
	}
	if instance.IsAllInterfacesHost(cfg.BindHost) {
		warnings = append(warnings, security.Warn("network.all_interfaces_bind", "bind_host", "The service is configured to bind all interfaces. Prefer 127.0.0.1 plus HTTPS proxy/tunnel unless broad exposure is intentional."))
	}
	if cfg.AllowAllInterfacesFallback {
		warnings = append(warnings, security.Warn("network.all_interfaces_fallback", "allow_all_interfaces_fallback", "All-interface fallback is enabled; bind failures may broaden exposure to every local interface."))
	}
	if cfg.Exposure != nil && cfg.Exposure.Provider == instance.ExposureProviderTailscale && cfg.Exposure.Mode == instance.ExposureModeFunnel {
		warnings = append(warnings, security.Warn("network.public_tailscale_funnel", "exposure", "Tailscale Funnel exposes this agent to the public internet; require explicit tailnet policy controls and keep Basic Auth enabled."))
	}
	return warnings
}

func expectedAdvertiseHost(cfg instance.Config) string {
	if cfg.AdvertiseHost != "" {
		return cfg.AdvertiseHost
	}
	return instance.HostFromURL(cfg.AdvertiseURL)
}

func sameHost(left, right string) bool {
	return instance.IsLoopbackHost(left) && instance.IsLoopbackHost(right) || strings.EqualFold(left, right)
}
