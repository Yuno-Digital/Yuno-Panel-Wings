package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yuno/wings/config"
)

// runConfigure fetches this node's configuration from the panel and writes it
// to config.json. The token is owned by the panel, so it stays stable across
// restarts (no more freshly generated token each boot).
func runConfigure(args []string) error {
	fs := flag.NewFlagSet("configure", flag.ExitOnError)
	panelURL := fs.String("panel-url", "", "Base URL of the Yuno Panel")
	token := fs.String("token", "", "Node token from the panel")
	node := fs.Int("node", 0, "Node ID from the panel")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *panelURL == "" || *token == "" || *node == 0 {
		return fmt.Errorf("--panel-url, --token and --node are all required")
	}

	url := fmt.Sprintf("%s/api/nodes/%d/config", strings.TrimRight(*panelURL, "/"), *node)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+*token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("contacting panel: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("panel returned status %d (check the URL, token and node id)", resp.StatusCode)
	}

	cfg := &config.Config{}
	if err := json.NewDecoder(resp.Body).Decode(cfg); err != nil {
		return fmt.Errorf("parsing panel response: %w", err)
	}
	// Remember where the panel lives for future reference.
	cfg.PanelURL = strings.TrimRight(*panelURL, "/")

	if err := cfg.Save(config.DefaultPath); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}
