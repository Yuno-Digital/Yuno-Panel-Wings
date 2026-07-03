package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Install performs the full, logged installation of a server, mirroring
// Pelican: run the egg's install script (which downloads the server files)
// in the installer container, pull the game image, then create the container.
// All command output is streamed to log.
func (c *Client) Install(ctx context.Context, uuid string, spec CreateSpec, log io.Writer) error {
	name := c.containerName(uuid)

	stamp := func(format string, a ...any) {
		fmt.Fprintf(log, "\n\x1b[36m[yuno] "+format+"\x1b[0m\n", a...)
	}

	stamp("Preparing installation of %s", uuid)
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()

	if spec.VolumePath != "" {
		if err := os.MkdirAll(spec.VolumePath, 0o755); err != nil {
			return fmt.Errorf("create volume dir: %w", err)
		}
	}

	// 1. Run the egg install script (downloads server.jar etc.).
	if strings.TrimSpace(spec.InstallScript) != "" {
		container := spec.InstallContainer
		if container == "" {
			container = "ghcr.io/pelican-eggs/installers:debian"
		}
		entry := spec.InstallEntry
		if entry == "" {
			entry = "bash"
		}

		stamp("Pulling install image %s", container)
		if err := stream(ctx, log, "docker", "pull", container); err != nil {
			return err
		}

		// Normalise line endings: eggs imported from JSON often carry Windows
		// CRLFs, which break shell parsing (\r seen as part of commands).
		script := strings.ReplaceAll(spec.InstallScript, "\r\n", "\n")
		script = strings.ReplaceAll(script, "\r", "\n")

		scriptPath := filepath.Join(os.TempDir(), name+".install")
		if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
			return fmt.Errorf("write install script: %w", err)
		}
		defer os.Remove(scriptPath)

		stamp("Running install script with %s", entry)
		args := []string{
			"run", "--rm", "--entrypoint", entry,
			"-v", spec.VolumePath + ":/mnt/server",
			"-v", scriptPath + ":/mnt/install.sh:ro",
			"-w", "/mnt/server",
		}
		for key, value := range spec.Env {
			args = append(args, "-e", key+"="+value)
		}
		args = append(args, container, "/mnt/install.sh")

		if err := stream(ctx, log, "docker", args...); err != nil {
			return err
		}
	} else {
		stamp("Egg has no install script — skipping")
	}

	// 2. Pull the game image.
	stamp("Pulling server image %s", spec.Image)
	if err := stream(ctx, log, "docker", "pull", spec.Image); err != nil {
		return err
	}

	// 3. Create the game container.
	stamp("Creating server container")
	if err := c.Create(ctx, uuid, spec); err != nil {
		fmt.Fprintf(log, "[yuno] create failed: %v\n", err)
		return err
	}

	stamp("Installation complete — the server is ready to start")
	return nil
}

// stream runs a command, copying its combined output to w as it goes.
func stream(ctx context.Context, w io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(w, "\x1b[31m[yuno] step failed: %v\x1b[0m\n", err)
		return err
	}
	return nil
}
