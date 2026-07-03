package router

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/yuno/wings/internal/ws"
)

// wsMessage is the JSON envelope exchanged over the console websocket. It mirrors
// the panel's client protocol: an event name and a list of string arguments.
type wsMessage struct {
	Event string   `json:"event"`
	Args  []string `json:"args"`
}

// wsJSON builds an encoded server->client message.
func wsJSON(event string, args ...string) []byte {
	if args == nil {
		args = []string{}
	}
	b, _ := json.Marshal(wsMessage{Event: event, Args: args})
	return b
}

// funcWriter adapts a callback to io.Writer so the log follower can push each
// chunk of container output onto the socket.
type funcWriter struct{ fn func([]byte) }

func (f funcWriter) Write(p []byte) (int, error) {
	f.fn(append([]byte(nil), p...)) // copy: exec reuses the buffer
	return len(p), nil
}

// handleWS upgrades the request to a WebSocket and streams the server's console.
// Authentication happens in-band: the first message must be an "auth" event
// carrying a panel-issued token signed with this node's shared secret.
func (rt *Router) handleWS(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")

	conn, err := ws.Upgrade(w, r)
	if err != nil {
		rt.log.Warn("ws upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	// First frame must authenticate the connection for this server.
	raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var msg wsMessage
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Event != "auth" || len(msg.Args) == 0 {
		_ = conn.WriteText(wsJSON("error", "authentication required"))
		return
	}
	if server, err := rt.verifyWSToken(msg.Args[0]); err != nil || server != uuid {
		_ = conn.WriteText(wsJSON("error", "invalid token"))
		return
	}
	_ = conn.WriteText(wsJSON("auth success"))

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	send := func(event string, args ...string) { _ = conn.WriteText(wsJSON(event, args...)) }

	// Send the recent history once, then follow live output and stats.
	if tail, err := rt.docker.Logs(ctx, uuid, 200); err == nil && tail != "" {
		send("console output", tail)
	}
	rt.pushStatus(ctx, uuid, send)
	go rt.streamStats(ctx, uuid, send)
	go rt.streamConsole(ctx, uuid, send)

	// Read loop: commands and power actions coming from the browser.
	for {
		raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var m wsMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		switch m.Event {
		case "command":
			if len(m.Args) > 0 && strings.TrimSpace(m.Args[0]) != "" {
				if err := rt.docker.SendCommand(ctx, uuid, m.Args[0]); err != nil {
					send("error", err.Error())
				}
			}
		case "power":
			if len(m.Args) > 0 {
				rt.doPower(ctx, uuid, m.Args[0])
				rt.pushStatus(ctx, uuid, send)
			}
		}
	}
}

// pushStatus sends the current container state.
func (rt *Router) pushStatus(ctx context.Context, uuid string, send func(string, ...string)) {
	if state, err := rt.docker.State(ctx, uuid); err == nil {
		send("status", state)
	}
}

// doPower performs a power action, ignoring errors (the status update that
// follows reflects the real outcome).
func (rt *Router) doPower(ctx context.Context, uuid, action string) {
	switch action {
	case "start":
		_ = rt.docker.Start(ctx, uuid)
	case "stop":
		_ = rt.docker.Stop(ctx, uuid)
	case "restart":
		_ = rt.docker.Restart(ctx, uuid)
	}
}

// streamStats pushes a resource snapshot every couple of seconds, plus a status
// message whenever the state changes.
func (rt *Router) streamStats(ctx context.Context, uuid string, send func(string, ...string)) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()

	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			stats, err := rt.docker.Stats(ctx, uuid)
			if err != nil {
				continue
			}
			if b, err := json.Marshal(stats); err == nil {
				send("stats", string(b))
			}
			if stats.State != last {
				last = stats.State
				send("status", stats.State)
			}
		}
	}
}

// streamConsole follows the container's live output, re-attaching across
// stop/start cycles until the socket closes.
func (rt *Router) streamConsole(ctx context.Context, uuid string, send func(string, ...string)) {
	w := funcWriter{fn: func(p []byte) { send("console output", string(p)) }}
	for {
		if ctx.Err() != nil {
			return
		}
		if state, _ := rt.docker.State(ctx, uuid); state != "running" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		// Blocks until the container stops or ctx is cancelled.
		_ = rt.docker.StreamLogs(ctx, uuid, w)
	}
}

// verifyWSToken validates a panel-issued HS256 JWT and returns its "server"
// claim. The token is signed with this node's shared daemon token.
func (rt *Router) verifyWSToken(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed token")
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("bad signature encoding")
	}
	mac := hmac.New(sha256.New, []byte(rt.cfg.Token))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if subtle.ConstantTimeCompare(sig, mac.Sum(nil)) != 1 {
		return "", errors.New("signature mismatch")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("bad payload encoding")
	}
	var claims struct {
		Server string `json:"server"`
		Exp    int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", errors.New("bad claims")
	}
	if claims.Exp != 0 && time.Now().Unix() > claims.Exp {
		return "", errors.New("token expired")
	}
	return claims.Server, nil
}
