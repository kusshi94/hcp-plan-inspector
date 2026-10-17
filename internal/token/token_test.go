package token

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolve_EnvVarTakesPriority(t *testing.T) {
	t.Setenv(envToken, "env-token")
	t.Setenv(envCredentialsFile, "/does/not/exist")

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "env-token" {
		t.Errorf("Resolve() = %q, want %q", got, "env-token")
	}
}

func TestResolve_CredentialsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.tfrc.json")
	writeCredentials(t, path, Host, "file-token")

	t.Setenv(envToken, "")
	t.Setenv(envCredentialsFile, path)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "file-token" {
		t.Errorf("Resolve() = %q, want %q", got, "file-token")
	}
}

func TestResolve_MissingFile(t *testing.T) {
	t.Setenv(envToken, "")
	t.Setenv(envCredentialsFile, filepath.Join(t.TempDir(), "missing.json"))

	_, err := Resolve()
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Resolve() error = %v, want *NotFoundError", err)
	}
}

func TestResolve_MissingHostInFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.tfrc.json")
	writeCredentials(t, path, "example.com", "other-token")

	t.Setenv(envToken, "")
	t.Setenv(envCredentialsFile, path)

	_, err := Resolve()
	if err == nil {
		t.Fatal("Resolve() error = nil, want error")
	}
}

func writeCredentials(t *testing.T, path, host, token string) {
	t.Helper()
	data, err := json.Marshal(credentialsFile{
		Credentials: map[string]struct {
			Token string `json:"token"`
		}{
			host: {Token: token},
		},
	})
	if err != nil {
		t.Fatalf("marshal credentials: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}
