// Package config loads and persists the Yuno Wings daemon configuration.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// DefaultPath is where the daemon looks for its config file.
const DefaultPath = "config.json"

// Config holds all runtime settings for the daemon.
type Config struct {
	// Token is the shared secret the panel must present as a Bearer token.
	Token string `json:"token"`
	// APIHost is the address the HTTP API binds to.
	APIHost string `json:"api_host"`
	// APIPort is the port the HTTP API listens on.
	APIPort int `json:"api_port"`
	// PanelURL is the base URL of the Yuno Panel this node belongs to.
	PanelURL string `json:"panel_url"`
	// DockerPrefix is prepended to container names, e.g. "yuno" -> "yuno.<uuid>".
	DockerPrefix string `json:"docker_prefix"`
}

// Default returns a config populated with sensible defaults and a fresh token.
func Default() *Config {
	return &Config{
		Token:        randomToken(),
		APIHost:      "0.0.0.0",
		APIPort:      8080,
		PanelURL:     "http://localhost:8000",
		DockerPrefix: "yuno",
	}
}

// Load reads the config file at path. If it does not exist, a default config is
// written to disk and returned alongside a flag indicating it was just created.
func Load(path string) (cfg *Config, created bool, err error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg = Default()
		if err := cfg.Save(path); err != nil {
			return nil, false, fmt.Errorf("writing default config: %w", err)
		}
		return cfg, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("reading config: %w", err)
	}

	cfg = &Config{}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, false, fmt.Errorf("parsing config: %w", err)
	}
	return cfg, false, nil
}

// Save writes the config to path as indented JSON with owner-only permissions.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Address returns the host:port the API should bind to.
func (c *Config) Address() string {
	return fmt.Sprintf("%s:%d", c.APIHost, c.APIPort)
}

// randomToken returns a 32-byte hex-encoded random string.
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
