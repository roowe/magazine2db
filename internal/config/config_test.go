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
    "primary": {"model": "deepseek-v4-pro"},
    "fallback": {"base_url": "https://ollama.com/v1", "model": "deepseek-v4-pro"}
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
	if cfg.Summary.Primary.BaseURL != "https://myai.example/v1" {
		t.Fatalf("MyAI base URL = %q", cfg.Summary.Primary.BaseURL)
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
	env := "export MYAI_API_KEY=\"" + primaryKey + "\"\n" +
		"export MYAI_BASE_URL='https://myai.example/v1'\n" +
		"export OLLAMA_API_KEY='" + fallbackKey + "'\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
