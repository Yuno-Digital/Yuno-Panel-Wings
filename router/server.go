package router

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"

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

	if err := rt.docker.Create(r.Context(), uuid, spec); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"uuid": uuid, "status": "created"})
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
