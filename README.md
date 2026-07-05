# Yuno Panel Wings

Der **Node-Daemon** („Wings") für [Yuno Panel](../Yuno-Panel), geschrieben in
**Go**. Er läuft auf jedem Host, der Gameserver bereitstellt, und stellt eine
token-authentifizierte HTTP-API bereit, über die das Panel die zugehörigen
Docker-Container erstellt, steuert und überwacht. Inspiriert von
[Pelican Wings](https://github.com/pelican-dev/wings), bewusst schlank gehalten.

> Aktuelle Version: **1.0.0-alpha5**

## Funktionen

- **Server-Lifecycle** – Container aus einem Egg installieren (Image-Pull +
  Install-Skript), starten/stoppen/neustarten, neu installieren
- **Live-Konsole per WebSocket** – Konsolen-Ausgabe, Ressourcen-Stats und
  Status in Echtzeit; Konsolen-Befehle und Power-Aktionen zurück an den Server.
  Die Installationsausgabe wird ebenfalls live in die Konsole gestreamt.
- **Ressourcen-Stats** – CPU- und RAM-Verbrauch pro Container
- **Dateimanager** – Verzeichnisse auflisten, Dateien lesen/schreiben/löschen
  (sandboxed auf das Server-Verzeichnis, kein Path-Traversal)
- **Host-Erkennung** – meldet Gesamt-RAM und -Disk an das Panel
- **Panel-eigene Tokens** – `configure` holt die Config inkl. Token vom Panel,
  damit der Token über Neustarts stabil bleibt

## Architektur

- **Abhängigkeitsfrei** – ausschließlich Go-Standardbibliothek (`net/http` mit
  Go-1.22-Routing, eigene minimale WebSocket-Implementierung nach RFC 6455)
- **Docker** wird über die `docker`-CLI gesteuert (`os/exec`)
- Container-Namensschema: `<docker_prefix>.<uuid>`, z. B. `yuno.<server-uuid>`
- Container laufen als **derselbe User wie der Daemon** (`--user uid:gid`), damit
  der Server in sein gebundenes Volume (`/home/container`) schreiben kann

```
main.go                 Einstiegspunkt: Config laden / configure, Server starten, Graceful Shutdown
configure.go            `configure`: Config inkl. Token vom Panel holen und speichern
config/config.go        Config laden/erzeugen (JSON)
internal/docker/        Wrapper um die docker-CLI (create/install/power/state/stats/logs/command)
internal/files/         Sandboxed Dateimanager (list/read/write/delete)
internal/ws/            Minimaler RFC-6455-WebSocket-Server (stdlib-only)
internal/system/        Host-Ressourcen (RAM/Disk) erkennen
router/router.go        HTTP-Mux + Handler
router/ws.go            Konsolen-WebSocket (JWT-Auth, Konsole/Stats/Install-Log-Streaming)
router/middleware.go    Bearer-Token-Auth + Request-Logging
```

## Voraussetzungen

- Go **1.26+** (zum Bauen)
- **Docker** auf dem Host
- Ein Datenverzeichnis, in das der Daemon-User schreiben darf, z. B.:
  ```bash
  sudo mkdir -p /var/lib/yuno && sudo chown $USER /var/lib/yuno
  ```

## Schnellinstallation (Debian/Ubuntu)

Installiert **Docker** und **Go**, baut den Daemon und richtet ihn als
**systemd-Dienst** (`yuno-wings`) mit Standard-Config ein:

```bash
curl -fsSL https://raw.githubusercontent.com/Yuno-Digital/Yuno-Panel-Wings/main/install.sh | bash
```

**Node ans Panel anbinden:** Danach im Panel unter **Admin → Nodes → \<Node\> →
Auto Deploy** den fertigen Befehl kopieren und auf dem Host ausführen — er holt
die Config (inkl. Token) vom Panel, schreibt `config.json` und startet den
Dienst neu:

```bash
cd /etc/yuno && sudo yuno-wings configure --panel-url https://panel.example.com \
    --token yuno_node_… --node 1 && sudo systemctl restart yuno-wings
```

Öffne ggf. die Firewall für den API-Port (Default `8090`), damit das Panel den
Host erreicht.

### One-shot & HTTPS (optional)

Wer alles in einem Rutsch will, kann Panel-Daten direkt an den Installer
übergeben. Mit `--domain` (DNS muss auf den Host zeigen) holt er zusätzlich per
**certbot** ein **Let's-Encrypt-Zertifikat**, trägt `ssl_cert`/`ssl_key` in die
`config.json` ein und richtet die Auto-Erneuerung inkl. Daemon-Neustart ein:

```bash
curl -fsSL https://raw.githubusercontent.com/Yuno-Digital/Yuno-Panel-Wings/main/install.sh | bash -s -- \
    --panel-url https://panel.example.com --token yuno_node_… --node 1 \
    --domain node01.example.com --ssl-email you@example.com
```

Bei HTTPS im Panel den Node auf **„Use HTTPS (TLS)"**, die Domain als FQDN und
Port `8090` setzen. (Port 80 muss für die Zertifikatsprüfung kurz frei sein.)
Ein späterer Auto-Deploy-Befehl behält die gesetzten `ssl_*`-Pfade bei.

Nützliche Befehle:

```bash
systemctl status yuno-wings      # Status
journalctl -u yuno-wings -f      # Live-Logs
```

Öffne ggf. die Firewall für den API-Port (Default `8090`), damit das Panel den
Host erreicht.

## Bauen

```bash
go build -o yuno-wings .
```

## Docker (GitHub Package)

Ein fertiges Image wird per CI nach dem **GitHub Container Registry** gepusht:
`ghcr.io/yuno-digital/yuno-panel-wings` (`latest` sowie pro Release ein
versioniertes Tag). Der Daemon steuert Docker über die CLI und den Socket des
Hosts – dieser muss gemountet werden, und `data_path` muss host- und
containerseitig **derselbe Pfad** sein (Bind-Mounts werden vom Host-Docker
aufgelöst).

```bash
# Einmalig konfigurieren (schreibt /etc/yuno/config.json):
docker run --rm -v /etc/yuno:/etc/yuno \
  ghcr.io/yuno-digital/yuno-panel-wings:latest \
  configure --panel-url https://panel.example.com --token yuno_node_… --node 1

# Daemon starten:
docker run -d --name yuno-wings --restart unless-stopped \
  -p 8090:8090 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /etc/yuno:/etc/yuno \
  -v /var/lib/yuno:/var/lib/yuno \
  ghcr.io/yuno-digital/yuno-panel-wings:latest
```

## Einrichten & Starten

### Empfohlen: Auto Deploy vom Panel

Im Panel unter **Admin → Nodes → \<Node\> → Auto Deploy** steht der fertige
Befehl. Der Daemon holt sich seine Config **inkl. Token vom Panel** und schreibt
`config.json`. **Ohne `sudo` ausführen**, sonst gehört `config.json` root und der
Daemon-User kann sie nicht lesen:

```bash
./yuno-wings configure --panel-url https://panel.example.com --token yuno_node_… --node 1
./yuno-wings
```

### Alternativ: standalone

```bash
# Erster Start schreibt config.json (mit frischem Token) und beendet sich:
./yuno-wings
# config.json prüfen/anpassen, dann erneut starten:
./yuno-wings
```

Der Daemon lauscht standardmäßig auf `0.0.0.0:8090`.

## Konfiguration (`config.json`)

| Feld | Zweck |
|------|-------|
| `token` | Shared Secret, das das Panel als Bearer-Token präsentiert |
| `api_host` / `api_port` | Bind-Adresse der HTTP-API (Default `0.0.0.0:8090`) |
| `panel_url` | Basis-URL des zugehörigen Panels |
| `docker_prefix` | Präfix der Container-Namen (`yuno` → `yuno.<uuid>`) |
| `disk_path` | Pfad zur Disk-Kapazitätserkennung |
| `data_path` | Basisverzeichnis der Server-Volumes (Default `/var/lib/yuno/servers`) |
| `backup_path` | Basisverzeichnis der Server-Backups (Default `/var/lib/yuno/backups`) |
| `ssl_cert` / `ssl_key` | Pfade zu PEM-Zertifikat und -Key. Sind **beide** gesetzt, läuft die API über **HTTPS** statt HTTP |

> `config.json` enthält das Secret und ist per `.gitignore` ausgeschlossen — niemals committen.

### SSL / HTTPS

Standardmäßig läuft die API über HTTP. Für HTTPS trägst du in `config.json` die
Pfade zu einem Zertifikat und Key ein (z. B. ein Let's-Encrypt-Cert oder ein
selbstsigniertes) und startest den Daemon neu:

```json
{
  "ssl_cert": "/etc/yuno/certs/fullchain.pem",
  "ssl_key":  "/etc/yuno/certs/privkey.pem"
}
```

Der Daemon-User muss beide Dateien lesen können. Im Panel muss der Node dann auf
**HTTPS** (mit passendem FQDN, der zum Zertifikat passt) und den API-Port zeigen.

## API

Alle `/api/*`-Endpoints erfordern `Authorization: Bearer <token>`. Ausnahmen:
`/health` (offen) und die Konsolen-WebSocket, die sich **in-band per
kurzlebigem JWT** authentifiziert (vom Panel mit dem Node-Token signiert).

| Methode | Pfad | Zweck |
|---------|------|-------|
| GET  | `/health` | Liveness-Check (ohne Auth) |
| GET  | `/api/system` | Daemon- & Docker-Status + erkanntes `memory_mb`/`disk_mb` |
| GET  | `/api/servers/{uuid}` | Container-Status (`running`/`exited`/`missing`) |
| POST | `/api/servers/{uuid}` | Server installieren (async): Image-Pull + Install-Skript + Container erstellen |
| POST | `/api/servers/{uuid}/power` | Body `{"action":"start\|stop\|restart"}` |
| POST | `/api/servers/{uuid}/command` | Konsolen-Befehl senden — Body `{command}` |
| GET  | `/api/servers/{uuid}/stats` | Ressourcen-Snapshot (state, cpu_percent, memory_mb) |
| GET  | `/api/servers/{uuid}/logs?lines=N` | Letzte Container-Logzeilen |
| GET  | `/api/servers/{uuid}/install-log` | Bisheriges Installations-Log |
| GET  | `/api/servers/{uuid}/ws` | Konsolen-WebSocket (Konsole/Stats/Status + Command/Power) |
| GET  | `/api/servers/{uuid}/files?path=/` | Verzeichnis auflisten (name, directory, size, modified) |
| GET  | `/api/servers/{uuid}/files/contents?path=…` | Datei lesen |
| POST | `/api/servers/{uuid}/files/write` | Datei schreiben — Body `{path, contents}` |
| POST | `/api/servers/{uuid}/files/delete` | Löschen — Body `{paths:[…]}` (rekursiv, Root geschützt) |
| POST | `/api/servers/{uuid}/backups` | Backup erstellen — Body `{backup_uuid}`, liefert `{bytes, checksum}` |
| GET  | `/api/servers/{uuid}/backups/{backup}/download` | Backup-Archiv (`.tar.gz`) streamen |
| POST | `/api/servers/{uuid}/backups/{backup}/restore` | Backup ins Server-Verzeichnis zurückspielen |
| DELETE | `/api/servers/{uuid}/backups/{backup}` | Backup-Archiv löschen |

### Beispiel

```bash
TOKEN=$(grep -oP '"token":\s*"\K[^"]+' config.json)
curl -H "Authorization: Bearer $TOKEN" http://localhost:8090/api/system
curl -X POST -H "Authorization: Bearer $TOKEN" \
     -d '{"action":"start"}' http://localhost:8090/api/servers/<uuid>/power
```

## Hinweise / Troubleshooting

- **`configure` gibt 404** – falsche `--panel-url` (Port beachten, z. B. `:8000`).
- **`config.json` nicht lesbar / 401** – `configure`/Daemon **ohne `sudo`** starten,
  sonst gehört die Datei root (0600). Ein selbst gestarteter Daemon ohne
  `configure` erzeugt einen eigenen Token → passt nicht zum Panel-Token.
- **`mkdir /var/lib/yuno: permission denied`** – Datenverzeichnis anlegen und dem
  Daemon-User geben (siehe *Voraussetzungen*).
- **Egg-Install-Skript scheitert (Syntaxfehler)** – CRLF-Zeilenenden aus
  importierten Eggs werden vor der Ausführung normalisiert.
