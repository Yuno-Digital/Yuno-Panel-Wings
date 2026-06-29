# Yuno Panel Wings

Der **Node-Daemon** ("Wings") für [Yuno Panel](../Yuno-Panel), geschrieben in
**Go**. Läuft auf jedem Host, der Gameserver bereitstellt, und stellt eine
token-authentifizierte HTTP-API bereit, über die das Panel die zugehörigen
Docker-Container steuert. Inspiriert von [Pelican Wings](https://github.com/pelican-dev/wings),
bewusst minimal gehalten.

> Status: **Grundgerüst** – Config, token-gesicherte API, System-Info und
> Server-Power (start/stop/restart) über Docker funktionieren. Container-Erstellung,
> Konsole/Logs (WebSocket), Ressourcen-Stats, SFTP und Dateimanager folgen.

## Architektur

- **Abhängigkeitsfrei** – nur Go-Standardbibliothek (`net/http`, Go-1.22-Routing)
- **Docker** wird vorerst über die `docker`-CLI gesteuert (später Docker-Engine-SDK)
- Container-Namensschema: `<docker_prefix>.<uuid>`, z. B. `yuno.<server-uuid>`

```
main.go               Einstiegspunkt: Config laden, Server starten, Graceful Shutdown
config/config.go      Config laden/erzeugen (JSON, generiert Token beim ersten Start)
internal/docker/      Dünner Wrapper um die docker-CLI (state/start/stop/restart)
router/router.go      HTTP-Mux + Handler
router/middleware.go  Bearer-Token-Auth + Request-Logging
```

## Bauen & Starten

```bash
go build -o yuno-wings .

# Erster Start schreibt config.json (mit frischem Token) und beendet sich:
./yuno-wings

# config.json prüfen (panel_url, ggf. api_port anpassen), dann erneut starten:
./yuno-wings
```

Der Daemon lauscht standardmäßig auf `0.0.0.0:8080`.

## API

Alle `/api/*`-Endpoints erfordern den Header `Authorization: Bearer <token>`
(Token aus `config.json`). `/health` ist offen.

| Methode | Pfad | Zweck |
|---------|------|-------|
| GET  | `/health` | Liveness-Check (ohne Auth) |
| GET  | `/api/system` | Daemon- & Docker-Status |
| GET  | `/api/servers/{uuid}` | Container-Status (`running`/`exited`/`missing`) |
| POST | `/api/servers/{uuid}/power` | Body `{"action":"start\|stop\|restart"}` |

### Beispiel

```bash
TOKEN=$(grep -oP '"token":\s*"\K[^"]+' config.json)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/system
curl -X POST -H "Authorization: Bearer $TOKEN" \
     -d '{"action":"start"}' http://localhost:8080/api/servers/<uuid>/power
```

## Nächste Schritte

1. Container-Erstellung (Image, Ports, RAM/Disk-Limits, Volumes) statt nur Power
2. Live-Konsole & Logs per WebSocket
3. Ressourcen-Statistiken (CPU/RAM/Netz)
4. Anbindung ans Panel: Node-Token im Panel hinterlegen, Heartbeat/Registrierung
5. Wechsel von der docker-CLI auf das Docker-Engine-SDK
6. SFTP-Server & Dateimanager
