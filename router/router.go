// Package router wires the daemon's HTTP API: a small net/http mux with a
// Bearer-token auth middleware and handlers for system info and server power.
package router

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/yuno/wings/config"
	"github.com/yuno/wings/internal/backups"
	"github.com/yuno/wings/internal/docker"
	"github.com/yuno/wings/internal/files"
	"github.com/yuno/wings/internal/system"
)

// Version is the daemon version, surfaced via /api/system.
const Version = "1.0.0-alpha5.1"

// installScriptURL is fetched to self-update the daemon (see handleUpdate).
const installScriptURL = "https://raw.githubusercontent.com/Yuno-Digital/Yuno-Panel-Wings/main/install.sh"

// Router holds the dependencies shared by all handlers.
type Router struct {
	cfg    *config.Config
	docker *docker.Client
	files  *files.Manager
	log    *slog.Logger
}

// New builds a Router and returns an http.Handler with all routes registered.
func New(cfg *config.Config, dc *docker.Client, log *slog.Logger) http.Handler {
	rt := &Router{cfg: cfg, docker: dc, files: files.New(cfg.DataPath), log: log}

	mux := http.NewServeMux()
	// Health check is intentionally unauthenticated.
	mux.HandleFunc("GET /health", rt.handleHealth)
	mux.Handle("GET /api/system", rt.auth(http.HandlerFunc(rt.handleSystem)))
	mux.Handle("POST /api/update", rt.auth(http.HandlerFunc(rt.handleUpdate)))
	mux.Handle("GET /api/servers/{uuid}", rt.auth(http.HandlerFunc(rt.handleServerStatus)))
	mux.Handle("POST /api/servers/{uuid}", rt.auth(http.HandlerFunc(rt.handleCreate)))
	mux.Handle("POST /api/servers/{uuid}/power", rt.auth(http.HandlerFunc(rt.handlePower)))
	mux.Handle("POST /api/servers/{uuid}/command", rt.auth(http.HandlerFunc(rt.handleCommand)))
	mux.Handle("GET /api/servers/{uuid}/stats", rt.auth(http.HandlerFunc(rt.handleStats)))
	mux.Handle("GET /api/servers/{uuid}/logs", rt.auth(http.HandlerFunc(rt.handleLogs)))
	mux.Handle("GET /api/servers/{uuid}/install-log", rt.auth(http.HandlerFunc(rt.handleInstallLog)))
	// The console websocket authenticates in-band with a panel-issued token,
	// so it is not wrapped in the Bearer-token middleware.
	mux.HandleFunc("GET /api/servers/{uuid}/ws", rt.handleWS)
	mux.Handle("GET /api/servers/{uuid}/files", rt.auth(http.HandlerFunc(rt.handleFileList)))
	mux.Handle("GET /api/servers/{uuid}/files/contents", rt.auth(http.HandlerFunc(rt.handleFileRead)))
	mux.Handle("POST /api/servers/{uuid}/files/write", rt.auth(http.HandlerFunc(rt.handleFileWrite)))
	mux.Handle("POST /api/servers/{uuid}/files/delete", rt.auth(http.HandlerFunc(rt.handleFileDelete)))
	mux.Handle("POST /api/servers/{uuid}/backups", rt.auth(http.HandlerFunc(rt.handleBackupCreate)))
	mux.Handle("POST /api/servers/{uuid}/backups/{backup}/restore", rt.auth(http.HandlerFunc(rt.handleBackupRestore)))
	mux.Handle("GET /api/servers/{uuid}/backups/{backup}/download", rt.auth(http.HandlerFunc(rt.handleBackupDownload)))
	mux.Handle("DELETE /api/servers/{uuid}/backups/{backup}", rt.auth(http.HandlerFunc(rt.handleBackupDelete)))

	return logRequests(log, mux)
}

// handleHealth is a simple liveness probe.
func (rt *Router) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleSystem reports daemon status, Docker status and detected host resources
// (total memory and disk) so the panel can auto-fill a node's capacity.
func (rt *Router) handleSystem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Resource detection is best-effort: log failures but still respond.
	memoryMB, err := system.MemoryMB()
	if err != nil {
		rt.log.Warn("failed to detect memory", "error", err)
	}
	diskMB, err := system.DiskMB(rt.cfg.DiskPath)
	if err != nil {
		rt.log.Warn("failed to detect disk", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"version":        Version,
		"os":             runtime.GOOS,
		"arch":           runtime.GOARCH,
		"docker_ok":      rt.docker.Available(ctx),
		"docker_version": rt.docker.Version(ctx),
		"memory_mb":      memoryMB,
		"disk_mb":        diskMB,
	})
}

// handleUpdate self-updates the daemon by re-running the installer (which pulls
// the latest code, rebuilds the binary and restarts the service). It runs
// out-of-band via systemd-run so the service restart at the end of the update
// doesn't kill the updater (it lives in a separate cgroup). Returns immediately.
func (rt *Router) handleUpdate(w http.ResponseWriter, r *http.Request) {
	script := "curl -fsSL " + installScriptURL + " | bash > /etc/yuno/update.log 2>&1"
	cmd := exec.Command("systemd-run", "--collect", "--unit", "yuno-wings-update",
		"/bin/sh", "-c", script)

	if err := cmd.Run(); err != nil {
		rt.log.Error("failed to start self-update", "error", err)
		writeError(w, http.StatusInternalServerError, "could not start update: "+err.Error())
		return
	}

	rt.log.Info("self-update started")
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "updating"})
}

// handleServerStatus returns the Docker state for a single server.
func (rt *Router) handleServerStatus(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	state, err := rt.docker.State(r.Context(), uuid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"uuid": uuid, "state": state})
}

// powerRequest is the body of a power action request.
type powerRequest struct {
	Action string `json:"action"` // start | stop | restart
}

// handlePower performs a start/stop/restart on a server's container.
func (rt *Router) handlePower(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")

	var req powerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	ctx := r.Context()
	var err error
	switch req.Action {
	case "start":
		err = rt.docker.Start(ctx, uuid)
	case "stop":
		err = rt.docker.Stop(ctx, uuid)
	case "restart":
		err = rt.docker.Restart(ctx, uuid)
	default:
		writeError(w, http.StatusBadRequest, "action must be start, stop or restart")
		return
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"uuid": uuid, "action": req.Action})
}

// writeJSON serializes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// backupRequest carries the panel-generated backup uuid used as the filename.
type backupRequest struct {
	UUID string `json:"backup_uuid"`
}

// handleBackupCreate tars the server's data dir into a new backup archive.
func (rt *Router) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")

	var req backupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UUID == "" {
		writeError(w, http.StatusBadRequest, "backup_uuid is required")
		return
	}

	res, err := backups.Create(rt.cfg.DataPath, rt.cfg.Backups(), uuid, req.UUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleBackupDelete removes a backup archive.
func (rt *Router) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if err := backups.Delete(rt.cfg.Backups(), r.PathValue("uuid"), r.PathValue("backup")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handleBackupRestore extracts a backup back into the server's data dir.
func (rt *Router) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if err := backups.Restore(rt.cfg.DataPath, rt.cfg.Backups(), r.PathValue("uuid"), r.PathValue("backup")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restored"})
}

// handleBackupDownload streams a backup archive.
func (rt *Router) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	backup := r.PathValue("backup")
	path, err := backups.Path(rt.cfg.Backups(), r.PathValue("uuid"), backup)
	if err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+backup+".tar.gz\"")
	http.ServeFile(w, r, path)
}

// writeError writes a JSON error body.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
