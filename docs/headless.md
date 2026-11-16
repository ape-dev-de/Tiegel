# Headless-Modus (`tiegel --headless`)

Tiegel läuft auch ohne Fenster: derselbe Kern (Event-Bus, Hooks, Module, Einstellungen, Local History, Autosave) und dieselbe API wie die GUI, nur ohne Gio. Grundlage ist Entscheidung **D41**: Jede Backend-Funktion ist zuerst über `internal/api` erreichbar; die GUI, die CLI und der JSON-RPC-Server sind nur Clients davon.

Einsatzzwecke:

- **Pipelines und Skripte** – `tiegel --headless compose --json | jq …`
- **Unit- und End-to-End-Tests** – die E2E-Tests bauen das Binary und rufen es mit `--headless` auf (`internal/headless/headless_test.go`).
- **Andere Clients** – `tiegel --headless serve` spricht JSON-RPC 2.0 über stdin/stdout (später auch per Socket, M9/WSL).

Der Headless-Pfad initialisiert kein Gio und öffnet kein Fenster; er funktioniert ohne Display (CI, SSH).

## Aufruf

```
tiegel --headless <befehl> [argumente] [flags]
```

Flags dürfen vor oder nach dem Befehl stehen (`--name wert` oder `--name=wert`), `--` beendet die Flag-Auswertung.

### Globale Flags

| Flag | Bedeutung |
|---|---|
| `--json` | maschinenlesbare Ausgabe (eingerücktes JSON auf stdout) |
| `--project DIR` | Projektverzeichnis. Projektbefehle (`info`, `files`, `interpreters`, `compose`, `lsp-servers`, `history`, `git-status`, `php-version`, `php-check`) nehmen ohne Angabe das Arbeitsverzeichnis. |
| `--config-dir DIR` | Einstellungsverzeichnis statt `<UserConfigDir>/Tiegel`; alternativ Umgebungsvariable `TIEGEL_CONFIG_DIR` (Tests, CI, portable Nutzung) |
| `--help`, `-h` | Hilfe |
| `--version` | Version (auch `tiegel --version` ohne `--headless`) |

### Exit-Codes

| Code | Bedeutung |
|---|---|
| 0 | Erfolg |
| 1 | Fehler bei der Ausführung (Datei fehlt, kein Projekt, Git-Fehler …); `search` ohne Treffer und `php-check` mit Befunden liefern ebenfalls 1 (wie `grep`/Linter) |
| 2 | Nutzungsfehler: unbekannter Befehl/Flag, fehlende Argumente, ungültige Parameter, unbekannte API-Methode |

Fehlermeldungen gehen immer nach stderr (`tiegel: …`), stdout bleibt für Daten frei.

### Befehle

| Befehl | Beschreibung | API-Methode(n) |
|---|---|---|
| `info` | Version, Konfigurationsverzeichnis, Projekt (Git, Composer, Compose-Dateien, gewählter Interpreter) | `app.info`, `project.info` |
| `version` | Version, Commit, Build-Datum | `app.version` |
| `update-check [--prereleases]` | Prüft auf eine neuere Version. **Netzwerkzugriff nur bei diesem expliziten Aufruf**, keine Kennungen, kein Download. | `update.check` |
| `methods` | alle API-Methoden mit Kurzbeschreibung | `app.methods` |
| `files [SUBDIR] [--tree] [--depth N] [--ignored] [--libraries]` | Projektdateien; `.gitignore` wird (grob) beachtet, `vendor/` und `node_modules/` werden markiert und nicht aufgeklappt | `files.list` |
| `read FILE` | Dateiinhalt ausgeben (binär unverändert) | `files.read` |
| `lang FILE...` | Sprache erkennen (php, json, yaml, xml, dotenv, plaintext) | `files.detectLanguage` |
| `search FILE PATTERN [--regex] [--case] [--word] [--limit N]` | Treffer als `datei:zeile:spalte: zeile` | `search.inDoc` |
| `interpreters` | lokale PHP-Binaries und Compose-Dienste; `*` = gewählt | `interp.list` |
| `interpreters select ID` | Interpreter fürs Projekt wählen (ohne Projekt: global) | `interp.select` |
| `interpreters version [ID] [--modules] [--ini]` | Version, Erweiterungen, Xdebug-INI-Werte | `interp.version` |
| `compose [DIR] [--profile P]` | PHP-Kandidaten mit Score, Begründungen, Pfad-Mappings, Xdebug-Hinweisen | `compose.detect` |
| `lsp-servers [--all]` | installierte Language Server (`--all` inkl. proprietärer) | `lsp.servers` |
| `modules [enable ID \| disable ID]` | Module anzeigen/schalten | `modules.*` |
| `history FILE [--show ID \| --restore ID]` | Local History: Liste, Inhalt einer Revision, Wiederherstellen | `history.*` |
| `git-status` | Branch und geänderte Dateien (`XY pfad`) | `git.status` |
| `php-version` | PHP-Version des Projekts und ihre Quelle | `php.version` |
| `php-check FILE... [--php VERSION]` | Features/Deprecations gegen die Projektversion prüfen | `php.check` |
| `debug-listen [--addr HOST:PORT] [--map HOST=REMOTE]...` | Xdebug-Listener starten (Standard `127.0.0.1:9003`) und Ereignisse zeilenweise ausgeben, bis Strg+C | `debug.listen`, Events `debug.*` |
| `serve [--framing ndjson\|lsp] [--concurrent]` | JSON-RPC 2.0 über stdin/stdout | alle |
| `call METHOD [JSON \| -]` | beliebige API-Methode aufrufen; `-` liest die Parameter von stdin; Ausgabe immer JSON | alle |
| `help [BEFEHL]` | Hilfe | – |

Jeder Befehl ist ein dünner Wrapper; alles, was ein Befehl kann, geht auch über `call` und `serve`.

## Beispiele für Pipelines

```sh
# Bester Compose-Kandidat und seine Mappings
tiegel --headless compose --json | jq -r '.candidates[0] | "\(.service) \(.mappings[0].remote)"'

# Alle Xdebug-Warnungen
tiegel --headless compose --json | jq -r '.candidates[].xdebugHints[] | select(.severity=="warning") | .message'

# Alle PHP-Dateien des Projekts (ohne vendor, .gitignore beachtet) prüfen
tiegel --headless files | grep '\.php$' | xargs tiegel --headless php-check

# Projektdateien als JSON-Baum, nur Pfade
tiegel --headless files --json | jq -r '.. | objects | select(.dir|not) | .path? // empty'

# Gewählten Interpreter und seine Version
tiegel --headless interpreters --json | jq -r '.[] | select(.selected) | .id' \
  | xargs tiegel --headless interpreters version

# Sprache vieler Dateien
find . -name '*.dist' | xargs tiegel --headless lang

# Generischer Aufruf
tiegel --headless call files.detectLanguage '{"path":"bin/console"}'
echo '{"path":"src/Kernel.php","pattern":"function","regex":false}' | tiegel --headless call search.inDoc -

# Debug-Ereignisse als JSON-Zeilen weiterverarbeiten
tiegel --headless debug-listen --json --map "$PWD=/var/www/html" | jq -c 'select(.topic=="debug.status")'
```

Einzelbefehle laufen jeweils in einem frischen Prozess: offene Dokumente leben nur innerhalb eines Prozesses. Für zusammenhängende Bearbeitungen (`doc.open` → `doc.insert` → `doc.save`) `serve` verwenden. Persistentes (Einstellungen, Interpreter-Wahl, Local History, Modulzustände) liegt im Konfigurationsverzeichnis und gilt prozessübergreifend.

## JSON-RPC-Protokoll (`serve`)

`tiegel --headless serve` spricht **JSON-RPC 2.0** über stdin/stdout.

- **Framing** `--framing ndjson` (Standard): eine JSON-Nachricht pro Zeile (NDJSON). Leere Zeilen werden ignoriert.
- **Framing** `--framing lsp`: `Content-Length`-Header wie beim Language Server Protocol (wiederverwendet aus `internal/lsp/jsonrpc2`).
- **Reihenfolge**: Standardmäßig werden Anfragen nacheinander in Eingangsreihenfolge bearbeitet – genau das, was Pipelines erwarten. `--concurrent` bearbeitet sie parallel (für interaktive Clients mit eigenen Versionsprüfungen).
- **Eingabeende**: Endet stdin, wartet der Server auf alle ausstehenden Antworten und liefert die zuletzt ausgelösten Events aus, bevor er sich beendet. `printf '…' | tiegel --headless serve` bekommt also jede Antwort.
- **Abbruch**: `$/cancelRequest` mit `{"id": …}` bricht eine laufende Anfrage ab (Kontext wird abgebrochen).
- `--project DIR` öffnet das Projekt vor der ersten Anfrage; sonst `project.open` aufrufen.

Beispielsitzung:

```sh
printf '%s\n' \
 '{"jsonrpc":"2.0","id":1,"method":"events.subscribe","params":{"topics":["doc.*"]}}' \
 '{"jsonrpc":"2.0","id":2,"method":"doc.open","params":{"path":"a.php"}}' \
 '{"jsonrpc":"2.0","id":3,"method":"doc.insert","params":{"path":"a.php","at":{"line":2,"col":1},"text":"// hi\n"}}' \
 '{"jsonrpc":"2.0","id":4,"method":"doc.save","params":{"path":"a.php"}}' \
 | tiegel --headless serve --project .
```

Antworten:

```json
{"jsonrpc":"2.0","id":3,"result":{"path":"a.php","version":1,"modified":true,"length":20,"lines":4,"language":"php","eol":"lf","canUndo":true,"canRedo":false}}
{"jsonrpc":"2.0","method":"event","params":{"topic":"doc.changed","payload":{"path":"a.php","version":1, …}}}
{"jsonrpc":"2.0","id":4,"result":{"path":"a.php","version":1,"modified":false, …,"size":20,"revision":1}}
```

### Fehler

Fehler sind JSON-RPC-Fehlerobjekte `{"code": …, "message": …}`:

| Code | Bedeutung | CLI-Exit |
|---|---|---|
| -32700 | ungültiges JSON | – |
| -32601 | Methode unbekannt | 2 |
| -32602 | ungültige Parameter (auch unbekannte Felder – Tippfehler werden nicht verschluckt) | 2 |
| -32000 | Ausführung fehlgeschlagen | 1 |
| -32001 | nicht gefunden (Datei, Dokument, Revision, Modul, Sitzung, Repository) | 1 |
| -32003 | kein Projekt geöffnet | 1 |
| -32004 | Versionskonflikt (`version` passt nicht zum Dokument) | 1 |
| -32005 | externes Programm/Dienst nicht verfügbar (php, git, Port belegt, Update-Server) | 1 |

### Events (Notifications)

Alles, was auf dem Event-Bus veröffentlicht wird, kann ein Client abonnieren:

```json
{"jsonrpc":"2.0","id":1,"method":"events.subscribe","params":{"topics":["doc.*","debug.*"]}}
```

Muster: exakte Topics, `präfix.*` oder `*`. Zustellung als Notification `event` mit `{"topic": …, "payload": …}`. Innerhalb eines Abos bleibt die Reihenfolge erhalten; zwischen Antworten und Events gibt es keine feste Reihenfolge (Events laufen asynchron).

Von der API veröffentlichte Topics: `project.opened`, `project.closed`, `doc.opened`, `doc.changed`, `doc.saved`, `doc.closed`, `interp.selected`, `modules.changed`, `git.committed`, `settings.changed`, `debug.listening`, `debug.stopped`, `debug.session.started`, `debug.session.ended`, `debug.status`, `debug.output`, `debug.notification`, `debug.breakpoint.resolved`.

## Methoden

Alle Methoden nehmen ein Parameterobjekt (oder keins) und liefern JSON. Relative Pfade beziehen sich auf das Projektverzeichnis (ohne Projekt: auf das Arbeitsverzeichnis). Pfade in Ergebnissen sind innerhalb des Projekts relativ mit `/`, sonst absolut.

**Positionen** (`Pos`): `{"offset": n}` (0-basierter Byte-Offset) oder `{"line": l, "col": c}` (1-basiert, Spalte in Zeichen/Code-Points; `col` weglassen = Zeilenanfang).

| Methode | Parameter | Ergebnis |
|---|---|---|
| `app.info` | – | Version, API-Version, Konfig-Verzeichnis, Projekt, Methodenzahl |
| `app.version` | – | `version`, `commit`, `date`, `dev`, `string` |
| `app.methods` | – | `[{name, summary}]` |
| `update.check` | `source?`, `prereleases?` | `current`, `available`, `latest`, `url`, `notes` |
| `events.subscribe` / `events.unsubscribe` | `topics: [..]` | aktive Muster |
| `events.publish` | `topic`, `payload?` | – |
| `project.open` | `path` | Projektinfo |
| `project.info` / `project.close` | – | Projektinfo |
| `files.list` | `path?`, `depth?`, `ignored?`, `libraries?`, `limit?` | Baum `{name, path, dir, size, ignored, library, collapsed, children}` + Zähler |
| `files.read` | `path`, `maxBytes?` | `content`, `encoding` (`utf-8`/`base64`), `language`, `size`, `truncated` |
| `files.detectLanguage` | `path`, `content?` | `language` |
| `doc.open` | `path`, `create?`, `text?` | Dokumentzustand (+ `text`, `alreadyOpen`) |
| `doc.close` | `path` | `discarded` (ungespeicherte Änderungen verworfen) |
| `doc.list` | – | Dokumentzustände |
| `doc.text` | `path`, `start?`, `end?` | `text`, `version` |
| `doc.insert` | `path`, `at`, `text`, `version?` | Dokumentzustand |
| `doc.delete` | `path`, `start`, `end`, `version?` | Dokumentzustand |
| `doc.replace` | `path`, `start?`, `end?`, `text`, `version?` (ohne Bereich: ganzes Dokument) | Dokumentzustand |
| `doc.undo` / `doc.redo` | `path` | Dokumentzustand + `done` |
| `doc.save` | `path` | Zustand + `size`, `revision` (Hooks `file.beforeSave`, Local History, `file.saved`) |
| `doc.saveAll` | – | `saved: [..]` |
| `history.list` | `path` | Revisionen (neueste zuerst) |
| `history.content` | `path`, `id` | `revision`, `text` |
| `history.restore` | `path`, `id` | neue Revision (offenes Dokument wird neu geladen) |
| `search.inDoc` | `path`, `pattern`, `regex?`, `caseSensitive?`, `wholeWord?`, `limit?` | Treffer mit `line`, `col`, `text`, `lineText` (offenes Dokument oder Datei) |
| `search.replaceInDoc` | wie `search.inDoc` + `replacement` | Zustand + `replaced` (ein Undo-Schritt) |
| `interp.list` | `compose?` | Interpreter `{id, label, kind, version, path, service, score, selected}` |
| `interp.select` | `id` | gewählter Interpreter |
| `interp.version` | `id?`, `modules?`, `ini?` | `version`, `modules`, `ini` |
| `compose.detect` | `dir?`, `profiles?` | `found`, `files`, `services`, `candidates[{service, score, reasons, image, mappings, xdebugHints}]` |
| `lsp.servers` | `all?` | installierte Server `{id, name, path, args, license}` |
| `lsp.known` | – | unterstützte Server |
| `debug.listen` | `addr?`, `mappings?: [{host, remote}]` | `listening`, `addr` |
| `debug.stop` | – | – |
| `debug.sessions` | – | Sitzungen `{id, file, ideKey, engine, status, …}` |
| `debug.continue` | `session?`, `action` (`run`, `stepInto`, `stepOver`, `stepOut`, `stop`, `detach`) | Status mit Datei/Zeile |
| `debug.stack` | `session?` | Stack-Frames |
| `debug.eval` | `session?`, `expr` | Property |
| `debug.setBreakpoint` | `session?`, `file`, `line`, `condition?` | Breakpoint |
| `modules.list` | – | `{id, dependsOn, enabled, running, error}` |
| `modules.enable` / `modules.disable` | `id`, `withDependencies?` | Modulzustand |
| `git.status` | `ignored?`, `noUntracked?`, `paths?` | Branch, Upstream, ahead/behind, `files[{path, index, worktree, staged, unstaged}]` |
| `git.diff` | `base?`, `target?`, `staged?`, `context?`, `paths?`, `raw?` | Dateien mit Hunks oder Rohtext |
| `git.log` | `rev?`, `all?`, `skip?`, `max?` (Standard 100), `path?`, `follow?` | Commits |
| `git.branches` | – | Branches |
| `git.commit` | `message`, `stage?`, `amend?`, `allowEmpty?`, `noVerify?`, `signOff?` | `hash` (speichert offene Dokumente vorher) |
| `php.version` | `override?`, `interpreter?`, `noInterpreter?` | `version`, `short`, `source`, `detail` |
| `php.check` | `path`, `version?` | `version`, `source`, `diagnostics[{feature, severity, message, line, col, …}]` |

`session` bei `debug.*` darf fehlen, wenn genau eine Sitzung aktiv ist.

## Erweitern: neue Namespaces

Neue Backend-Pakete registrieren nur einen Namespace in `internal/api` – CLI (`call`), `serve` und GUI sehen ihn sofort. Das Muster (ausführlich in `internal/api/doc.go`):

1. `internal/api/<namespace>.go` mit Param-/Ergebnis-Structs (JSON-Tags) und Handlern `func (a *App) xyz(ctx context.Context, p Params) (Result, error)`.
2. `func (a *App) registerXyz()` mit `mustHandle(a, "xyz.verb", "Kurzbeschreibung", a.xyz)` und Aufruf in `New`.
3. Erwartbare Fehler als `Errorf(CodeNotFound, …)` usw., Zustandsänderungen als Bus-Event.
4. Tests gegen `App.Call`, bei Bedarf ein bequemer CLI-Befehl in `internal/headless/commands.go`, Methode hier in die Tabelle.

Außerhalb des Pakets geht es ohne Änderung an `New` über `api.Options.Extensions` und `api.Handle`.
