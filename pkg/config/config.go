// Package config loads Virgil's runtime configuration from a YAML file.
//
// The config file is the single source of truth for the GitHub App
// credentials, Anthropic API access, reviewer behavior, and SQLite
// storage path. It is never committed to the repository; see
// config.example.yaml for the schema.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Strictness presets. The reviewer package maps these to system-prompt
// variants; new values must be added in lockstep there.
const (
	StrictnessLenient  = "lenient"
	StrictnessBalanced = "balanced"
	StrictnessStrict   = "strict"
)

// Config is the top-level configuration loaded from YAML.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	GitHub    GitHubConfig    `yaml:"github"`
	Anthropic AnthropicConfig `yaml:"anthropic"`
	Reviewer  ReviewerConfig  `yaml:"reviewer"`
	Storage   StorageConfig   `yaml:"storage"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	// Port is the TCP port the webhook server listens on. Defaults to 8081.
	Port int `yaml:"port"`
}

// GitHubConfig holds the credentials needed to authenticate as the
// Virgil GitHub App and to verify incoming webhook payloads.
type GitHubConfig struct {
	AppID          int64  `yaml:"app_id"`
	PrivateKeyPath string `yaml:"private_key_path"`
	WebhookSecret  string `yaml:"webhook_secret"`
}

// AnthropicConfig holds the API key and model settings used by the reviewer.
//
// APIKey may be left empty in YAML; if so, Load falls back to the
// ANTHROPIC_API_KEY environment variable. If both are empty, Load errors.
type AnthropicConfig struct {
	APIKey    string `yaml:"api_key"`
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"`
}

// ReviewerConfig holds reviewer-behavior knobs.
type ReviewerConfig struct {
	Strictness string `yaml:"strictness"`
}

// StorageConfig holds persistent-state settings.
//
// Path defaults to ~/.local/share/virgil/state.db (XDG Base Directory).
// If $HOME is unset the default falls back to ./state.db. The parent
// directory is created on Load if it does not already exist.
type StorageConfig struct {
	Path string `yaml:"path"`
}

// Load reads a YAML config file from the given path, applies defaults,
// and validates required fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if c.Server.Port == 0 {
		c.Server.Port = 8081
	}

	expanded, err := expandHome(c.GitHub.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("expanding github.private_key_path: %w", err)
	}
	c.GitHub.PrivateKeyPath = expanded

	if c.Anthropic.APIKey == "" {
		c.Anthropic.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if c.Anthropic.Model == "" {
		c.Anthropic.Model = "claude-sonnet-4-6"
	}
	if c.Anthropic.MaxTokens == 0 {
		c.Anthropic.MaxTokens = 16384
	}

	if c.Reviewer.Strictness == "" {
		c.Reviewer.Strictness = StrictnessBalanced
	}

	resolvedPath, err := resolveStoragePath(c.Storage.Path)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	c.Storage.Path = resolvedPath

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// expandHome resolves a leading "~/" to the current user's home
// directory. Other path forms pass through unchanged.
func expandHome(path string) (string, error) {
	if path == "" || !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[2:]), nil
}

// resolveStoragePath expands ~ to $HOME, applies the XDG default when
// the path is empty, and ensures the parent directory exists. It returns
// the fully resolved absolute path.
func resolveStoragePath(p string) (string, error) {
	home, _ := os.UserHomeDir()

	if p == "" {
		if home == "" {
			p = "./state.db"
		} else {
			p = filepath.Join(home, ".local", "share", "virgil", "state.db")
		}
	} else if strings.HasPrefix(p, "~/") {
		if home == "" {
			return "", fmt.Errorf("storage.path uses ~/ but $HOME is unset")
		}
		p = filepath.Join(home, p[2:])
	}

	dir := filepath.Dir(p)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("creating storage parent dir %q: %w", dir, err)
		}
	}
	return p, nil
}

func (c *Config) validate() error {
	if c.GitHub.AppID == 0 {
		return fmt.Errorf("github.app_id is required")
	}
	if c.GitHub.PrivateKeyPath == "" {
		return fmt.Errorf("github.private_key_path is required")
	}
	if c.GitHub.WebhookSecret == "" {
		return fmt.Errorf("github.webhook_secret is required")
	}
	if _, err := os.Stat(c.GitHub.PrivateKeyPath); err != nil {
		return fmt.Errorf("github.private_key_path %s: %w", c.GitHub.PrivateKeyPath, err)
	}

	if c.Anthropic.APIKey == "" {
		return fmt.Errorf("anthropic.api_key is required (or set ANTHROPIC_API_KEY)")
	}

	switch c.Reviewer.Strictness {
	case StrictnessLenient, StrictnessBalanced, StrictnessStrict:
	default:
		return fmt.Errorf("reviewer.strictness must be one of lenient|balanced|strict, got %q", c.Reviewer.Strictness)
	}

	return nil
}
