package keychain

import (
	"testing"
	"time"
)

func TestMemoryKeychainIsolatesInstances(t *testing.T) {
	t.Setenv("OPENCODE_AGENT_TEST_KEYRING", "memory")
	ResetMemory()
	if err := Store("default", Credentials{Username: "opencode", Password: "one"}); err != nil {
		t.Fatalf("store default: %v", err)
	}
	if err := Store("api", Credentials{Username: "opencode", Password: "two"}); err != nil {
		t.Fatalf("store api: %v", err)
	}
	defaultCreds, err := Load("default")
	if err != nil {
		t.Fatalf("load default: %v", err)
	}
	apiCreds, err := Load("api")
	if err != nil {
		t.Fatalf("load api: %v", err)
	}
	if defaultCreds.Password != "one" || apiCreds.Password != "two" {
		t.Fatalf("credentials not isolated: default=%#v api=%#v", defaultCreds, apiCreds)
	}
	Delete("default")
	if _, err := Load("default"); err == nil {
		t.Fatalf("expected default credentials to be deleted")
	}
	if _, err := Load("api"); err != nil {
		t.Fatalf("api credentials should remain: %v", err)
	}
}

func TestCredentialsMetadataAndLegacyCompatibility(t *testing.T) {
	t.Setenv("OPENCODE_AGENT_TEST_KEYRING", "memory")
	ResetMemory()
	created := time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC)
	rotated := created.Add(time.Hour)
	if err := Store("meta", Credentials{Username: "opencode", Password: "secret", CreatedAt: created, RotatedAt: rotated}); err != nil {
		t.Fatalf("store: %v", err)
	}
	got, err := Load("meta")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Version != 1 || !got.CreatedAt.Equal(created) || !got.RotatedAt.Equal(rotated) {
		t.Fatalf("metadata mismatch: %#v", got)
	}

	memoryMu.Lock()
	memory[account("legacy")] = `{"username":"opencode","password":"legacy"}`
	memoryMu.Unlock()
	legacy, err := Load("legacy")
	if err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	if legacy.Version != 1 || legacy.CreatedAt.IsZero() || legacy.Password != "legacy" {
		t.Fatalf("legacy credentials were not normalized: %#v", legacy)
	}
}
