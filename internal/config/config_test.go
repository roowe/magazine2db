package config

import (
	"os"
	"path/filepath"
	"testing"
)

const testConfig = `{
  "database": "test.db",
  "retention": 4,
  "summary": {
    "concurrency": 4,
    "max_tokens": 4096,
    "primary": {"base_url": "https://primary.example", "model": "primary-model"},
    "fallback": {"base_url": "https://fallback.example", "model": "fallback-model"}
  }
}`

func TestLoadFromPrefersCurrentDirectory(t *testing.T) {
	cwd := writeRuntime(t, "cwd", "cwd-primary", "cwd-fallback")
	execDir := writeRuntime(t, "exec", "exec-primary", "exec-fallback")

	cfg, err := LoadFrom(cwd, filepath.Join(execDir, "magazines2db"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkDir != cwd {
		t.Fatalf("work dir = %q, want %q", cfg.WorkDir, cwd)
	}
	if cfg.Database != filepath.Join(cwd, "test.db") {
		t.Fatalf("database = %q", cfg.Database)
	}
	if cfg.Summary.Primary.APIKey != "cwd-primary" || cfg.Summary.Fallback.APIKey != "cwd-fallback" {
		t.Fatal("API keys were not loaded from cwd/.env")
	}
}

func TestLoadFromFallsBackToExecutableDirectory(t *testing.T) {
	cwd := t.TempDir()
	execDir := writeRuntime(t, "exec", "primary-key", "fallback-key")

	cfg, err := LoadFrom(cwd, filepath.Join(execDir, "magazines2db"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkDir != execDir {
		t.Fatalf("work dir = %q, want %q", cfg.WorkDir, execDir)
	}
}

func writeRuntime(t *testing.T, name, primaryKey, fallbackKey string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cfg.json"), []byte(testConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	env := "export MAGAZINE_PRIMARY_API_KEY=\"" + primaryKey + "\"\n" +
		"export MAGAZINE_FALLBACK_API_KEY='" + fallbackKey + "'\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
