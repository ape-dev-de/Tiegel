# Headless-Modus (`tiegel --headless`)

Tiegel läuft auch ohne Fenster: derselbe Kern (Event-Bus, Hooks, Module, Einstellungen, Local History, Autosave) und dieselbe API wie die GUI, nur ohne Gio. Grundlage ist Entscheidung **D41**: Jede Backend-Funktion ist zuerst über `internal/api` erreichbar; die GUI, die CLI und der JSON-RPC-Server sind nur Clients davon.

Einsatzzwecke:

- **Pipelines und Skripte** – `tiegel --headless compose --json | jq …`
- **Unit- und End-to-End-Tests** – die E2E-Tests bauen das Binary und rufen es mit `--headless` auf (`internal/headless/headless_test.go`).
- **Andere Clients** – `tiegel --headless serve` spricht JSON-RPC 2.0 über stdin/stdout oder – mit `--listen` – über einen Unix-Socket bzw. Loopback-TCP für mehrere Clients gleichzeitig (Windows-GUI ↔ WSL-Backend, siehe [plattformen.md](plattformen.md)).

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
| `--project DIR` | Projektverzeichnis. Projektbefehle (`info`, `files`, `interpreters`, `compose`, `lsp-servers`, `lsp-check`, `history`, `git-*`, `php-*`, `runtime-diagnose`, `mappings`, `breakpoints`) nehmen ohne Angabe das Arbeitsverzeichnis. |
| `--config-dir DIR` | Einstellungsverzeichnis statt `<UserConfigDir>/Tiegel`; alternativ Umgebungsvariable `TIEGEL_CONFIG_DIR` (Tests, CI, portable Nutzung) |
| `--help`, `-h` | Hilfe |
| `--version` | Version (auch `tiegel --version` ohne `--headless`) |

### Exit-Codes

| Code | Bedeutung |
|---|---|
| 0 | Erfolg |
| 1 | Fehler bei der Ausführung (Datei fehlt, kein Projekt, Git-Fehler …); `search` ohne Treffer, `php-check`/`lsp-check` mit Befunden und `runtime-diagnose` mit Warnungen liefern ebenfalls 1 (wie `grep`/Linter); ebenso Aufrufe eines abgeschalteten Moduls |
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
| `recovery [--dismiss [FILE...]]` | Wiederherstellungspunkte: ungespeicherte Änderungen, die beim Schließen nicht gespeichert werden konnten (bzw. beim Beenden noch offen waren), liegen in der Local History; `--dismiss` entfernt die Hinweise (ohne Dateien: alle), die Revisionen bleiben | `history.recovery`, `history.recoveryDismiss` |
| `git-status` | Branch und geänderte Dateien (`XY pfad`) | `git.status` |
| `php-version [--start]` | PHP-Version des Projekts und ihre Quelle; bei gestopptem Compose-Dienst Version aus `composer.json` mit Hinweis „Container läuft nicht“, `--start` erlaubt `docker compose run` | `php.version` |
| `php-check FILE... [--php VERSION]` | Features/Deprecations gegen die Projektversion prüfen | `php.check` |
| `debug-listen [--addr HOST:PORT] [--map HOST=REMOTE]...` | Xdebug-Listener starten (Standard `127.0.0.1:9003`; ohne `--map` Mappings aus php-runtime) und Ereignisse zeilenweise ausgeben, bis Strg+C | `debug.listen`, Events `debug.*` |
| `breakpoints [set DATEI:ZEILE [--condition AUSDRUCK] \| remove DATEI:ZEILE \| enable DATEI:ZEILE \| disable DATEI:ZEILE]` | Projekt-Breakpoints anzeigen (`pfad:zeile [disabled] if bedingung`) oder ändern; dauerhaft pro Projekt gespeichert, in jeder neuen Debug-Sitzung automatisch gesetzt | `debug.breakpoints.*` |
| `lsp-check FILE... [--server ID] [--command PATH] [--timeout DUR]` | startet den Language Server (Erkennung/Einstellungen, Phpactor Standard, oder `--command`), öffnet die Dateien, wartet auf die Diagnosen (Standard 10 s) und gibt sie als `datei:zeile:spalte: schwere: meldung [quelle code]` aus; Exit 1 bei Fehlern/Warnungen oder Timeout | `lsp.start`, `doc.open`, Event `lsp.diagnostics` |
| `git-commit -m MSG [--amend] [--push] [DATEI...]` | Commit (offene Dokumente werden vorher gespeichert); mit DATEIEN nur diese (unabhängig vom Index), sonst der Index; `--push` pusht danach (Exit 1, wenn der Push scheitert – mit Fehlerart) | `git.commitSelection`, `git.commit`, `git.push` |
| `git-diff [DATEI...] [--staged] [--base REV]` | Unified Diff (Arbeitsbaum ↔ Index, `--staged` Index ↔ HEAD, `--base` gegen Revision); `--json` geparst | `git.diff` |
| `git-push [--remote R]` | aktuellen Branch pushen (Netzwerkzugriff nur hier) | `git.push` |
| `git-file-diff DATEI [--base V] [--target V] [--base-path PFAD] [--ignore-whitespace]` | Änderungsblöcke einer Datei zwischen zwei Versionen (`V` = `HEAD`, `index`, `working`, `empty` oder Revision; Standard `HEAD` ↔ Arbeitsbaum) als `@@ art left … right … @@` mit `-`/`+`-Zeilen; `--json` mit Volltexten und Wort-Bereichen | `git.fileDiff` |
| `git-history DATEI [--skip N] [--max N] [--rev R] [--no-follow]` | Commits, die DATEI geändert haben (folgt Umbenennungen): `hash datum autor status pfad betreff` | `git.fileHistory` |
| `git-blame DATEI [--rev R] [--ignore-whitespace]` | jede Zeile mit Commit, Autor, Datum (`00000000 (Not committed …)` für ungespeicherte/uncommittete Zeilen) | `git.blame` |
| `git-compare BASIS [ZIEL]` | geänderte Dateien zwischen zwei Revisionen (ohne ZIEL: Arbeitsbaum) als `status pfad` | `git.compareRevisions` |
| `runtime-diagnose [INTERPRETER-ID]` | Xdebug-Einrichtung des (gewählten) Interpreters: INI-Werte `xdebug.mode/client_host/client_port`, Exec-Modus, Pfad-Mappings, Hinweise; Exit 1 bei Warnungen | `runtime.diagnose` |
| `mappings [set HOST=REMOTE... \| reset] [--exec-mode auto\|exec\|run]` | Pfad-Mappings anzeigen oder dauerhaft korrigieren (`reset`: wieder aus Compose); Exec-Modus für Compose-Interpreter | `runtime.mappings.*`, `runtime.execMode` |
| `php-override [VERSION \| clear]` | dauerhaften PHP-Versions-Override setzen/entfernen, danach Version und Quelle | `php.setVersionOverride`, `php.version` |
| `highlights DATEI [--lines VON:BIS] [--language L]` | Syntax-Highlighting als `start ende rolle "text"` (Byte-Offsets, ohne `plain`-Bereiche) | `syntax.highlights` |
| `folds DATEI [--language L]` | Faltbereiche als `von-bis` (Zeilen, 1-basiert) | `syntax.folds` |
| `outline DATEI [--language L]` | Struktur (Namespaces, Klassen, Member, Funktionen) eingerückt als `zeile:spalte modifier art name signatur` | `syntax.outline` |
| `serve [--framing ndjson\|lsp] [--concurrent] [--listen ADDR [--token-file F] [--token T] [--allow-remote]]` | JSON-RPC 2.0 über stdin/stdout, mit `--listen unix:PFAD` oder `tcp:127.0.0.1:PORT` über einen Socket (Token-Authentisierung, siehe unten) | alle |
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

# Language-Server-Diagnosen in CI (Exit 1 bei Fehlern/Warnungen)
git diff --name-only HEAD~1 -- '*.php' | xargs tiegel --headless lsp-check --timeout 30s

# Nur ausgewählte Dateien committen und pushen
tiegel --headless git-commit -m "Fix" --push src/Kernel.php

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
- `auth` (`{token}` → `{ok, version, apiVersion}`) beantwortet jeder Transport; über stdio ist es ohne Wirkung.

### Socket (`serve --listen`)

```
tiegel --headless serve --listen unix:$XDG_RUNTIME_DIR/tiegel.sock --concurrent
tiegel --headless serve --listen tcp:127.0.0.1:7420 --token-file ~/.config/Tiegel/serve.token
```

- Mehrere Clients gleichzeitig; jede Verbindung ist eine eigene Sitzung mit eigenen Event-Abos. Alle teilen dieselben offenen Dokumente.
- **Authentisierung:** Die erste Anfrage jeder Verbindung muss `auth` mit dem Token sein. Jede andere Anfrage davor und ein falsches Token liefern `-32007` und schließen die Verbindung; ohne erfolgreiche Anmeldung wird sie nach 10 s geschlossen. Vor der Anmeldung sind Nachrichten auf 4 KiB begrenzt (größere schließen die Verbindung); höchstens 64 Verbindungen gleichzeitig, weitere werden sofort geschlossen.
- **Langsame Clients:** Liest ein Client seine Events nicht mehr, wird er nach 10 000 wartenden Events getrennt (statt unbegrenzt Speicher zu belegen).
- **Token:** `--token T` ist **in der Prozessliste (`ps`) für alle Benutzer sichtbar** – nur für Tests; sonst `$TIEGEL_TOKEN`, sonst ein zufälliges 256-Bit-Token, das in `--token-file` (Standard `<Konfigurationsverzeichnis>/serve.token`, Modus 0600) geschrieben und beim Beenden gelöscht wird – nur, wenn die Datei noch das eigene Token enthält (ein zweiter Server mit derselben Token-Datei behält seine).
- **Adressen:** Unix-Sockets werden mit Modus 0600 angelegt (verwaiste Socket-Dateien werden ersetzt, belegte nicht). TCP nur auf Loopback (`127.0.0.1`, `::1`, `localhost`); andere Adressen (auch `0.0.0.0`) nur mit `--allow-remote` und Warnung – die Verbindung ist **unverschlüsselt**.
- **Start/Ende:** Nach dem Binden schreibt der Server eine JSON-Zeile auf stdout (`{"listen":"tcp:127.0.0.1:54321","tokenFile":"…","pid":…}`; bei Port 0 mit dem echten Port). SIGINT/SIGTERM beenden ihn sauber: keine neuen Verbindungen, offene werden geschlossen, Socket- und Token-Datei entfernt, Exit 0.
- Go-Clients nutzen `internal/api/client` (`Dial`, `Spawn`), siehe [plattformen.md](plattformen.md).

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
| -32004 | Konflikt: Versionskonflikt (`version` passt nicht zum Dokument), `doc.close` mit ungespeicherten Änderungen ohne `discard` oder mit veralteter `generation`, Hunk-Auswahl passt nicht mehr zum Diff (`header`/`hash`), `git.commitSelection` während Merge/Rebase/Cherry-pick/Revert | 1 |
| -32005 | externes Programm/Dienst nicht verfügbar (php, git, Language Server, Port belegt, Update-Server) | 1 |
| -32006 | Modul abgeschaltet (`module disabled`; `data: {module, method}`) – mit `modules.enable` wieder einschalten | 1 |
| -32007 | nicht angemeldet / falsches Token (nur Socket-Verbindungen; die Verbindung wird geschlossen) | – |

Fehlerobjekte können `data` tragen (z.B. `git.push`: `{kind, message, stderr}`).

### Events (Notifications)

Alles, was auf dem Event-Bus veröffentlicht wird, kann ein Client abonnieren:

```json
{"jsonrpc":"2.0","id":1,"method":"events.subscribe","params":{"topics":["doc.*","debug.*"]}}
```

Muster: exakte Topics, `präfix.*` oder `*`. Zustellung als Notification `event` mit `{"topic": …, "payload": …}`. Jedes Event kommt pro Verbindung höchstens einmal (auch wenn mehrere Muster passen) und alle Events einer Verbindung in Veröffentlichungsreihenfolge; zwischen Antworten und Events gibt es keine feste Reihenfolge (Events laufen asynchron).

Vom Kern veröffentlichte Topics: `project.opened`/`project.closed` (`{root, name}`), `file.opened` (`{path, file, language, version, size}`), `file.saved` (`{path, file, trigger, size}` – auch bei Autosave), `doc.opened`, `doc.changed`, `doc.edited`, `doc.saved`, `doc.closed`, `modules.changed`, `settings.changed`.

`doc.edited` beschreibt **jede** Pufferänderung – egal ob über `doc.*`, Undo, Autosave-Filter oder direkt in der GUI – im Format `{path, file, language, version, start, oldEnd, newEnd, startPoint, oldEndPoint, newEndPoint, range: {start: {line, character}, end: …}, text, oldText}`: Byte-Offsets und Zeile/Byte-Spalte (Tree-sitter) sowie der ersetzte Bereich in LSP-Koordinaten (0-basiert, UTF-16). `version` steigt pro Änderung um 1.

Von den Modulen veröffentlichte Topics:

| Modul | Topics |
|---|---|
| php-runtime | `interpreter.changed` (`{id, label, kind, previous}`), `interp.selected`, `runtime.mappingsChanged` (`{mappings, source}`) |
| php-features | `php.diagnostics` (`{path, file, version, phpVersion, source, diagnostics}`), `php.versionChanged` (`{override}`) |
| lang-php | `lsp.status` (gestartet/gestoppt/abgestürzt), `lsp.diagnostics` (`{path, file, version, diagnostics}`) |
| git | `git.changed` (`{op, paths}` nach jeder Änderung über die API), `git.committed` |
| debug-xdebug | `debug.listening`, `debug.stopped`, `debug.sessionStarted`, `debug.sessionEnded`, `debug.break` (gemappte Host-Datei/Zeile), `debug.breakpointHit`, `debug.breakpoints.changed`, `debug.status`, `debug.output`, `debug.notification`, `debug.breakpoint.resolved` |

## Module

Die Funktionen außerhalb des Kerns stellen Module bereit (`internal/modules/*`, ohne Gio). Sie registrieren ihre Methoden beim Start und sprechen untereinander nur über Bus-Topics und Hooks:

| Modul | Abhängig von | Namespaces | Hooks |
|---|---|---|---|
| `php-runtime` | – | `interp.*`, `compose.*`, `php.version`, `runtime.*` | stellt `php.resolveVersion` und `runtime.pathMappings` (Filter) bereit |
| `php-features` | `php-runtime` | `php.check`, `php.diagnostics`, `php.setVersionOverride` | nutzt `php.resolveVersion` |
| `lang-php` | (`php-runtime` optional) | `lsp.*` | nutzt `php.resolveVersion` |
| `git` | – | `git.*` | ruft Guard `commit.before` (Kern: Autosave) und Filter `commit.message` |
| `syntax` | – | `syntax.*` | – (folgt `doc.edited`) |
| `debug-xdebug` | `php-runtime` | `debug.*` | ruft Guard `debug.before` (Kern: Autosave), nutzt `runtime.pathMappings` |

`modules.enable`/`modules.disable` wirken sofort (und dauerhaft): ein abgeschaltetes Modul räumt Hooks und Abos ab, seine Methoden antworten mit `-32006 module disabled`. `app.methods` zeigt zu jeder Methode `module` und `disabled`.

Hintergrundarbeit: php-features prüft offene PHP-Dokumente nach jeder Änderung (entprellt, inkrementelles Tree-sitter-Update aus `doc.edited`) und veröffentlicht `php.diagnostics`. lang-php hält einen gestarteten Language Server automatisch synchron: `doc.open` → didOpen, jede Änderung (auch direkt am Puffer) → didChange (inkrementell, UTF-16), `doc.close` → didClose, Speichern → didSave; bei `interpreter.changed` und `php.versionChanged` startet der Server neu.


## Methoden

Alle Methoden nehmen ein Parameterobjekt (oder keins) und liefern JSON. Relative Pfade beziehen sich auf das Projektverzeichnis (ohne Projekt: auf das Arbeitsverzeichnis). Pfade in Ergebnissen sind innerhalb des Projekts relativ mit `/`, sonst absolut.

**Positionen** (`Pos`): `{"offset": n}` (0-basierter Byte-Offset) oder `{"line": l, "col": c}` (1-basiert, Spalte in Zeichen/Code-Points; `col` weglassen = Zeilenanfang).

| Methode | Parameter | Ergebnis |
|---|---|---|
| `app.info` | – | Version, API-Version, Konfig-Verzeichnis, Projekt, Methodenzahl |
| `app.version` | – | `version`, `commit`, `date`, `dev`, `string` |
| `app.methods` | – | `[{name, summary, module, disabled}]` |
| `update.check` | `source?`, `prereleases?` | `current`, `available`, `latest`, `url`, `notes` |
| `events.subscribe` / `events.unsubscribe` | `topics: [..]` | aktive Muster |
| `events.publish` | `topic`, `payload?` | – |
| `project.open` | `path` | Projektinfo |
| `project.info` / `project.close` | – | Projektinfo |
| `files.list` | `path?`, `depth?`, `ignored?`, `libraries?`, `limit?` | Baum `{name, path, dir, size, ignored, library, collapsed, children}` + Zähler |
| `files.read` | `path`, `maxBytes?` | `content`, `encoding` (`utf-8`/`base64`), `language`, `size`, `truncated` |
| `files.detectLanguage` | `path`, `content?` | `language` |
| `doc.open` | `path`, `create?`, `text?` | Dokumentzustand (+ `text`, `alreadyOpen`); jeder Dokumentzustand enthält `generation` (eindeutig pro geöffneter Instanz, neu nach Schließen + Öffnen) |
| `doc.close` | `path`, `generation?`, `discard?` | `path`, `generation`, `discarded`; mit ungespeicherten Änderungen nur mit `discard: true` (sonst -32004, `data` = Dokumentzustand); mit `generation` nur, wenn sie zur offenen Instanz passt (sonst -32004 – ein verspätetes Close trifft kein neu geöffnetes Dokument) |
| `doc.list` | – | Dokumentzustände |
| `doc.text` | `path`, `start?`, `end?` | `text`, `version` |
| `doc.insert` | `path`, `at`, `text`, `version?` | Dokumentzustand |
| `doc.delete` | `path`, `start`, `end`, `version?` | Dokumentzustand |
| `doc.replace` | `path`, `start?`, `end?`, `text`, `version?` (ohne Bereich: ganzes Dokument) | Dokumentzustand |
| `doc.undo` / `doc.redo` | `path` | Dokumentzustand + `done` |
| `doc.save` | `path` | Zustand + `size`, `revision` (Hooks `file.beforeSave`, Local History, `file.saved`); der Inhalt wird erst unter der Speicher-Sperre gelesen, ein älterer Stand überschreibt nie einen neueren Autosave |
| `doc.saveAll` | – | `saved: [..]` |
| `history.list` | `path` | Revisionen (neueste zuerst) |
| `history.content` | `path`, `id` | `revision`, `text` |
| `history.restore` | `path`, `id` | neue Revision (offenes Dokument wird neu geladen; unter der Speicher-Sperre) |
| `history.recovery` | – | `[{path, file, revision, time, reason, error}]` – Wiederherstellungspunkte (neueste zuerst): ungespeicherte Änderungen, die beim Schließen nicht gespeichert werden konnten (`autosave.trigger` mit `close`) oder beim Beenden/`project.close` noch offen waren; Inhalt über `history.content` |
| `history.recoveryDismiss` | `paths?` (ohne: alle) | verbleibende Wiederherstellungspunkte (die Revisionen bleiben in der Local History) |
| `autosave.trigger` | `trigger?` (`focusLost` Standard, `idle`, `beforeRun`, `beforeDebug`, `beforeCommit`, `close`) | `saved: [..]` – alle geänderten Dokumente über Autosave (Hooks, Local History mit Label `autosave: <trigger>`); mit `close` werden nicht speicherbare Dokumente als Wiederherstellungspunkt gesichert, der Fehler (-32000) nennt das und trägt die Punkte als `data` |
| `autosave.config` | `idleSeconds?` (0 = aus) | `idleSeconds` (dauerhaft in den globalen Settings; jede Pufferänderung startet den Leerlauf-Timer neu) |
| `settings.get` | `key`, `scope?` (`global`, `project`) | `key`, `scope`, `found`, `value` |
| `settings.set` | `key` (nur `ui.*`, `editor.*`, `update.*`), `scope?`, `value` (`null` löscht) | wie `settings.get` |
| `search.inDoc` | `path`, `pattern`, `regex?`, `caseSensitive?`, `wholeWord?`, `limit?` | Treffer mit `line`, `col`, `text`, `lineText` (offenes Dokument oder Datei) |
| `search.replaceInDoc` | wie `search.inDoc` + `replacement` | Zustand + `replaced` (ein Undo-Schritt) |
| `modules.list` | – | `{id, dependsOn, enabled, running, error}` |
| `modules.enable` / `modules.disable` | `id`, `withDependencies?` | Modulzustand (wirkt sofort) |

`version` bei `doc.insert`/`doc.delete`/`doc.replace` ist optimistisches Sperren: Ist es angegeben (auch `0`), muss es der aktuellen Dokumentversion entsprechen, sonst `-32004` mit `data: {version}` (aktuelle Version). Darauf baut der Dokument-Spiegel `internal/api/docsync` für entfernte Clients auf.

**php-runtime**

| Methode | Parameter | Ergebnis |
|---|---|---|
| `interp.list` | `compose?` | Interpreter `{id, label, kind, version, path, service, score, selected}` |
| `interp.select` | `id` | gewählter Interpreter (Events `interpreter.changed`, `interp.selected`) |
| `interp.version` | `id?`, `modules?`, `ini?` | `version`, `modules`, `ini` |
| `compose.detect` | `dir?`, `profiles?` | `found`, `files`, `services`, `candidates[{service, score, reasons, image, mappings, xdebugHints}]` |
| `php.version` | `override?`, `interpreter?`, `noInterpreter?`, `start?` | `version`, `short`, `source` (`override`, `interpreter`, `composer …`), `detail`, `hint?`; ohne `override` gilt der gespeicherte Override; bei Compose-Interpretern kommt die Version per `docker compose exec` aus dem **laufenden** Container – ohne `start: true` wird nie ein Container gestartet (kein `run`, kein Image-Pull); läuft er nicht, Version aus `composer.json` mit `hint: {code: "container-not-running"}` |
| `runtime.mappings.get` | – | `mappings[{host, remote}]`, `source` (`settings`, `compose`, `none`), `service` |
| `runtime.mappings.set` | `mappings` oder `reset: true` | wie `get` (dauerhaft pro Projekt; Event `runtime.mappingsChanged`) |
| `runtime.execMode` | `mode?` (`auto`, `exec`, `run`) | `mode` – wie PHP im Compose-Dienst läuft (`docker compose exec`/`run --rm`) |
| `runtime.diagnose` | `interpreter?`, `start?` (wie bei `php.version`; ohne nur `exec`, bei gestopptem Dienst Hinweis `container-not-running`) | `interpreter`, `version`, `ini` (`xdebug.mode`, `xdebug.client_host`, `xdebug.client_port`, `xdebug.start_with_request`), `execMode`, `mappings`, `hints[{severity, code, message}]` (Compose-Hinweise + INI-Prüfung) |

**php-features**

| Methode | Parameter | Ergebnis |
|---|---|---|
| `php.check` | `path`, `version?` | `version`, `source`, `diagnostics[{feature, severity, message, start, end, line, col, endLine, endCol, text}]` (offenes Dokument oder Datei) |
| `php.diagnostics` | `path` | letzte Hintergrund-Diagnosen des offenen Dokuments (`{path, version, phpVersion, source, diagnostics}`) |
| `php.setVersionOverride` | `version` (leer = entfernen) | `override` (dauerhaft; Event `php.versionChanged`) |

**lang-php** – Bereiche in Ergebnissen sind `{start, end}` mit `{offset, line, col}` (1-basiert, Spalte in Zeichen); die Umrechnung nach UTF-16 erfolgt intern.

| Methode | Parameter | Ergebnis |
|---|---|---|
| `lsp.servers` | `all?` | installierte Server `{id, name, path, args, license}` |
| `lsp.known` | – | unterstützte Server |
| `lsp.start` | `server?`, `command?`, `args?` (ohne Angabe: Einstellungen `lsp.server`/`lsp.command`/`lsp.args`, sonst erkannter Server, Phpactor bevorzugt; PHP-Version über Hook `php.resolveVersion`) | Status `{running, server, name, command, args, phpVersion, openDocs, serverInfo, generation}` |
| `lsp.stop` / `lsp.status` | – | Status |
| `lsp.diagnostics` | `path?` (ohne: alle) | `[{path, file, version, diagnostics[{severity, message, source, code, start, end, line, col, endLine, endCol}]}]` |
| `lsp.completion` | `path`, `at` (Pos) | `{incomplete, items[{label, kind, detail, documentation, insertText, snippet, edit{range, newText}}]}` |
| `lsp.hover` | `path`, `at` | `{found, kind, contents, range?}` |
| `lsp.signature` | `path`, `at` | `{signatures[{label, documentation, parameters[{label, documentation}]}], activeSignature, activeParameter}` |
| `lsp.definition` / `lsp.implementation` | `path`, `at` | `[{path, file, range}]` |
| `lsp.semanticTokens` | `path` | `[{range, type, modifiers}]` |

**git** – Hunk-Indizes beziehen sich auf `git.diff` mit denselben Optionen. Jeder Hunk in `git.diff` hat `header` und `hash` (Inhalts-Hash); eine Auswahl `{hunk, header?, hash?}` mit diesen Werten wird abgelehnt (-32004 mit `data: {reason: "staleSelection", path, hunk}`), wenn sich der Diff inzwischen geändert hat (z.B. durch Formatierung beim Speichern) – statt still einen anderen Hunk zu nehmen; der Client lädt dann den Diff neu. Ohne `header`/`hash` gilt nur der Index (rückwärtskompatibel). Clients sollten beide mitschicken.

| Methode | Parameter | Ergebnis |
|---|---|---|
| `git.status` | `ignored?`, `noUntracked?`, `paths?` | Branch, Upstream, ahead/behind, `files[{path, index, worktree, staged, unstaged}]` |
| `git.diff` | `base?`, `target?`, `staged?`, `context?`, `paths?`, `raw?` | Dateien mit Hunks (`header`, `hash`, `lines`) oder Rohtext |
| `git.log` | `rev?`, `all?`, `skip?`, `max?` (Standard 100), `path?`, `follow?` | Commits |
| `git.branches` | – | Branches |
| `git.commit` | `message`, `stage?`, `amend?`, `allowEmpty?`, `noVerify?`, `signOff?` | `hash` (Guard `commit.before` speichert offene Dokumente, Filter `commit.message`) |
| `git.stage` / `git.unstage` | `paths?` oder `path` + `hunks?: [{hunk, lines?, header?, hash?}]` (Indizes aus `git.diff`: stage ohne Basis, unstage mit `staged`) | Status |
| `git.discard` | `path`, `hunks` oder `all: true` | `path`, `reloaded` (offenes, ungeändertes Dokument neu geladen, ein Undo-Schritt) |
| `git.commitSelection` | `message`, `paths?`, `hunks?: [{path, hunks}]` (Indizes aus `git.diff` mit `base: "HEAD"`), `amend?`, `push?`, `noVerify?`, `signOff?`, `allowEmpty?` | `hash`, `message`, `pushed`, `push?`, `pushError?: {kind, message}`; Guard `commit.before` (Autosave; schlägt das Speichern fehl, läuft es in den Timeout oder stürzt es ab, wird der Commit abgelehnt), Filter `commit.message`. Während Merge/Rebase/Cherry-pick/Revert/am abgelehnt (-32004; `git.commit` committet den ganzen Index). Nur-gestagte Inhalte berührter Dateien bleiben im Index erhalten |
| `git.push` | `remote?`, `branch?`, `remoteBranch?`, `setUpstream?`, `forceWithLease?`, `tags?` | `remote`, `branch`, `remoteBranch`; Fehler -32000 mit `data: {kind, message, stderr}`, `kind` = `rejected`, `remoteRejected`, `auth`, `network`, `repoNotFound`, `noRemote`, `failed` |
| `git.recentMessages` | `max?` (20) | Nachrichten, neueste zuerst |
| `git.gutter` | `path`, `base?` (`head`/`index`) | `ranges[{kind, start, end, baseStart, baseEnd}]` (1-basiert, `end` exklusiv; gelöscht: `start == end`), aus dem Pufferinhalt |
| `git.rollback` | `path`, `line`, `base?` | Dokumentzustand (Änderung als Undo-fähige Bearbeitung des offenen Dokuments) |
| `git.show` | `rev?` (HEAD), `path` | `content`, `encoding` (`utf-8`/`base64`) |
| `git.checkout` | `ref` | Status (offene, ungeänderte Dokumente werden neu geladen) |
| `git.createBranch` / `git.deleteBranch` | `name`, `start?`, `checkout?` / `force?` | Branches |
| `git.conflicts` | – | Dateien mit Konflikt |
| `git.mergeData` | `path` | `base`, `ours`, `theirs`, `has*`, `blocks[{index, kind, conflict, base, ours, theirs, *Start}]`, `merged` (mit Markern), `conflicts` |
| `git.resolve` | `path`, `content?` oder `resolutions: [{block, resolution: ours\|theirs\|oursFirst\|theirsFirst\|base\|custom, custom?}]` | `content`, `reloaded` |
| `git.fileDiff` | `path`, `base?` (`HEAD`), `target?` (`working`), `basePath?` (Pfad auf der Basisseite nach Umbenennung), `ignoreWhitespace?`, `noInline?`; Versionen: `working` (offenes Dokument bzw. Datei), `index`, `empty`, Revision | `left`/`right`: `{version, hash?, label, path, exists, binary?, text, open?}` (Volltexte, mit Arbeitsbaum-Filtern), `blocks[{kind: added\|deleted\|modified, leftStart, leftEnd, rightStart, rightEnd, leftInline?, rightInline?: [{line, ranges[{start, end}]}]}]` (1-basiert, Ende exklusiv; Wort-/Zeichenbereiche als Byte-Spalten der Zeile), `identical` |
| `git.fileHistory` | `path`, `skip?`, `max?` (100), `rev?`, `noFollow?` | `commits[{hash, parents, author, authorEmail, date, subject, refs?, path, oldPath?, status}]` (Pfad der Datei im Commit, `oldPath` im Elterncommit nach Umbenennung), `more` |
| `git.blame` | `path`, `rev?` (ohne: aktueller Inhalt, offenes Dokument inkl. ungespeicherter Änderungen), `ignoreWhitespace?` | `lines[{line, hash, origLine}]`, `commits[{hash, author, authorEmail, date, summary, path, previous?, prevPath?, boundary?, uncommitted?}]` (neueste zuerst; nicht committete Zeilen: Hash aus 40 Nullen) |
| `git.compareRevisions` | `base?` (`HEAD`), `target?` (leer/`working` = Arbeitsbaum) | `files[{path, oldPath?, status: A\|M\|D\|R\|C\|T\|U, similarity?}]` |

**debug-xdebug**

| Methode | Parameter | Ergebnis |
|---|---|---|
| `debug.listen` | `addr?`, `mappings?: [{host, remote}]` (ohne: Mappings aus php-runtime, folgt `runtime.mappingsChanged`) | `listening`, `addr`, `mappings`; vorher Guard `debug.before` (speichert offene Dokumente) |
| `debug.stop` | – | Listener beenden (alle Sitzungen enden) |
| `debug.sessions` | – | Sitzungen `{id, file, ideKey, engine, status, …}` |
| `debug.run` / `debug.stepInto` / `debug.stepOver` / `debug.stepOut` / `debug.stopSession` / `debug.detach` | `session?` | Status mit Host-Datei/Zeile |
| `debug.continue` | `session?`, `action` (`run`, `stepInto`, `stepOver`, `stepOut`, `stop`, `detach`) | Status mit Datei/Zeile |
| `debug.stack` | `session?` | Stack-Frames |
| `debug.eval` | `session?`, `expr` | Property |
| `debug.variables` | `session?`, `depth?`, `context?`, `fullname?`, `page?`, `maxData?` | `contexts`, `variables[{name, fullname, type, value, numChildren, children}]`, `parent`, `hasMore` (Kinder per `fullname`, seitenweise) |
| `debug.watches` / `debug.watches.set` | `session?` / `expressions` | ausgewertete Watches `[{expr, value, error}]` / gespeicherte Liste |
| `debug.setBreakpoint` | `session?`, `file`, `line`, `condition?` | Breakpoint nur in dieser Sitzung |
| `debug.breakpoints.list` | – | `breakpoints[{id, path, line, condition, enabled}]` |
| `debug.breakpoints.set` | `file`, `line`, `condition?`, `enabled?` | Liste + `changed` (dauerhaft pro Projekt, wirkt sofort in laufenden Sitzungen, neue Sitzungen bekommen alle aktiven Breakpoints) |
| `debug.breakpoints.remove` | `file`+`line` oder `id` | Liste + `changed` |
| `debug.breakpoints.enable` | `file`+`line` oder `id`, `enabled` | Liste + `changed` |

**syntax** – Highlighting, Faltbereiche und Struktur vom Backend, v.a. für Clients ohne Tree-sitter (Windows-UI ohne cgo gegen ein WSL-Backend, D80). Quelle ist ein offenes Dokument (inkrementell aus `doc.edited` geparst), sonst die Datei – oder `text` (Sprache aus `language` bzw. der Endung von `path`). `language`: `php`, `json`, `yaml`, `xml`, `dotenv`, `plaintext`. Ergebnisse enthalten immer `path`, `language` (`php`, `json`, `yaml`, `xml`, `env`, `""`), `version` (Pufferversion, 0 bei Dateien), `open`, `length`. Ohne Tree-sitter (Build ohne cgo) liefern PHP/JSON/YAML/XML `-32005`.

| Methode | Parameter | Ergebnis |
|---|---|---|
| `syntax.highlights` | `path?`, `text?`, `language?`, Bereich `start?`/`end?` (Byte-Offsets) oder `startLine?`/`endLine?` (1-basiert, inklusiv), `plain?` (auch ungefärbte Bereiche) | `start`, `end`, `spans[{start, end, role}]` – Rollen: `keyword`, `string`, `number`, `comment`, `variable`, `function`, `type`, `constant`, `operator`, `punctuation`, `tag`, `attribute`, `property` (`plain`) |
| `syntax.folds` | `path?`, `text?`, `language?` | `folds[{startLine, endLine}]` (1-basiert, inklusiv) |
| `syntax.outline` | `path?`, `text?`, `language?` | `symbols[{kind, name, detail, modifiers, start, end, nameStart, children}]` mit Positionen `{offset, line, col}` (1-basiert, Spalte in Zeichen); `kind`: `namespace`, `class`, `interface`, `trait`, `enum`, `enum case`, `method`, `function`, `property`, `constant` (nur PHP) |

`session` bei `debug.*` darf fehlen, wenn genau eine Sitzung aktiv ist.

## Erweitern: neue Namespaces

Neue Funktionen kommen entweder in den Kern (`internal/api`, `mustHandle` in `New`) oder – der Normalfall – in ein Modul unter `internal/modules/<name>` (ausführlich in `internal/api/doc.go`):

1. Param-/Ergebnis-Structs (JSON-Tags) und Handler `func (m *Module) xyz(ctx context.Context, p Params) (Result, error)`.
2. In `Start`: `api.AppFrom(ctx)` für den Kernzugriff, Methoden mit `api.Provide`/`api.Add`, Hooks mit `hooks.WithOwner(ctx.ModuleID())`, Bus-Abos über `ctx.OnStop(bus.Subscribe(...))`. `Claims()` meldet die Methoden an, damit sie bei abgeschaltetem Modul `module disabled` liefern.
3. Erwartbare Fehler als `api.Errorf(api.CodeNotFound, …)` (bei Bedarf `.WithData(...)`), Zustandsänderungen als Bus-Event, gemeinsame Daten über Topics/Hooks aus `internal/api/contract.go` – nie direkte Aufrufe anderer Module.
4. Dokument-Inhalte immer über `App.Document`/`Doc.Snapshot` bzw. `doc.edited` lesen; Bearbeitungen über `App.EditDocument` (Undo-fähig).
5. Tests mit `internal/modules/modtest`, bei Bedarf ein CLI-Befehl in `internal/headless/cmd_<bereich>.go` (eigene Flags per `init` in `flagSpec`), Methode hier in die Tabelle.
