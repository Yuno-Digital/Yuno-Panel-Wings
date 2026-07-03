package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// CreateSpec describes the container to build for a server. The panel resolves
// the startup command and variables and sends the final values.
type CreateSpec struct {
	Image      string            `json:"image"`
	Command    string            `json:"command"`
	MemoryMB   int               `json:"memory_mb"`
	SwapMB     int               `json:"swap_mb"`
	CPU        int               `json:"cpu"` // percent, 0 = unlimited
	Ports      []int             `json:"ports"`
	Env        map[string]string `json:"env"`
	VolumePath string            `json:"-"` // host path bind-mounted into the container

	// Install script from the egg, run in InstallContainer with the server
	// volume mounted at /mnt/server before the game container is created.
	InstallScript    string `json:"install_script"`
	InstallContainer string `json:"install_container"`
	InstallEntry     string `json:"install_entry"`
}

// Stats is a point-in-time resource snapshot for a server.
type Stats struct {
	State         string  `json:"state"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryMB      float64 `json:"memory_mb"`
	MemoryLimitMB float64 `json:"memory_limit_mb"`
}

// Create (re)builds the server's container from spec. Any existing container
// for the server is removed first. The container is created but not started.
func (c *Client) Create(ctx context.Context, uuid string, spec CreateSpec) error {
	name := c.containerName(uuid)

	// Remove any previous container so create is idempotent.
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()

	if spec.VolumePath != "" {
		if err := os.MkdirAll(spec.VolumePath, 0o755); err != nil {
			return fmt.Errorf("create volume dir: %w", err)
		}
	}

	args := []string{"create", "--name", name, "-i", "-w", "/home/container", "--user", currentUser()}

	if spec.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", spec.MemoryMB))
		args = append(args, "--memory-swap", fmt.Sprintf("%dm", spec.MemoryMB+spec.SwapMB))
	}
	if spec.CPU > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(float64(spec.CPU)/100.0, 'f', 2, 64))
	}
	if spec.VolumePath != "" {
		args = append(args, "-v", spec.VolumePath+":/home/container")
	}
	for _, port := range spec.Ports {
		args = append(args, "-p", fmt.Sprintf("%d:%d", port, port))
	}
	for key, value := range spec.Env {
		args = append(args, "-e", key+"="+value)
	}

	args = append(args, spec.Image)
	if strings.TrimSpace(spec.Command) != "" {
		args = append(args, "sh", "-c", spec.Command)
	}

	// Run with the caller's context directly (no short timeout): creating a
	// container may pull a large image, which can take minutes.
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker create: %s", strings.TrimSpace(string(out)))
	}

	return nil
}

// Stats returns a resource snapshot for the server's container.
func (c *Client) Stats(ctx context.Context, uuid string) (Stats, error) {
	state, err := c.State(ctx, uuid)
	if err != nil {
		return Stats{}, err
	}

	stats := Stats{State: state}
	if state != "running" {
		return stats, nil
	}

	out, err := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{json .}}", c.containerName(uuid)).Output()
	if err != nil {
		return stats, fmt.Errorf("docker stats: %w", err)
	}

	var raw struct {
		CPUPerc  string `json:"CPUPerc"`
		MemUsage string `json:"MemUsage"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return stats, fmt.Errorf("parse stats: %w", err)
	}

	stats.CPUPercent = parsePercent(raw.CPUPerc)
	if used, limit, ok := strings.Cut(raw.MemUsage, "/"); ok {
		stats.MemoryMB = parseSizeMB(used)
		stats.MemoryLimitMB = parseSizeMB(limit)
	}

	return stats, nil
}

// Logs returns the last n lines of the server's container output.
func (c *Client) Logs(ctx context.Context, uuid string, lines int) (string, error) {
	if lines <= 0 || lines > 1000 {
		lines = 200
	}
	out, err := exec.CommandContext(ctx, "docker", "logs", "--tail", strconv.Itoa(lines), c.containerName(uuid)).CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "no such container") {
			return "", nil
		}
		return "", fmt.Errorf("docker logs: %s", strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// SendCommand writes a line to the server process's stdin (PID 1's fd 0),
// e.g. to run a Minecraft console command like "say hi" or "stop".
func (c *Client) SendCommand(ctx context.Context, uuid, command string) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", c.containerName(uuid), "sh", "-c", "cat > /proc/1/fd/0")
	cmd.Stdin = strings.NewReader(command + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("send command: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// currentUser returns "uid:gid" of the daemon process, used to run containers
// as the same user that owns the server volume so the server can write to it.
func currentUser() string {
	return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
}

// parsePercent turns "12.34%" into 12.34.
func parsePercent(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%")), 64)
	return v
}

// parseSizeMB converts a Docker size string like "1.5GiB" or "512MiB" to MB.
func parseSizeMB(s string) float64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		factor float64
	}{
		{"GiB", 1024}, {"GB", 1000}, {"MiB", 1}, {"MB", 1},
		{"KiB", 1.0 / 1024}, {"kB", 1.0 / 1000}, {"B", 1.0 / (1024 * 1024)},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			v, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
			return v * u.factor
		}
	}
	return 0
}
