// Package config loads and persists the Yuno Wings daemon configuration.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// DefaultPath is the fallback config location (relative to the working dir).
const DefaultPath = "config.json"

// systemDir is the standard config directory used by the installer/systemd unit.
const systemDir = "/etc/yuno"

// ResolvePath returns the config file path. It honours $YUNO_CONFIG, then the
// system directory /etc/yuno (so `configure` and the daemon agree regardless of
// the current working directory), and finally falls back to ./config.json for
// dev and container runs.
func ResolvePath() string {
	if p := os.Getenv("YUNO_CONFIG"); p != "" {
		return p
	}
	if info, err := os.Stat(systemDir); err == nil && info.IsDir() {
		return systemDir + "/config.json"
	}
	return DefaultPath
}

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
	// DiskPath is the filesystem path used to detect total disk capacity.
	DiskPath string `json:"disk_path"`
	// DataPath is the base directory holding each server's files/volume.
	DataPath string `json:"data_path"`
	// BackupPath is the base directory where server backups are stored.
	BackupPath string `json:"backup_path"`
	// SSLCert and SSLKey point to a PEM certificate and private key. When both
	// are set, the API is served over HTTPS instead of plain HTTP.
	SSLCert string `json:"ssl_cert"`
	SSLKey  string `json:"ssl_key"`
}

// Default returns a config populated with sensible defaults and a fresh token.
func Default() *Config {
	return &Config{
		Token:        randomToken(),
		APIHost:      "0.0.0.0",
		APIPort:      8090,
		PanelURL:     "http://localhost:8000",
		DockerPrefix: "yuno",
		DiskPath:     "/",
		DataPath:     "/var/lib/yuno/servers",
		BackupPath:   "/var/lib/yuno/backups",
	}
}

// Backups returns the configured backup directory, falling back to a sibling of
// the data dir when unset (older config files).
func (c *Config) Backups() string {
	if c.BackupPath != "" {
		return c.BackupPath
	}

	return "/var/lib/yuno/backups"
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

// TLSEnabled reports whether both a certificate and key are configured, in
// which case the API is served over HTTPS.
func (c *Config) TLSEnabled() bool {
	return c.SSLCert != "" && c.SSLKey != ""
}

// Scheme returns "https" when TLS is enabled, otherwise "http".
func (c *Config) Scheme() string {
	if c.TLSEnabled() {
		return "https"
	}
	return "http"
}

// randomToken returns a 32-byte hex-encoded random string.
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
