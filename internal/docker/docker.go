// Package docker is a thin wrapper around the Docker CLI used to control the
// containers that back game servers. It shells out to `docker` so the daemon
// stays dependency-free; this can later be swapped for the Docker Engine SDK.
package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Client controls server containers via the local Docker CLI.
type Client struct {
	// Prefix is prepended to UUIDs to form container names.
	Prefix string
	// DNS resolvers passed to every container (install + run).
	DNS []string
}

// New returns a Client that names containers "<prefix>.<uuid>".
func New(prefix string, dns []string) *Client {
	return &Client{Prefix: prefix, DNS: dns}
}

// dnsArgs builds the "--dns X" flags for docker run/create.
func (c *Client) dnsArgs() []string {
	args := make([]string, 0, len(c.DNS)*2)
	for _, d := range c.DNS {
		args = append(args, "--dns", d)
	}

	return args
}

// containerName builds the Docker container name for a server UUID.
func (c *Client) containerName(uuid string) string {
	return fmt.Sprintf("%s.%s", c.Prefix, uuid)
}

// Available reports whether the Docker daemon is reachable.
func (c *Client) Available(ctx context.Context) bool {
	return exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run() == nil
}

// Version returns the Docker server version, or an empty string if unavailable.
func (c *Client) Version(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// State returns the container state for a server: "running", "exited",
// "missing" (no such container), etc.
func (c *Client) State(ctx context.Context, uuid string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Status}}", c.containerName(uuid)).CombinedOutput()
	if err != nil {
		// Docker phrases this as "No such object" (older) or "no such object"
		// (newer), so match case-insensitively.
		if strings.Contains(strings.ToLower(string(out)), "no such object") {
			return "missing", nil
		}
		return "", fmt.Errorf("docker inspect: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Start starts the server's container.
func (c *Client) Start(ctx context.Context, uuid string) error {
	return c.run(ctx, "start", c.containerName(uuid))
}

// Stop gracefully stops the server's container.
func (c *Client) Stop(ctx context.Context, uuid string) error {
	return c.run(ctx, "stop", c.containerName(uuid))
}

// Restart restarts the server's container.
func (c *Client) Restart(ctx context.Context, uuid string) error {
	return c.run(ctx, "restart", c.containerName(uuid))
}

// run executes a docker subcommand and wraps any error with its output.
func (c *Client) run(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}
