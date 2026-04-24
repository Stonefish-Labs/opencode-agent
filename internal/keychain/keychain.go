package keychain

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

const ServiceName = "opencode-agent"

type Credentials struct {
	Version   int       `json:"version,omitempty"`
	Username  string    `json:"username"`
	Password  string    `json:"password"` // #nosec G117 -- marshaled only into the OS keychain blob, never into agent config or logs.
	CreatedAt time.Time `json:"created_at,omitempty"`
	RotatedAt time.Time `json:"rotated_at,omitempty"`
}

var (
	memoryMu sync.Mutex
	memory   = map[string]string{}
)

func Store(instance string, creds Credentials) error {
	if creds.Username == "" || creds.Password == "" {
		return errors.New("credentials are incomplete")
	}
	creds = Normalize(creds, time.Now())
	blob, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	if useMemory() {
		memoryMu.Lock()
		defer memoryMu.Unlock()
		memory[account(instance)] = string(blob)
		return nil
	}
	if err := keyring.Set(ServiceName, account(instance), string(blob)); err != nil {
		return fmt.Errorf("store credentials in OS keychain service %q: %w", ServiceName, err)
	}
	return nil
}

func Load(instance string) (Credentials, error) {
	var blob string
	if useMemory() {
		memoryMu.Lock()
		value, ok := memory[account(instance)]
		memoryMu.Unlock()
		if !ok {
			return Credentials{}, errors.New("password is missing from keychain")
		}
		blob = value
	} else {
		value, err := keyring.Get(ServiceName, account(instance))
		if err != nil {
			return Credentials{}, fmt.Errorf("read credentials from OS keychain service %q: %w", ServiceName, err)
		}
		blob = value
	}
	var creds Credentials
	if err := json.Unmarshal([]byte(blob), &creds); err != nil {
		return Credentials{}, err
	}
	if creds.Username == "" || creds.Password == "" {
		return Credentials{}, errors.New("stored credentials are incomplete")
	}
	creds = Normalize(creds, time.Now())
	return creds, nil
}

func Normalize(creds Credentials, now time.Time) Credentials {
	if creds.Version == 0 {
		creds.Version = 1
	}
	if creds.CreatedAt.IsZero() {
		creds.CreatedAt = now
	}
	return creds
}

func Delete(instance string) {
	if useMemory() {
		memoryMu.Lock()
		delete(memory, account(instance))
		memoryMu.Unlock()
		return
	}
	_ = keyring.Delete(ServiceName, account(instance))
}

func ResetMemory() {
	memoryMu.Lock()
	defer memoryMu.Unlock()
	memory = map[string]string{}
}

func account(instance string) string {
	if instance == "" {
		instance = "default"
	}
	return "instance:" + instance
}

func useMemory() bool {
	return os.Getenv("OPENCODE_AGENT_TEST_KEYRING") == "memory"
}
