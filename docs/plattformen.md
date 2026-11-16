# Plattformen: macOS, Linux, Windows/WSL

Tiegel trennt strikt zwischen Backend (Kern, Module, API – `internal/api`, kein Gio) und GUI (Gio, `internal/ui`, `internal/app`). Die GUI ist nur ein Client der API (D41). Wie Backend und GUI zusammenkommen, hängt von der Plattform ab.

## Überblick

| Plattform | GUI | Backend | Verbindung | Tree-sitter |
|---|---|---|---|---|
| macOS | Gio (Metal), cgo | im selben Prozess | `api.InProcess` (direkter Aufruf, kein JSON) | im Prozess |
| Linux | Gio (Wayland/X11, cgo) | im selben Prozess | `api.InProcess` | im Prozess |
| Windows | Gio (Direct3D), **ohne cgo** | `tiegel --headless serve` in der WSL (Linux-Build mit cgo) | `client.Spawn` über stdio (`wsl.exe -d <Distro> -- tiegel --headless serve`) oder `client.Dial` auf Socket | im Backend, per API (`syntax.*`, D80) |
| Remote/Skripte | – | `serve --listen` | Unix-Socket oder Loopback-TCP mit Token | im Backend |

Die GUI programmiert gegen **eine** Schnittstelle, `api.Caller` (D82):

```go
type Caller interface {
	Call(ctx context.Context, method string, params, result any) error
	Subscribe(ctx context.Context, topics []string, fn func(api.Event)) (cancel func(), err error)
}

st, err := api.Call[api.DocState](ctx, c, "doc.insert", api.DocInsertParams{...})
```

- `api.NewInProcess(app)` – macOS/Linux: Aufrufe gehen direkt in die Methodentabelle. Passen Parameter- und Ergebnistyp zum Handler, wird nichts kodiert (Werte werden geteilt, nicht tief kopiert).
- `client.Dial(ctx, "unix:/run/user/1000/tiegel.sock", token)` / `client.Spawn(ctx, argv)` – Windows/Remote: JSON-RPC 2.0.

Beide liefern Fehler als `*api.Error` mit denselben Codes (`api.ErrorCode(err)`), Events als `api.Event{Topic, Payload}` (Payload immer JSON, auch in-process). Eine Testsuite (`internal/api/client/caller_test.go`) läuft gegen In-Process, Pipe, Unix-Socket und TCP.

## Windows ↔ WSL

```
┌────────── Windows ──────────┐        ┌──────────── WSL (Linux) ────────────┐
│ tiegel.exe (Gio, CGO=0)     │ stdio  │ tiegel --headless serve              │
│  api.Caller = client.Client ├────────┤  Kern, Module, Tree-sitter, LSP,     │
│  docsync.Mirror je Dokument │ (oder  │  git, php, Xdebug-Listener           │
│  Highlighting via syntax.*  │ Socket)│  Projekt liegt im Linux-Dateisystem  │
└─────────────────────────────┘        └──────────────────────────────────────┘
```

- **Start:** Die Windows-GUI startet das Backend mit `client.Spawn(ctx, client.WSLServeCommand("Ubuntu", "tiegel", "--project", "/home/u/app"))`. stdio braucht kein Token: Nur der Elternprozess hat die Pipe. Beim Schließen bekommt das Backend EOF und beendet sich; hängt es, wird es nach einer Frist beendet.
- **Alternativ Socket:** Läuft das Backend bereits (`serve --listen`), verbindet sich die GUI mit `client.Dial`. Unix-Sockets funktionieren zwischen Windows 10+ und WSL1; für WSL2 ist `tcp:127.0.0.1:PORT` (localhost-Weiterleitung) der robuste Weg.
- **Dokumente:** Remote gibt es keinen geteilten Puffer. `internal/api/docsync.Mirror` hält eine lokale Kopie (`buffer.Buffer`), sendet eigene Änderungen als `doc.insert`/`doc.delete`/`doc.replace` mit `version` und wendet fremde `doc.edited`-Events (andere Clients, Undo, Formatter, `git.rollback`) in Versionsreihenfolge an. Der Server ist Autorität: Bei Versionskonflikt, Lücke oder unpassendem Alttext lädt der Spiegel den Text neu (`doc.text`); ein abgelehnter eigener Edit liefert `docsync.ErrConflict`. Eigene Edits werden erst nach Bestätigung lokal angewendet (ein Roundtrip, lokal < 1 ms).
- **Highlighting:** Das Windows-Binary hat kein Tree-sitter (cgo). Highlights, Folds und Outline kommen vom Backend über `syntax.highlights` (gern nur für den sichtbaren Zeilenbereich), `syntax.folds`, `syntax.outline` (D80).
- **Pfade:** Die GUI zeigt Windows-Pfade, das Backend arbeitet mit Linux-Pfaden. Übersetzung in `internal/interp` (reine Funktionen):

| Windows | WSL |
|---|---|
| `C:\Users\u\app` | `/mnt/c/Users/u/app` |
| `\\wsl.localhost\Ubuntu\home\u\app` | `/home/u/app` |
| `\\wsl$\Ubuntu\home\u\app` (ältere Form) | `/home/u/app` |

  `interp.WindowsToWSL(p, distro)` liefert `false` für relative Pfade, fremde Distros und andere Freigaben; `interp.WSLToWindows(p, distro)` erzeugt `\\wsl.localhost\<Distro>\…` (bzw. `C:\…` für `/mnt/c/…`). Empfehlung: Projekte im Linux-Dateisystem halten (`/home/…`), `/mnt/c` ist unter WSL2 langsam.
- **PHP in WSL ohne Tiegel-Backend:** `interp.WSL` führt `wsl.exe -d <Distro> -- php …` aus; `interp.ListDistros` liest `wsl.exe -l -q` (UTF-16LE), `interp.DiscoverWSL` sucht `php` je Distro. php-runtime listet diese Interpreter unter Windows (`wsl:<distro>:<pfad>`).

## Socket-Protokoll

`tiegel --headless serve --listen ADDR` – Details und Flags in [headless.md](headless.md#socket-serve---listen).

1. Client verbindet sich mit `unix:/pfad` oder `tcp:127.0.0.1:PORT`.
2. Erste Anfrage: `{"jsonrpc":"2.0","id":1,"method":"auth","params":{"token":"…"}}` → `{"ok":true,"version":"…","apiVersion":"0.1"}`.
3. Danach dasselbe JSON-RPC 2.0 wie über stdio (NDJSON, eine Nachricht pro Zeile; `--framing lsp` möglich), inkl. `events.subscribe`, `$/cancelRequest`.
4. Events kommen als Notification `event` `{topic, payload}`: pro Verbindung jedes Event höchstens einmal, alle in Veröffentlichungsreihenfolge.

Mehrere Clients gleichzeitig sind erlaubt; jede Verbindung hat eigene Abos, alle sehen dieselben offenen Dokumente. Für interaktive Clients `--concurrent` verwenden (eine langsame LSP-Anfrage blockiert sonst die nächste); `docsync` serialisiert seine eigenen Edits selbst.

Nach dem Binden schreibt der Server eine Zeile JSON auf stdout: `{"listen":"tcp:127.0.0.1:54321","tokenFile":"/home/u/.config/Tiegel/serve.token","pid":1234}`.

## Sicherheit

- **Token-Pflicht** für jede Socket-Verbindung (256 Bit Zufall pro Start, konstanter Vergleich). Jede andere Anfrage vor `auth` oder ein falsches Token → Fehler `-32007` und Verbindungsabbruch; ohne Anmeldung nach 10 s Abbruch. Notifications vor der Anmeldung werden ignoriert.
- **Token-Quelle:** `$TIEGEL_TOKEN` oder Token-Datei mit Modus 0600 (Verzeichnis 0700), die beim Beenden gelöscht wird. `--token` nur für Tests (in der Prozessliste sichtbar).
- **Unix-Socket** mit Modus 0600; eine verwaiste Socket-Datei wird ersetzt, ein aktiver Socket oder eine normale Datei nicht.
- **TCP nur auf Loopback.** `0.0.0.0`, `::` oder andere Adressen nur mit `--allow-remote`, dann mit Warnung auf stderr – das Protokoll ist unverschlüsselt; für echten Remote-Zugriff einen SSH-Tunnel auf einen Unix-Socket verwenden.
- **Sauberes Ende:** SIGINT/SIGTERM schließen Listener und Verbindungen, entfernen Socket und Token-Datei, Exit 0.
- **Privacy:** Der Server öffnet nur, was der Nutzer startet; keine ausgehenden Verbindungen.

## Builds und cgo

| Paket | cgo nötig? |
|---|---|
| `internal/syntax` (Tree-sitter-Grammatiken in C) | ja, sonst Stub ohne Tree-sitter (`syntax.TreeSitter == false`, D80) |
| `internal/ui/...`, `cmd/tiegel-catalog` (Gio) | unter macOS/Linux ja, unter Windows nein |
| `cmd/tiegel` | unter macOS/Linux ja (bindet die Gio-GUI ein), mit `-tags headless` nein (D84) |
| alles andere (`internal/api/...`, `interp`, `headless`, `modules`, `app/gui/controller`) | nein |

- **macOS/Linux** (Release): nativ mit cgo – volle Funktion inkl. Tree-sitter.
- **Linux-Backend für WSL:** nativer Linux-Build mit cgo (CI-Linux-Job) und in die Distro installiert.
- **Windows-GUI:** `CGO_ENABLED=0 GOOS=windows go build ./cmd/tiegel` – ohne Tree-sitter; Highlighting über das Backend.
- **Headless-only (D84):** `go build -tags headless ./cmd/tiegel` lässt die GUI weg (kein Gio). So baut `cmd/tiegel` auch unter macOS/Linux ohne cgo, z.B. als Backend in Container/Server ohne X11/Wayland-Bibliotheken (dann ohne Tree-sitter; für das WSL-Backend mit cgo bauen). `tiegel` ohne `--headless` endet dort mit Exit 2 und dem Hinweis `use tiegel --headless <command>`.
- **Cross-Prüfung:** `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags headless ./cmd/tiegel` und `GOOS=windows GOARCH=amd64 go vet ./...` laufen von jedem Host aus. Ein Cross-Build *mit* Tree-sitter oder Gio für macOS/Linux bräuchte einen C-Cross-Compiler (z.B. zig cc) und ist bewusst nicht Teil der CI.

CI (`.github/workflows/ci.yml`, läuft auf `main`, `ci-*`-Branches, Pull Requests und per `workflow_dispatch`): macOS und Linux mit `make check` (cgo, `-race`); Windows mit `CGO_ENABLED=0` (`go build ./...`, `go vet ./...`, `go test` ohne `internal/ui` und `cmd/tiegel-catalog` – `internal/app/gui` läuft mit, Tree-sitter-abhängige Teile werden dort übersprungen –, Bau von `tiegel.exe` und `tiegel-catalog.exe`); ein Cross-Job ohne cgo: windows/amd64 und windows/arm64 bauen das volle Binary inkl. GUI und `go vet ./...`, linux/amd64, linux/arm64 und darwin/arm64 das Headless-only-Binary und `go vet -tags headless` für alle Nicht-Gio-Pakete.

### Windows-Besonderheiten (D85)

- **Absolute Pfade:** `/x` bzw. `\x` gilt als absolut (Wurzel des aktuellen Laufwerks) und wird nie an die Projektwurzel gehängt (`internal/core/fspath`). API-Clients sollten unter Windows vollständige Pfade (`C:\…`) oder projektrelative Pfade senden.
- **Zeilenenden:** Das Repository ist per `.gitattributes` LF-only. Tiegel selbst lässt `core.autocrlf` des Nutzers unangetastet: Gutter und Diffs vergleichen mit der Arbeitsbaum-Form (`git cat-file --filters`), die bei `autocrlf=true` CRLF hat – genau wie die ausgecheckten Dateien.
- **Tests:** Git-Testrepos setzen `core.autocrlf=false`; Fake-Programme als Shell-Skript laufen unter Windows nicht und sind dort mit Begründung übersprungen; selbst gebaute Test-Binaries bekommen `.exe`.

## Offene Punkte

- Die GUI (`internal/app`) nutzt `api.Caller`/`docsync` noch nicht; Umschaltung In-Process ↔ WSL in den Einstellungen fehlt.
- Kein automatischer Reconnect: Bei `client.ErrDisconnected` muss die GUI neu verbinden, Abos und Spiegel neu anlegen.
- Pfad-Mappings Windows ↔ WSL sind noch nicht in `runtime.pathMappings`/Debugger verdrahtet.
- Unix-Socket-Dateien unter Windows: verwaiste Sockets werden dort nicht erkannt (Go meldet sie nicht als Socket) – im Zweifel löschen oder TCP verwenden.
- Ein Linux-Release-Paket (AppImage/Tarball) und ein Windows-Installer fehlen noch.
