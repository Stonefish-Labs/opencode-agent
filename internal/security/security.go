package security

type Warning struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
}

func Warn(code, field, message string) Warning {
	return Warning{Code: code, Severity: "warn", Field: field, Message: message}
}

func WarnPath(code, path, field, message string) Warning {
	return Warning{Code: code, Severity: "warn", Path: path, Field: field, Message: message}
}
