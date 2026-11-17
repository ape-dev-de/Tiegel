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
| `--project DIR` | Projektverzeichnis. Projektbefehle (`info`, `files`, `interpreters`, `compose`, `lsp-servers`, `lsp-check`, `history`, `git-*`, `php-*`, `runtime-diagnose`, `mappings`, `interp-*`, `docker`, `breakpoints`, `symbols`, `definition`, `implementations`, `super`, `hierarchy`, `gutter`, `index-status`) nehmen ohne Angabe das Arbeitsverzeichnis. |
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
| `new-file DATEI [--content TEXT]` | Datei im Projekt anlegen (Elternordner nach Bedarf; Fehler, wenn sie existiert) | `files.createFile` |
| `new-dir ORDNER` | Ordner anlegen | `files.createDir` |
| `rename PFAD NEUER-NAME\|NEUER-PFAD [--overwrite]` | umbenennen bzw. verschieben (enthält das Ziel `/`, ist es ein Pfad); offene Dokumente (auch ungespeicherte) und Local History ziehen mit | `files.rename` |
| `move PFAD... ORDNER [--overwrite]` / `copy PFAD... ORDNER\|NEUER-PFAD [--overwrite]` | verschieben/kopieren (Ordner rekursiv, Symlinks als Links) | `files.move`, `files.copy` |
| `delete PFAD... [--permanent]` | in den Papierkorb (macOS: Finder-Papierkorb mit „Zurücklegen“, Linux: freedesktop-Papierkorb); ohne Papierkorb (Windows, anderes Dateisystem) Fehler – dann nur mit `--permanent`. Offene Dokumente mit ungespeicherten Änderungen → Fehler, nichts wird gelöscht | `files.delete` |
| `new-class ORDNER NAME [--kind class\|abstract\|interface\|trait\|enum] [--namespace NS]` | PHP-Datei `NAME.php` mit `declare(strict_types=1)` und dem PSR-4-Namespace aus `composer.json` (`autoload`/`autoload-dev`, längstes passendes Verzeichnis); `--namespace ""` = ohne Namespace | `files.createPhpClass` |
| `search FILE PATTERN [--regex] [--case] [--word] [--limit N]` | Treffer als `datei:zeile:spalte: zeile` | `search.inDoc` |
| `find PATTERN [--regex] [--case] [--word] [--mask GLOBS] [--dir DIR \| --open] [--libraries] [--ignored] [--limit N] [--max-size BYTES]` | Find in Files im Projekt: Treffer als `pfad:zeile:spalte: zeile`; `--mask "*.php,!*Test.php"`, `--dir` Verzeichnis, `--open` nur offene Dokumente; `vendor/`, `node_modules/` und `.gitignore`-Dateien nur mit `--libraries`/`--ignored`; Exit 1 ohne Treffer | `search.inProject` |
| `replace PATTERN ERSETZUNG [--preview] [find-Flags] [DATEI...]` | Replace in Path: ohne `--preview` werden die Dateien geschrieben (Local History „before replace in path“/„replace in path“), mit DATEIEN nur diese; Regex-Ersetzung mit `$1`/`${name}`; Exit 1 ohne Treffer oder bei Fehlern | `search.replaceInProject` |
| `interpreters` | lokale PHP-Binaries, Compose-Dienste (mit Docker-Kontext) und Docker-Image-Interpreter; `*` = gewählt | `interp.list` |
| `interpreters select ID` | Interpreter fürs Projekt wählen (ohne Projekt: global) | `interp.select` |
| `interpreters version [ID] [--modules] [--ini]` | Version, Erweiterungen, Xdebug-INI-Werte | `interp.version` |
| `compose [DIR] [--profile P]` | PHP-Kandidaten mit Score, Begründungen, Pfad-Mappings, Xdebug-Hinweisen | `compose.detect` |
| `interp-probe [ID] [--refresh] [--start]` | Interpreter-Info: PHP-Version, SAPI, OS, Erweiterungen, Xdebug-Version, `php.ini` und zusätzliche ini-Dateien – **ein** `php -r`-Lauf, Ergebnis pro Interpreter gecacht (`(cached …)`); `--refresh` liest neu, `--start` erlaubt `docker compose run` bzw. das Ziehen eines Images (sonst nur `exec`/`--pull never`) | `interp.probe` |
| `interp-status [ID]` | Docker-Kontext und Zustand eines Compose-Dienstes (`running`/`stopped`/`missing`, Container) bzw. ob das Image eines Image-Interpreters lokal vorhanden ist (fragt die Engine) | `interp.status` |
| `interp-image [add IMAGE [REMOTE] \| remove IMAGE]` | Docker-Image-Interpreter anzeigen/anlegen/entfernen: `docker run --rm -v <projekt>:REMOTE` (Standard `/app`), ID `docker:IMAGE`; danach mit `interpreters select docker:IMAGE` wählen | `interp.addImage`, `interp.removeImage`, `interp.list` |
| `docker [contexts]` | Docker-Binary (mit Fundort: `setting`, `path`, `login-shell`, `common`) und Kontexte mit Engine (OrbStack, Docker Desktop, Colima, …) und Endpoint; `*` = wirksamer Kontext; liest nur die lokale CLI-Konfiguration | `docker.contexts` |
| `docker status [KONTEXT]` | prüft, ob die Engine des (gewählten) Kontexts antwortet (`docker version`, Timeout `docker.timeoutSeconds`); Exit 1, wenn nicht erreichbar | `docker.status` |
| `docker use NAME [--global]` / `docker use --clear [--global]` | Kontext fürs Projekt (bzw. global) wählen oder zurücksetzen; Compose- und Image-Interpreter verwenden ihn (`--context`) | `docker.setContext` |
| `docker path [PFAD \| --clear]` | Docker-Binary anzeigen bzw. fest einstellen (`docker.path`) | `docker.setPath`, `docker.contexts` |
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
| `git-fetch [--remote R] [--prune] [--tags]` | alle Remotes (oder eines) holen; Ausgabe `new`/`updated`/`pruned` + Ref + Hash je geänderter Ref (Netzwerkzugriff nur hier) | `git.fetch` |
| `git-pull [--remote R] [--branch B] [--strategy merge\|rebase\|ff-only] [--autostash] [--prune]` | holen und in den aktuellen Branch integrieren; Konflikte als `U pfad`, dann Exit 1 (weiter mit `git.conflicts`/`git.mergeData`/`git.resolve`) | `git.pull` |
| `git-update [--strategy S] [--autostash] [--prune]` | Update Project: Dokumente speichern, Upstream holen, integrieren, offene Dokumente neu laden; Exit 1 bei Konflikten | `git.updateProject` |
| `git-stash [list \| push [-m MSG] [--untracked] [--keep-index] [DATEI...] \| apply [N] [--restore-index] \| pop [N] [--restore-index] \| drop [N]]` | Stash verwalten (`N` oder `stash@{N}`, ohne Unterbefehl: Liste); Exit 1 bei Konflikten von `apply`/`pop` | `git.stash*` |
| `usages DATEI ZEILE:SPALTE [--no-declaration] [--server ID] [--command PATH]` | Find Usages über den Language Server: `pfad:zeile:spalte: zeile`; Exit 1 ohne Treffer | `lsp.start`, `lsp.references` |
| `code-actions DATEI ZEILE:SPALTE [--apply N\|ID\|TITEL] [--timeout DUR] [--server ID] [--command PATH]` | Code Actions (Quick Fixes mit den Diagnosen an der Stelle, Refactorings, Organize Imports) auflisten; `--apply` wendet eine an und speichert die geänderten Dokumente | `lsp.codeActions`, `lsp.applyCodeAction` |
| `format DATEI... [--preview] [--tab-size N] [--server ID] [--command PATH]` | Reformat über den Language Server und speichern (`--preview`: nur die Änderungen) | `lsp.formatting` |
| `rename-symbol DATEI ZEILE:SPALTE NEUER-NAME [--preview] [--server ID] [--command PATH]` | Symbol in allen Dateien umbenennen (prepareRename + rename) und speichern | `lsp.rename` |
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
| `symbols QUERY [--kind K]... [--limit N] [--no-libraries]` | Fuzzy-Suche im PHP-Symbol-Index (Klassen, Member, Funktionen, Konstanten) als `art<TAB>fqn<TAB>pfad:zeile:spalte`; `--kind type` = alle Klassenarten (Go to Class); `Klasse::member` sucht Member, ein `\` im FQN; Exit 1 ohne Treffer | `nav.symbols` |
| `definition DATEI ZEILE[:SPALTE] [--source auto\|index\|lsp]` | Deklarationen des Symbols an der Position als `pfad:zeile:spalte: art fqn [quelle]` | `nav.definition` |
| `implementations DATEI ZEILE[:SPALTE] [--source …]` | implementierende Klassen (transitiv), Unterklassen, Trait-Nutzer, überschreibende/implementierende Methoden | `nav.implementations` |
| `super DATEI ZEILE[:SPALTE]` | Obertypen einer Klasse bzw. überschriebene/implementierte Methoden | `nav.super` |
| `hierarchy FQN \| hierarchy DATEI ZEILE[:SPALTE]` | Typ-Hierarchie: Obertypen (rekursiv, nicht indizierte wie `Countable` mit `?`) und Untertypen eingerückt | `nav.typeHierarchy` |
| `gutter DATEI` | Gutter-Marken: `zeile<TAB>art fqn<TAB>up N<TAB>down M` | `nav.gutter` |
| `index-status [--rebuild [--full]]` | baut den Index (bzw. aktualisiert ihn aus dem Cache) und zeigt Zustand und Zahlen; `--full` verwirft Index und Cache | `index.status`, `index.rebuild` |
| `watch [--backend auto\|fsevents\|fsnotify\|poll] [--exclude DIR]...` | Projekt beobachten und Änderungen zeilenweise ausgeben, bis Strg+C: `watching ROOT (backend, N files in M directories, T ms)`, dann `created/modified/deleted PFAD`, `renamed ALT -> NEU` (Ordner mit `/`), `git PFADE` (HEAD, index, refs …). `--json`: ein Event pro Zeile `{"topic", "payload"}`. `--exclude` ersetzt die Einstellung `watch.exclude` (Standard `node_modules`) | `watch.start`, Events `files.changed`, `git.changed`, `watch.status` |
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

# Alle Aufrufe einer Funktion in PHP-Dateien (ohne vendor), dann ersetzen
tiegel --headless find 'old_helper(' --mask '*.php'
tiegel --headless replace 'old_helper\(' 'new_helper(' --regex --mask '*.php' --preview

# Projekt aktualisieren (Fetch + Rebase, lokale Änderungen automatisch gestasht)
tiegel --headless git-update --strategy rebase --autostash

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
| -32004 | Konflikt: Versionskonflikt (`version` passt nicht zum Dokument), `doc.save`/`doc.saveAll` auf eine extern geänderte Datei (`data: {reason: "externalChange", path, kind: modified\|created\|deleted, generation}`), `doc.close` mit ungespeicherten Änderungen ohne `discard` oder mit veralteter `generation`, Hunk-Auswahl passt nicht mehr zum Diff (`header`/`hash`), `git.commitSelection` während Merge/Rebase/Cherry-pick/Revert | 1 |
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

Vom Kern veröffentlichte Topics: `project.opened`/`project.closed` (`{root, name}`), `file.opened` (`{path, file, language, version, size}`), `file.saved` (`{path, file, trigger, size}` – auch bei Autosave), `doc.opened`, `doc.changed`, `doc.edited`, `doc.saved`, `doc.closed`, `doc.renamed`, `files.changed`, `modules.changed`, `settings.changed`, `search.results` (gestreamte Projektsuche: `{id, files, done, summary, error}`).

`files.changed` meldet die Dateioperationen eines API-Aufrufs (`files.*`) für Dateibaum und Indizes: `{changes: [{kind: created|renamed|deleted, path, file, oldPath?, oldFile?, dir?, trashed?, source: "tiegel"}]}` (derselbe Typ wie beim Watcher, siehe unten); bei `renamed` sind `path`/`file` der neue Ort. Folgt ein offenes Dokument einem Umbenennen/Verschieben, kommen `doc.renamed` (`{from, to, fromFile, file, generation, language}`), dann `doc.closed` für den alten Pfad mit `renamedTo` und `doc.opened` für den neuen – Abonnenten, die nur Öffnen/Schließen kennen (Language Server, Syntax), folgen so automatisch; Dokument, Puffer, Undo und Generation bleiben erhalten. Der Language Server bekommt zusätzlich `workspace/didRenameFiles`, wenn er sich dafür registriert hat.

**Dateiwatcher und externe Änderungen (D140, D141).** Läuft der Watcher (`serve` und GUI starten ihn bei `project.open`, sonst `watch.start`), kommen:

- `files.changed` – `{changes: [{kind, path, file, oldPath?, oldFile?, dir?, source: "watcher"}]}`, `kind` = `created` \| `modified` \| `deleted` \| `renamed` (mit `oldPath`); gebündelt (100 ms Ruhe, spätestens 1 s), `.git`-Interna, `node_modules` (bzw. `watch.exclude`) und Tiegels Temp-Dateien fehlen. Auch eigene Speichervorgänge erscheinen hier (für Dateibaum und Git-Status), lösen aber nie die folgenden Dokument-Events aus – ebenso wenig Tiegels eigene Dateioperationen (`files.rename`/`move`/`copy`).
- `git.changed` – `{op: "external", paths: [".git/HEAD", ".git/index", ".git/refs/…"]}` bei Änderungen von HEAD, Index, Refs, `packed-refs` oder `MERGE_HEAD`/`CHERRY_PICK_HEAD`/`REVERT_HEAD`/`REBASE_HEAD` durch andere Programme.
- `doc.reloaded` – Dokumentzustand + `source` (`watcher`, `refresh`, `resolve`, `api`): ein offenes Dokument **ohne** ungespeicherte Änderungen wurde aus der Datei neu geladen (ein Undo-Schritt, nur der geänderte Bereich wird ersetzt).
- `doc.externalConflict` – `{path, file, generation, version, kind, modified, diskSize, diskTime, diff: {hunks: [{bufferStart, bufferLines, diskStart, diskLines}], added, removed}, source}`: die Datei eines Dokuments **mit** ungespeicherten Änderungen wurde extern geändert (`source: "watcher"`) oder ein Speichern wurde deshalb verweigert (`"save"`). Einmal pro Plattenstand. Der Dokumentzustand trägt dann `external: "modified"` (bzw. `"created"`).
- `doc.deletedOnDisk` – gleicher Aufbau mit `kind: "deleted"` und ggf. `renamedTo`: die Datei wurde gelöscht oder umbenannt; das Dokument bleibt offen (`external: "deleted"`), Speichern legt sie nur mit `force` neu an. Taucht die Datei mit gleichem Inhalt wieder auf, verschwindet der Status.
- `watch.status` – Watcher-Zustand (`{running, ready, root, backend, dirs, files, startupMs, batches, rescans, degraded?}`) nach dem Startscan, beim Stoppen und beim Rückfall auf Polling.

Autosave überschreibt eine extern geänderte Datei **nie**: `idle`/`focusLost` überspringen sie (Event statt Fehler), `beforeCommit`/`beforeRun`/`beforeDebug`/`close` melden einen Fehler (beim Schließen wird der Puffer als Wiederherstellungspunkt gesichert). Die Prüfung vor dem Schreiben arbeitet auch ohne Watcher.

`doc.edited` beschreibt **jede** Pufferänderung – egal ob über `doc.*`, Undo, Autosave-Filter oder direkt in der GUI – im Format `{path, file, language, version, start, oldEnd, newEnd, startPoint, oldEndPoint, newEndPoint, range: {start: {line, character}, end: …}, text, oldText}`: Byte-Offsets und Zeile/Byte-Spalte (Tree-sitter) sowie der ersetzte Bereich in LSP-Koordinaten (0-basiert, UTF-16). `version` steigt pro Änderung um 1.

Von den Modulen veröffentlichte Topics:

| Modul | Topics |
|---|---|
| php-runtime | `interpreter.changed` (`{id, label, kind, previous}`), `interp.selected`, `runtime.mappingsChanged` (`{mappings, source}`) |
| php-features | `php.diagnostics` (`{path, file, version, phpVersion, source, diagnostics}`), `php.versionChanged` (`{override}`) |
| lang-php | `lsp.status` (gestartet/gestoppt/abgestürzt), `lsp.diagnostics` (`{path, file, version, diagnostics}`), `lsp.editApplied` (`{label, applied, files}` nach jeder angewendeten Workspace-Änderung: Code Action, Rename, `workspace/applyEdit` des Servers) |
| git | `git.changed` (`{op, paths}` nach jeder Änderung über die API; `op` u.a. `fetch`, `pull`, `update`, `stash`, `stashApply`, `stashPop`, `stashDrop`), `git.committed` |
| php-index | `index.progress` (`{phase: scanning\|indexing\|ready, done, total}`, höchstens alle 100 ms), `index.updated` (`{files, truncated, generation, reason: sync\|file\|doc\|cache}`) |
| debug-xdebug | `debug.listening`, `debug.stopped`, `debug.sessionStarted`, `debug.sessionEnded`, `debug.break` (gemappte Host-Datei/Zeile), `debug.breakpointHit`, `debug.breakpoints.changed`, `debug.status`, `debug.output`, `debug.notification`, `debug.breakpoint.resolved` |

## Module

Die Funktionen außerhalb des Kerns stellen Module bereit (`internal/modules/*`, ohne Gio). Sie registrieren ihre Methoden beim Start und sprechen untereinander nur über Bus-Topics und Hooks:

| Modul | Abhängig von | Namespaces | Hooks |
|---|---|---|---|
| `php-runtime` | – | `interp.*`, `compose.*`, `php.version`, `runtime.*` | stellt `php.resolveVersion` und `runtime.pathMappings` (Filter) bereit |
| `php-features` | `php-runtime` | `php.check`, `php.diagnostics`, `php.setVersionOverride` | nutzt `php.resolveVersion` |
| `lang-php` | (`php-runtime` optional) | `lsp.*` | nutzt `php.resolveVersion`, stellt `nav.lsp` (Filter) bereit |
| `git` | – | `git.*` | ruft Guard `commit.before` (Kern: Autosave) und Filter `commit.message` |
| `syntax` | – | `syntax.*` | – (folgt `doc.edited`) |
| `php-index` | – | `index.*`, `nav.*` | ruft Filter `nav.lsp` (Anbieter: lang-php) |
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
| `project.open` | `path` | Projektinfo (ein anderes offenes Projekt wird vorher wie mit `project.close` geschlossen) |
| `project.info` / `project.close` | – | Projektinfo |
| `files.list` | `path?`, `depth?`, `ignored?`, `libraries?`, `limit?` | Baum `{name, path, dir, size, ignored, library, collapsed, children}` + Zähler |
| `files.read` | `path`, `maxBytes?` | `content`, `encoding` (`utf-8`/`base64`), `language`, `size`, `truncated` |
| `files.detectLanguage` | `path`, `content?` | `language` |
| `files.createFile` | `path`, `content?`, `open?` | `{path, file, doc?}`; `CodeConflict`, wenn vorhanden |
| `files.createDir` | `path` | `{path, file, dir}` |
| `files.rename` | `path`, `newName` **oder** `to`, `overwrite?` | `{path, file, from, dir, docs}` – `docs`: offene Dokumente unter neuem Pfad |
| `files.move` / `files.copy` | `path` oder `paths`, `to` (Ordner; bei einem Pfad auch Zielpfad), `overwrite?` | `{results: [{path, from, …}]}`; Ordner werden nie überschrieben |
| `files.delete` | `path` oder `paths`, `permanent?` | `{deleted, trashed, closed}`; `CodeConflict` bei ungespeicherten Dokumenten, `CodeUnavailable` ohne Papierkorb |
| `files.reveal` | `path` | im Dateimanager zeigen (`open -R`, `explorer /select,`, `xdg-open ORDNER`) – nur auf Nutzeraktion aufrufen |
| `files.copyPath` | `path` oder `paths` | `{paths: [{absolute, relative, name, uri}], text, relativeText}` – die GUI kopiert in die Zwischenablage |
| `files.createPhpClass` | `dir?`, `name`, `kind?` (`class` = `final class`, `abstract`, `interface`, `trait`, `enum`), `namespace?`, `open?` | Datei + `namespace`, `class` (FQCN) |
| `files.phpNamespace` | `path?` (Ordner) | `{dir, namespace, found, mapping}` aus `composer.json` PSR-4 |
| `doc.open` | `path`, `create?`, `text?` | Dokumentzustand (+ `text`, `alreadyOpen`); jeder Dokumentzustand enthält `generation` (eindeutig pro geöffneter Instanz, neu nach Schließen + Öffnen) |
| `doc.close` | `path`, `generation?`, `discard?` | `path`, `generation`, `discarded`; mit ungespeicherten Änderungen nur mit `discard: true` (sonst -32004, `data` = Dokumentzustand); mit `generation` nur, wenn sie zur offenen Instanz passt (sonst -32004 – ein verspätetes Close trifft kein neu geöffnetes Dokument) |
| `doc.list` | – | Dokumentzustände (mit `external`, solange eine externe Änderung offen ist) |
| `doc.text` | `path`, `start?`, `end?` | `text`, `version` |
| `doc.insert` | `path`, `at`, `text`, `version?` | Dokumentzustand |
| `doc.delete` | `path`, `start`, `end`, `version?` | Dokumentzustand |
| `doc.replace` | `path`, `start?`, `end?`, `text`, `version?` (ohne Bereich: ganzes Dokument) | Dokumentzustand |
| `doc.undo` / `doc.redo` | `path` | Dokumentzustand + `done` |
| `doc.save` | `path`, `force?`, `reload?` | Zustand + `size`, `revision` (Hooks `file.beforeSave`, Local History, `file.saved`); der Inhalt wird erst unter der Speicher-Sperre gelesen, ein älterer Stand überschreibt nie einen neueren Autosave. Wurde die Datei seit dem Lesen/Schreiben extern geändert (Inhalt weicht ab), -32004 mit `data.reason = "externalChange"`; `force: true` überschreibt trotzdem, `reload: true` verwirft stattdessen den Puffer und lädt die Datei (wie `doc.resolveExternal takeDisk`) |
| `doc.resolveExternal` | `path`, `action` (`keepMine` \| `takeDisk`) | `keepMine`: Puffer über die geänderte Datei speichern (wie `doc.save force`); `takeDisk`: Puffer durch die Datei ersetzen (ein Undo-Schritt; -32001, wenn sie gelöscht ist). Ergebnis wie `doc.save` + `action` |
| `doc.saveAll` | – | `saved: [..]` |
| `watch.start` | `backend?` (`auto`, `fsevents`, `fsnotify`, `poll`), `exclude?` | Watcher-Status; (neu)startet den Projekt-Watcher; ohne `exclude` gilt die Einstellung `watch.exclude` (Projekt, sonst global; Ordnernamen oder projektrelative Pfade, Standard `["node_modules"]`) |
| `watch.stop` / `watch.status` | – | Watcher-Status `{running, ready, root, backend, dirs, files, startupMs, batches, rescans, degraded?}` |
| `watch.refresh` | – | `external: [Dokumentzustand]`; scannt das Projekt sofort (Events wie oben, synchron) und gleicht **alle** offenen Dokumente mit der Platte ab (auch ohne Watcher; z.B. bei Fensterfokus) |
| `history.list` | `path` | Revisionen (neueste zuerst) |
| `history.content` | `path`, `id` | `revision`, `text` |
| `history.restore` | `path`, `id` | neue Revision (offenes Dokument wird neu geladen; unter der Speicher-Sperre) |
| `history.recovery` | – | `[{path, file, revision, time, reason, error}]` – Wiederherstellungspunkte (neueste zuerst): ungespeicherte Änderungen, die beim Schließen nicht gespeichert werden konnten (`autosave.trigger` mit `close`) oder beim Beenden/`project.close` noch offen waren; Inhalt über `history.content` |
| `history.recoveryDismiss` | `paths?` (ohne: alle) | verbleibende Wiederherstellungspunkte (die Revisionen bleiben in der Local History) |
| `autosave.trigger` | `trigger?` (`focusLost` Standard, `idle`, `beforeRun`, `beforeDebug`, `beforeCommit`, `close`) | `saved: [..]` – alle geänderten Dokumente über Autosave (Hooks, Local History mit Label `autosave: <trigger>`); mit `close` werden nicht speicherbare Dokumente als Wiederherstellungspunkt gesichert, der Fehler (-32000) nennt das und trägt die Punkte als `data` |
| `autosave.config` | `idleSeconds?` (0 = aus) | `idleSeconds` (dauerhaft in den globalen Settings; jede Pufferänderung startet den Leerlauf-Timer neu) |
| `settings.get` | `key`, `scope?` (`global`, `project`) | `key`, `scope`, `found`, `value` |
| `settings.set` | `key` (nur `ui.*`, `editor.*`, `update.*`, `watch.*`), `scope?`, `value` (`null` löscht) | wie `settings.get` |
| `search.inDoc` | `path`, `pattern`, `regex?`, `caseSensitive?`, `wholeWord?`, `limit?` | Treffer mit `line`, `col`, `text`, `lineText` (offenes Dokument oder Datei) |
| `search.replaceInDoc` | wie `search.inDoc` + `replacement` | Zustand + `replaced` (ein Undo-Schritt) |
| `search.inProject` | `pattern`, `regex?`, `caseSensitive?`, `wholeWord?`, `mask?` (`"*.php,*.inc,!*.min.js"`; mit `/` gegen den Projektpfad), `scope?` (`project`, `directory` + `dir`, `openFiles`), `paths?` (nur diese Dateien), `libraries?`, `ignored?`, `limit?` (5000), `maxFileSize?` (4 MiB), `stream?`, `id?` | `{id, files[{path, file, open, version, matches[{start, end, line, col, text, lineText, previewStart?}]}], matches, filesMatched, filesSearched, skippedBinary, skippedLarge, limited, cancelled, durationMs}`; offene Dokumente werden im Puffer durchsucht (auch ungespeicherte neue). Mit `stream: true` kommt sofort `{id, running: true}`, die Treffer folgen als Events `search.results` (Stapel; das letzte hat `done: true` und `summary`) |
| `search.cancel` | `id` | `{id, cancelled}` – bricht eine laufende Suche (auch `search.replaceInProject` vor dem Schreiben) ab |
| `search.replaceInProject` | wie `search.inProject` + `replacement`, `preview?` | `{preview, files[{path, open, replaced, version, revision, matches?[{…, replacement}], error}], replaced, filesChanged, failed, filesSearched, limited}` – Vorschau ändert nichts; sonst pro Datei: offenes Dokument = ein Undo-Schritt (nicht gespeichert), geschlossene Datei atomar geschrieben mit Local-History-Revisionen „before replace in path“/„replace in path“ und `file.saved` (`trigger: "replace in path"`); die Treffer werden beim Anwenden gegen den aktuellen Text neu berechnet; Fehler einer Datei stoppen die anderen nicht |
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
| `lsp.references` | `path`, `at`, `includeDeclaration?` (Standard `true`) | Find Usages: `[{path, file, range, lineText}]` sortiert nach Datei/Position |
| `lsp.documentHighlight` | `path`, `at` | `[{range, kind}]` (`text`, `read`, `write`) |
| `lsp.codeActions` | `path`, `start`, `end?`, `only?` (Kind-Präfixe wie `quickfix`, `source.organizeImports`) | `[{id, title, kind, preferred, disabled, diagnostics, hasEdit, command, action}]`; die zuletzt empfangenen Server-Diagnosen, die den Bereich berühren, gehen als Kontext mit (Quick Fixes). `id` gilt für die laufende Server-Generation (nach Neustart -32001) |
| `lsp.applyCodeAction` | `id` oder `action` (rohe Code Action aus `lsp.codeActions`) | `{label, applied, files[{kind, path, newPath, open, edits, version, revision, skipped}], command, commandResult}`; ohne Edit wird per `codeAction/resolve` nachgeladen, danach Edit angewendet, dann `command` per `workspace/executeCommand` ausgeführt (dessen `workspace/applyEdit` werden bedient); deaktivierte Aktionen -32000 |
| `lsp.executeCommand` | `command`, `arguments?` | wie `lsp.applyCodeAction` (`command`, `commandResult`) |
| `lsp.formatting` / `lsp.rangeFormatting` | `path`, (`start`, `end` bei Range), `tabSize?` (4), `insertSpaces?` (`true`), `apply?` (`true`) | `{path, edits[{range, newText}], applied, version}` – als ein Undo-Schritt, nur wenn das Dokument noch die angefragte Version hat (sonst -32004) |
| `lsp.prepareRename` | `path`, `at` | `{canRename, range?, placeholder?}` |
| `lsp.rename` | `path`, `at`, `newName`, `preview?` | wie `lsp.applyCodeAction` + `preview[{kind, path, newPath, edits}]`; vorher `prepareRename` (nicht umbenennbar: -32602) |
| `lsp.documentSymbols` | `path` | Baum `[{name, detail, kind, range, selectionRange, children}]` |
| `lsp.workspaceSymbols` | `query`, `limit?` (500) | `[{name, kind, container, path, file, range?}]` |

Workspace-Änderungen des Servers (Code Actions, Rename, `workspace/applyEdit`) laufen über `App.ApplyFileOps`: Text-Edits offener Dokumente als ein Undo-Schritt je Datei (Versionsprüfung bei versionierten `documentChanges`), geschlossene Dateien atomar unter der Speicher-Sperre mit Local History, Ressourcen-Operationen `create`/`rename`/`delete` in Reihenfolge (Umbenennen wie `files.rename`: offene Dokumente folgen mit Puffer und Undo, `doc.renamed`; Gelöschtes bleibt in der Local History; `files.changed` wird veröffentlicht). Beim ersten Fehler wird abgebrochen (`failureHandling: abort`), bereits angewendete Schritte bleiben.

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
| `git.push` | `remote?`, `branch?`, `remoteBranch?`, `setUpstream?`, `forceWithLease?`, `tags?` | `remote`, `branch`, `remoteBranch`; Fehler -32000 mit `data: {kind, message, stderr}`, `kind` = `rejected`, `remoteRejected`, `auth`, `network`, `repoNotFound`, `noRemote`, `failed` (Fetch/Pull/Update/Stash zusätzlich `noUpstream`, `localChanges`, `diverged`, `detached`) |
| `git.recentMessages` | `max?` (20) | Nachrichten, neueste zuerst |
| `git.gutter` | `path`, `base?` (`head`/`index`) | `ranges[{kind, start, end, baseStart, baseEnd}]` (1-basiert, `end` exklusiv; gelöscht: `start == end`), aus dem Pufferinhalt |
| `git.rollback` | `path`, `line`, `base?` | Dokumentzustand (Änderung als Undo-fähige Bearbeitung des offenen Dokuments) |
| `git.show` | `rev?` (HEAD), `path` | `content`, `encoding` (`utf-8`/`base64`) |
| `git.checkout` | `ref` | Status (offene, ungeänderte Dokumente werden neu geladen) |
| `git.createBranch` / `git.deleteBranch` | `name`, `start?`, `checkout?` / `force?` | Branches |
| `git.conflicts` | – | Dateien mit Konflikt |
| `git.mergeData` | `path` | `base`, `ours`, `theirs`, `has*`, `blocks[{index, kind, conflict, base, ours, theirs, *Start}]`, `merged` (mit Markern), `conflicts` |
| `git.fetch` | `remote?` (ohne: alle), `prune?`, `tags?` | `{remotes, updated[{ref, old?, new?}]}` (Netzwerkzugriff nur hier) |
| `git.pull` | `remote?`, `branch?` (Standard: Upstream), `strategy?` (`merge` Standard, `rebase`, `ff-only`), `autostash?`, `prune?` | `{branch, remote, remoteBranch, before, after, upToDate, fastForward, changed, conflicts, state, fetched, ahead, behind}`; vorher werden offene Dokumente gespeichert, das Holen läuft außerhalb, das Integrieren unter der Speicher-Sperre, danach werden offene, ungeänderte Dokumente neu geladen. Konflikte sind kein Fehler (`conflicts`, `state` `merging`/`rebasing`) |
| `git.updateProject` | `strategy?`, `prune?`, `autostash?` | wie `git.pull` für den Upstream des aktuellen Branches (`git.changed` mit `op: "update"` und den geänderten Pfaden) |
| `git.stash` | `message?`, `includeUntracked?`, `keepIndex?`, `paths?` | `{created, entry?}` (Dokumente werden vorher gespeichert) |
| `git.stashList` | – | `[{index, ref, hash, message, date}]`, neueste zuerst |
| `git.stashApply` / `git.stashPop` | `index?` (0), `hash?` (Prüfung gegen verschobenen Stash: -32004 `{reason: "staleStash"}`), `restoreIndex?` | `{conflicts, dropped}` (Pop behält den Eintrag bei Konflikten) |
| `git.stashDrop` | `index?`, `hash?` | verbleibende Einträge |
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

**php-index** – projektweiter PHP-Symbol-Index auf Tree-sitter (D111): rein strukturelle Navigation ohne Typinferenz, unabhängig vom Language Server. Der Index entsteht beim Öffnen des Projekts im Hintergrund (Cache unter `projects/<hash>/index/`, danach werden nur geänderte Dateien geparst) und folgt Dokumentänderungen, Speichern, `files.changed`, `git.changed` (D112). `vendor/` wird standardmäßig mitindiziert (`library: true`, in der Suche nachrangig), `.gitignore` grob beachtet. Einzelbefehle der CLI indizieren erst beim ersten `index.*`/`nav.*`-Aufruf. Positionen wie bei `doc.*`; Quelle ist das offene Dokument, sonst die Datei. `wait: true` wartet auf die erste Indizierung, sonst ist `complete` während des Aufbaus `false`. Ergebnisse `Location`: `{path, file, range (Name), declRange, kind, name, fqn, container, detail, library, source}` mit `kind` aus `class`, `interface`, `trait`, `enum`, `method`, `property`, `classConstant`, `enumCase`, `function`, `constant` und `source` `index`, `lsp` oder `index+lsp`. Ohne cgo: `-32005`.

| Methode | Parameter | Ergebnis |
|---|---|---|
| `index.status` | `wait?` | `state` (`idle`, `loading`, `indexing`, `ready`, `error`), `progress`, `files`, `libraryFiles`, `classes`, `members`, `functions`, `constants`, `generation`, `fromCache`, `lastSync`, `durationMs`, `libraries`, `error` |
| `index.config` | `libraries?`, `includeIgnored?`, `exclude?: [muster]` | aktuelle Einstellung (Projekt-Settings `index.*`); Änderungen starten den Index neu |
| `index.rebuild` | `full?` (Index und Cache verwerfen), `wait?` | Status |
| `nav.definition` | `path`, `at`, `source?` (`auto` = Index + laufender Language Server, `index`, `lsp`), `wait?` | `locations`, `complete`, `lsp`, `lspError` |
| `nav.implementations` | wie `nav.definition` | Interface → implementierende Klassen/Enums (transitiv, auch über Elternklassen), Klasse → Unterklassen, Trait → Nutzer, Methode → überschreibende/implementierende Methoden |
| `nav.super` | `path`, `at`, `wait?` | Klasse → direkte Obertypen; Methode → nächste Deklaration in der Elternkette + alle Interface-Deklarationen |
| `nav.typeHierarchy` | `fqn` oder `path`+`at`, `wait?` | `class`, `supertypes`/`subtypes` als Bäume (`children`), `truncated` (über 5 000 Untertypen) |
| `nav.symbols` | `query`, `kinds?` (Arten oder `type`), `limit?` (100), `libraries?` (true), `wait?` | `symbols[{…Location, score}]`, `complete` |
| `nav.gutter` | `path`, `wait?` | `marks[{line, kind, fqn, range, up, down, upTotal, downTotal}]` – pro Klasse/Methode: `up` = implementiert/überschreibt, `down` = wird implementiert/überschrieben (Listen auf 100 gekürzt) |

`session` bei `debug.*` darf fehlen, wenn genau eine Sitzung aktiv ist.

## Erweitern: neue Namespaces

Neue Funktionen kommen entweder in den Kern (`internal/api`, `mustHandle` in `New`) oder – der Normalfall – in ein Modul unter `internal/modules/<name>` (ausführlich in `internal/api/doc.go`):

1. Param-/Ergebnis-Structs (JSON-Tags) und Handler `func (m *Module) xyz(ctx context.Context, p Params) (Result, error)`.
2. In `Start`: `api.AppFrom(ctx)` für den Kernzugriff, Methoden mit `api.Provide`/`api.Add`, Hooks mit `hooks.WithOwner(ctx.ModuleID())`, Bus-Abos über `ctx.OnStop(bus.Subscribe(...))`. `Claims()` meldet die Methoden an, damit sie bei abgeschaltetem Modul `module disabled` liefern.
3. Erwartbare Fehler als `api.Errorf(api.CodeNotFound, …)` (bei Bedarf `.WithData(...)`), Zustandsänderungen als Bus-Event, gemeinsame Daten über Topics/Hooks aus `internal/api/contract.go` – nie direkte Aufrufe anderer Module.
4. Dokument-Inhalte immer über `App.Document`/`Doc.Snapshot` bzw. `doc.edited` lesen; Bearbeitungen über `App.EditDocument` (Undo-fähig).
5. Tests mit `internal/modules/modtest`, bei Bedarf ein CLI-Befehl in `internal/headless/cmd_<bereich>.go` (eigene Flags per `init` in `flagSpec`), Methode hier in die Tabelle.
