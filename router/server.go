package router

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yuno/wings/internal/docker"
)

// handleCreate (re)builds a server's container from the supplied spec.
func (rt *Router) handleCreate(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")

	var spec docker.CreateSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if spec.Image == "" {
		writeError(w, http.StatusBadRequest, "image is required")
		return
	}

	spec.VolumePath = filepath.Join(rt.cfg.DataPath, uuid)

	logPath := rt.installLogPath(uuid)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Install in the background: pulling images and running the install script
	// can take minutes and must not be cancelled when the panel's request
	// times out or disconnects. Output is streamed to the install log.
	go func() {
		defer logFile.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		if err := rt.docker.Install(ctx, uuid, spec, logFile); err != nil {
			rt.log.Error("install failed", "uuid", uuid, "error", err)
			return
		}
		rt.log.Info("server installed", "uuid", uuid)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"uuid": uuid, "status": "installing"})
}

// installLogPath returns the file the install output is streamed to.
func (rt *Router) installLogPath(uuid string) string {
	return filepath.Join(rt.cfg.DataPath, ".install-logs", uuid+".log")
}

// handleInstallLog returns the install log for a server.
func (rt *Router) handleInstallLog(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(rt.installLogPath(r.PathValue("uuid")))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"log": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"log": string(data)})
}

// handleStats returns a resource snapshot for the server.
func (rt *Router) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := rt.docker.Stats(r.Context(), r.PathValue("uuid"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// handleLogs returns the tail of the server's container output.
func (rt *Router) handleLogs(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	out, err := rt.docker.Logs(r.Context(), r.PathValue("uuid"), lines)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": out})
}

// handleFileList lists a directory in the server's volume.
func (rt *Router) handleFileList(w http.ResponseWriter, r *http.Request) {
	entries, err := rt.files.List(r.PathValue("uuid"), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// handleFileRead returns a file's contents.
func (rt *Router) handleFileRead(w http.ResponseWriter, r *http.Request) {
	contents, err := rt.files.Read(r.PathValue("uuid"), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"contents": contents})
}

// handleFileWrite creates or overwrites a file.
func (rt *Router) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path     string `json:"path"`
		Contents string `json:"contents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := rt.files.Write(r.PathValue("uuid"), body.Path, body.Contents); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}
