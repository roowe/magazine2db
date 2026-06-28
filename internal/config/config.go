package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

type Provider struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"-"`
}

type Summary struct {
	Concurrency int      `json:"concurrency"`
	MaxTokens   int      `json:"max_tokens"`
	Primary     Provider `json:"primary"`
	Fallback    Provider `json:"fallback"`
}

type Config struct {
	WorkDir   string  `json:"-"`
	Database  string  `json:"database"`
	Retention int     `json:"retention"`
	Summary   Summary `json:"summary"`
}

var apiKeyPattern = regexp.MustCompile(`(?m)\b(MAGAZINE_(?:PRIMARY|FALLBACK)_API_KEY)\s*=\s*("[^"]*"|'[^']*'|[^\s#]+)`)

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
	cfg.WorkDir = filepath.Dir(cfgPath)
	if !filepath.IsAbs(cfg.Database) {
		cfg.Database = filepath.Join(cfg.WorkDir, cfg.Database)
	}
	if err := loadAPIKeys(filepath.Join(cfg.WorkDir, ".env"), &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", cfgPath, err)
	}
	return cfg, nil
}

func loadAPIKeys(path string, cfg *Config) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	values := make(map[string]string)
	for _, match := range apiKeyPattern.FindAllStringSubmatch(string(content), -1) {
		value := match[2]
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
			if value[0] == '"' {
				unquoted, err := strconv.Unquote(value)
				if err != nil {
					return fmt.Errorf("parse %s in %s: %w", match[1], path, err)
				}
				value = unquoted
			} else {
				value = value[1 : len(value)-1]
			}
		}
		values[match[1]] = value
	}
	cfg.Summary.Primary.APIKey = values["MAGAZINE_PRIMARY_API_KEY"]
	cfg.Summary.Fallback.APIKey = values["MAGAZINE_FALLBACK_API_KEY"]
	return nil
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
	if cfg.Summary.MaxTokens < 1 {
		return errors.New("summary.max_tokens must be positive")
	}
	if cfg.Summary.Primary.BaseURL == "" || cfg.Summary.Primary.Model == "" || cfg.Summary.Primary.APIKey == "" {
		return errors.New("summary primary base_url, model and MAGAZINE_PRIMARY_API_KEY are required")
	}
	if cfg.Summary.Fallback.BaseURL == "" || cfg.Summary.Fallback.Model == "" || cfg.Summary.Fallback.APIKey == "" {
		return errors.New("summary fallback base_url, model and MAGAZINE_FALLBACK_API_KEY are required")
	}
	return nil
}
