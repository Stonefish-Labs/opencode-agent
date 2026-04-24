package projectconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/Stonefish-Labs/opencode-agent/internal/security"
)

const defaultConfig = `{
  "$schema": "https://opencode.ai/config.json",
  "permission": {
    "*": "ask",
    "read": "allow",
    "grep": "allow",
    "glob": "allow",
    "list": "allow",
    "external_directory": "ask"
  }
}
`

type Options struct {
	DryRun bool
	Seed   bool
}

type Report struct {
	Workdir   string             `json:"workdir"`
	SeedPath  string             `json:"seed_path,omitempty"`
	Seeded    bool               `json:"seeded"`
	WouldSeed bool               `json:"would_seed,omitempty"`
	Files     []File             `json:"files"`
	Warnings  []security.Warning `json:"warnings,omitempty"`
}

type File struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Exists bool   `json:"exists"`
}

func DefaultConfig() string {
	return defaultConfig
}

func Prepare(workdir string, opts Options) (Report, error) {
	if !opts.Seed {
		report := Audit(workdir)
		report.SeedPath = filepath.Join(workdir, "opencode.json")
		return report, nil
	}
	seedPath := filepath.Join(workdir, "opencode.json")
	if _, err := os.Stat(seedPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Report{}, fmt.Errorf("check project config %s: %w", seedPath, err)
		}
		if opts.DryRun {
			report := Audit(workdir)
			report.SeedPath = seedPath
			report.WouldSeed = true
			return report, nil
		}
		file, err := os.OpenFile(seedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- seedPath is inside the validated install workdir and O_EXCL prevents overwriting existing project config.
		if err != nil {
			return Report{}, fmt.Errorf("create secure project config %s: %w", seedPath, err)
		}
		if _, err := file.WriteString(defaultConfig); err != nil {
			_ = file.Close()
			return Report{}, fmt.Errorf("write secure project config %s: %w", seedPath, err)
		}
		if err := file.Close(); err != nil {
			return Report{}, fmt.Errorf("close secure project config %s: %w", seedPath, err)
		}
		report := Audit(workdir)
		report.SeedPath = seedPath
		report.Seeded = true
		return report, nil
	}
	report := Audit(workdir)
	report.SeedPath = seedPath
	return report, nil
}

func Audit(workdir string) Report {
	report := Report{Workdir: workdir, SeedPath: filepath.Join(workdir, "opencode.json")}
	for _, file := range configFiles(workdir) {
		_, err := os.Stat(file.Path)
		file.Exists = err == nil
		report.Files = append(report.Files, file)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				report.Warnings = append(report.Warnings, security.WarnPath("project_config.read_error", file.Path, "", "Could not inspect OpenCode project config: "+err.Error()))
			}
			continue
		}
		data, err := os.ReadFile(file.Path)
		if err != nil {
			report.Warnings = append(report.Warnings, security.WarnPath("project_config.read_error", file.Path, "", "Could not read OpenCode project config: "+err.Error()))
			continue
		}
		var parsed map[string]any
		if err := json.Unmarshal(stripJSONC(data), &parsed); err != nil {
			report.Warnings = append(report.Warnings, security.WarnPath("project_config.parse_error", file.Path, "", "OpenCode project config is not valid JSON/JSONC: "+err.Error()))
			continue
		}
		report.Warnings = append(report.Warnings, auditConfig(file.Path, parsed)...)
	}
	return report
}

func configFiles(workdir string) []File {
	return []File{
		{Path: filepath.Join(workdir, "opencode.json"), Kind: "project"},
		{Path: filepath.Join(workdir, "opencode.jsonc"), Kind: "project"},
		{Path: filepath.Join(workdir, ".opencode", "opencode.json"), Kind: "dot_opencode"},
		{Path: filepath.Join(workdir, ".opencode", "opencode.jsonc"), Kind: "dot_opencode"},
	}
}

func auditConfig(path string, cfg map[string]any) []security.Warning {
	var warnings []security.Warning
	warnings = append(warnings, auditPermission(path, cfg["permission"])...)
	warnings = append(warnings, auditTools(path, cfg["tools"])...)
	warnings = append(warnings, auditMCP(path, cfg["mcp"])...)
	sort.SliceStable(warnings, func(i, j int) bool {
		if warnings[i].Code == warnings[j].Code {
			return warnings[i].Field < warnings[j].Field
		}
		return warnings[i].Code < warnings[j].Code
	})
	return warnings
}

func auditPermission(path string, value any) []security.Warning {
	var warnings []security.Warning
	if strings.EqualFold(asString(value), "allow") {
		return append(warnings, security.WarnPath("project_config.permission.allow_all", path, "permission", "Project config sets all OpenCode permissions to allow."))
	}
	perm, ok := value.(map[string]any)
	if !ok {
		return warnings
	}
	if strings.EqualFold(asString(perm["*"]), "allow") {
		warnings = append(warnings, security.WarnPath("project_config.permission.allow_wildcard", path, "permission.*", "Project config auto-allows every OpenCode permission."))
	}
	for _, key := range dangerousPermissionKeys() {
		if permissionAllows(perm[key]) {
			warnings = append(warnings, security.WarnPath("project_config.permission.dangerous_allow", path, "permission."+key, "Project config auto-allows security-sensitive OpenCode tool "+key+"."))
		}
	}
	return warnings
}

func auditTools(path string, value any) []security.Warning {
	tools, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	var warnings []security.Warning
	for _, key := range dangerousPermissionKeys() {
		if enabled(tools[key]) {
			warnings = append(warnings, security.WarnPath("project_config.tools.dangerous_enable", path, "tools."+key, "Legacy tools config enables security-sensitive OpenCode tool "+key+"."))
		}
	}
	return warnings
}

func auditMCP(path string, value any) []security.Warning {
	servers, ok := value.(map[string]any)
	if !ok || len(servers) == 0 {
		return nil
	}
	var warnings []security.Warning
	for name, raw := range servers {
		field := "mcp." + name
		server, ok := raw.(map[string]any)
		if !ok {
			warnings = append(warnings, security.WarnPath("project_config.mcp.invalid", path, field, "MCP server entry is not an object."))
			continue
		}
		serverType := asString(server["type"])
		if serverType == "" {
			serverType = "local"
		}
		switch serverType {
		case "local":
			warnings = append(warnings, security.WarnPath("project_config.mcp.local", path, field, "Project config registers a local MCP server; local MCP commands run unsandboxed as your user."))
			if commandUsesNetworkTool(server["command"]) {
				warnings = append(warnings, security.WarnPath("project_config.mcp.local_network_tool", path, field+".command", "Local MCP command uses a network-capable tool such as curl, wget, nc, or ncat."))
			}
		case "remote":
			rawURL := asString(server["url"])
			parsed, err := url.Parse(rawURL)
			if rawURL == "" || err != nil || parsed.Scheme == "" || parsed.Host == "" {
				warnings = append(warnings, security.WarnPath("project_config.mcp.remote_invalid_url", path, field+".url", "Remote MCP server URL is missing or invalid."))
			} else if parsed.Scheme != "https" {
				warnings = append(warnings, security.WarnPath("project_config.mcp.remote_insecure_url", path, field+".url", "Remote MCP server URL is not HTTPS."))
			}
		}
		warnings = append(warnings, credentialKeyWarnings(path, field+".headers", server["headers"])...)
		warnings = append(warnings, credentialKeyWarnings(path, field+".environment", server["environment"])...)
	}
	return warnings
}

func permissionAllows(value any) bool {
	if strings.EqualFold(asString(value), "allow") {
		return true
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, nested := range obj {
		if strings.EqualFold(asString(nested), "allow") {
			return true
		}
	}
	return false
}

func dangerousPermissionKeys() []string {
	return []string{"bash", "edit", "write", "task", "agent", "skill", "webfetch", "websearch", "mcp"}
}

func enabled(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true") || strings.EqualFold(typed, "allow")
	default:
		return false
	}
}

func commandUsesNetworkTool(value any) bool {
	parts := stringSlice(value)
	if len(parts) == 0 {
		return false
	}
	for _, part := range strings.Fields(strings.Join(parts, " ")) {
		base := filepath.Base(part)
		switch base {
		case "curl", "wget", "nc", "ncat":
			return true
		}
	}
	return false
}

func credentialKeyWarnings(path, field string, value any) []security.Warning {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	var warnings []security.Warning
	for key := range obj {
		if looksCredentialBearing(key) {
			warnings = append(warnings, security.WarnPath("project_config.mcp.credential_key", path, field+"."+key, "MCP config contains a credential-looking key; use environment references or external secret storage instead of plaintext values."))
		}
	}
	return warnings
}

func looksCredentialBearing(key string) bool {
	upper := strings.ToUpper(key)
	for _, part := range []string{"AUTH", "COOKIE", "PASSWORD", "PASSWD", "SECRET", "TOKEN", "API_KEY", "APIKEY", "CREDENTIAL", "SESSION"} {
		if strings.Contains(upper, part) {
			return true
		}
	}
	return strings.HasSuffix(upper, "_KEY") || strings.Contains(upper, "_KEY_")
}

func asString(value any) string {
	if typed, ok := value.(string); ok {
		return typed
	}
	return ""
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if str := asString(item); str != "" {
				out = append(out, str)
			}
		}
		return out
	case []string:
		return typed
	case string:
		return strings.Fields(typed)
	default:
		return nil
	}
}

func stripJSONC(input []byte) []byte {
	input = stripComments(input)
	return stripTrailingCommas(input)
}

func stripComments(input []byte) []byte {
	var out bytes.Buffer
	inString := false
	escaped := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if inString {
			out.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			out.WriteByte(ch)
			continue
		}
		if ch == '/' && i+1 < len(input) {
			switch input[i+1] {
			case '/':
				i += 2
				for i < len(input) && input[i] != '\n' && input[i] != '\r' {
					i++
				}
				if i < len(input) {
					out.WriteByte(input[i])
				}
				continue
			case '*':
				i += 2
				for i+1 < len(input) && !(input[i] == '*' && input[i+1] == '/') {
					if input[i] == '\n' || input[i] == '\r' {
						out.WriteByte(input[i])
					}
					i++
				}
				i++
				continue
			}
		}
		out.WriteByte(ch)
	}
	return out.Bytes()
}

func stripTrailingCommas(input []byte) []byte {
	var out bytes.Buffer
	inString := false
	escaped := false
	for i := 0; i < len(input); i++ {
		ch := input[i]
		if inString {
			out.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			out.WriteByte(ch)
			continue
		}
		if ch == ',' {
			j := i + 1
			for j < len(input) && unicode.IsSpace(rune(input[j])) {
				j++
			}
			if j < len(input) && (input[j] == '}' || input[j] == ']') {
				continue
			}
		}
		out.WriteByte(ch)
	}
	return out.Bytes()
}
