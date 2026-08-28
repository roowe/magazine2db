package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testConfig = `{
  "database": "test.db",
  "retention": 4,
  "summary": {
    "concurrency": 4,
    "timeout_seconds": 1800,
    "codex_bin": "codex"
  }
}`

func TestLoadFromPrefersCurrentDirectory(t *testing.T) {
	cwd := writeRuntime(t, "cwd")
	execDir := writeRuntime(t, "exec")

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
	if cfg.Summary.CodexBin != "codex" || cfg.Summary.TimeoutSeconds != 1800 {
		t.Fatalf("summary config = %+v", cfg.Summary)
	}
}

func TestLoadFromFallsBackToExecutableDirectory(t *testing.T) {
	cwd := t.TempDir()
	execDir := writeRuntime(t, "exec")

	cfg, err := LoadFrom(cwd, filepath.Join(execDir, "magazines2db"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkDir != execDir {
		t.Fatalf("work dir = %q, want %q", cfg.WorkDir, execDir)
	}
}

func TestLoadFromRejectsTrailingContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cfg.json"), []byte(testConfig+"\n{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFrom(dir, filepath.Join(t.TempDir(), "magazine2db"))
	if err == nil || !strings.Contains(err.Error(), "trailing content") {
		t.Fatalf("expected trailing content error, got %v", err)
	}
}

func writeRuntime(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cfg.json"), []byte(testConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
