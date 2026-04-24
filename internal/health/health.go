package health

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Stonefish-Labs/opencode-agent/internal/instance"
	"github.com/Stonefish-Labs/opencode-agent/internal/keychain"
	"github.com/Stonefish-Labs/opencode-agent/internal/process"
)

type Result struct {
	URL          string `json:"url"`
	OK           bool   `json:"ok"`
	Detail       string `json:"detail,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"`
	DurationMS   int64  `json:"duration_ms"`
	RequiresAuth bool   `json:"requires_auth"`
}

type Report struct {
	Name           string    `json:"name"`
	ConfigPath     string    `json:"config_path"`
	StatePath      string    `json:"state_path"`
	LogPath        string    `json:"log_path"`
	ServiceName    string    `json:"service_name"`
	ServiceState   string    `json:"service_state"`
	Username       string    `json:"username"`
	PasswordStored bool      `json:"password_stored"`
	AdvertiseURL   string    `json:"advertise_url"`
	BindURL        string    `json:"bind_url"`
	WorkingDir     string    `json:"working_directory"`
	PID            int       `json:"pid"`
	ProcessAlive   bool      `json:"process_alive"`
	RuntimeBind    string    `json:"runtime_bind_host,omitempty"`
	LocalHealth    Result    `json:"local_health"`
	TailnetHealth  Result    `json:"tailnet_health"`
	LastExit       string    `json:"last_exit,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
}

func Check(baseURL string, creds *keychain.Credentials, timeout time.Duration) Result {
	target, err := instance.HealthURL(baseURL)
	result := Result{URL: target, RequiresAuth: creds != nil}
	if err != nil {
		result.Detail = "invalid url: " + err.Error()
		return result
	}
	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		result.Detail = "request build: " + err.Error()
		return result
	}
	req.Close = true
	if creds != nil {
		req.SetBasicAuth(creds.Username, creds.Password)
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: timeout / 2, KeepAlive: 0}).DialContext,
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: timeout,
		},
	}
	defer client.CloseIdleConnections()

	resp, err := client.Do(req)
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
	runtimeBind := state.BindHost
	if runtimeBind == "" {
		runtimeBind = cfg.BindHost
	}
	localBase := instance.LocalBaseURLForBind(runtimeBind, cfg.Port)
	return Report{
		Name:           cfg.Name,
		ConfigPath:     paths.ConfigPath,
		StatePath:      paths.StatePath,
		LogPath:        paths.LogPath,
		ServiceName:    serviceName,
		ServiceState:   serviceState,
		Username:       cfg.Username,
		PasswordStored: passwordStored,
		AdvertiseURL:   cfg.AdvertiseURL,
		BindURL:        instance.BaseURL(runtimeBind, cfg.Port),
		WorkingDir:     cfg.WorkingDirectory,
		PID:            state.PID,
		ProcessAlive:   process.Default.Alive(state.PID),
		RuntimeBind:    state.BindHost,
		LocalHealth:    Check(localBase, credsPtr, 3*time.Second),
		TailnetHealth:  Check(cfg.AdvertiseURL, credsPtr, 5*time.Second),
		LastExit:       state.LastExit,
		LastError:      state.LastError,
		CheckedAt:      time.Now(),
	}
}
