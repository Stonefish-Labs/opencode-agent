package keychain

import "testing"

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
