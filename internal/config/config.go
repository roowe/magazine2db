package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Summary struct {
	CodexBin       string `json:"codex_bin"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Concurrency    int    `json:"concurrency"`
}

type Config struct {
	WorkDir   string  `json:"-"`
	Database  string  `json:"database"`
	Retention int     `json:"retention"`
	Summary   Summary `json:"summary"`
}

func Load() (Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Config{}, fmt.Errorf("get current directory: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return Config{}, fmt.Errorf("locate executable: %w", err)
	}
	return LoadFrom(cwd, executable)
}

// LoadFrom finds cfg.json in cwd first, then next to the executable.
func LoadFrom(cwd, executable string) (Config, error) {
	paths := []string{filepath.Join(cwd, "cfg.json"), filepath.Join(filepath.Dir(executable), "cfg.json")}
	var cfgPath string
	for index, path := range paths {
		if index > 0 && path == paths[0] {
			continue
		}
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			cfgPath = path
			break
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("inspect %s: %w", path, err)
		}
	}
	if cfgPath == "" {
		return Config{}, fmt.Errorf("cfg.json not found in %s or %s", cwd, filepath.Dir(executable))
	}

	file, err := os.Open(cfgPath)
	if err != nil {
		return Config{}, fmt.Errorf("open %s: %w", cfgPath, err)
	}
	defer file.Close()
	var cfg Config
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", cfgPath, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, fmt.Errorf("decode %s: trailing content", cfgPath)
	}
	cfg.WorkDir = filepath.Dir(cfgPath)
	if !filepath.IsAbs(cfg.Database) {
		cfg.Database = filepath.Join(cfg.WorkDir, cfg.Database)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", cfgPath, err)
	}
	return cfg, nil
}

func (cfg Config) validate() error {
	if cfg.Database == "" {
		return errors.New("database is required")
	}
	if cfg.Retention < 1 {
		return errors.New("retention must be positive")
	}
	if cfg.Summary.Concurrency < 1 {
		return errors.New("summary.concurrency must be positive")
	}
	if cfg.Summary.TimeoutSeconds < 1 {
		return errors.New("summary.timeout_seconds must be positive")
	}
	if cfg.Summary.CodexBin == "" {
		return errors.New("summary.codex_bin is required")
	}
	return nil
}
