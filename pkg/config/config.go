// Package config loads Virgil's runtime configuration from a YAML file.
//
// The config file is the single source of truth for the GitHub App
// credentials and HTTP server settings. It is never committed to the
// repository; see config.example.yaml for the schema.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level configuration loaded from YAML.
type Config struct {
	Server ServerConfig `yaml:"server"`
	GitHub GitHubConfig `yaml:"github"`
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
	return nil
}
