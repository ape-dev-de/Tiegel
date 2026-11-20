# Roadmap & Meilensteine

Grundlage: [konzept.md](konzept.md). Stand und Fortschritt: [STATUS.md](STATUS.md). Entscheidungen: [DECISIONS.md](DECISIONS.md).

Jeder Meilenstein hat **Abnahmekriterien**, die automatisiert (Tests) oder per Smoke-Test prüfbar sind.
Arbeitspakete (AP) innerhalb eines Meilensteins sind so geschnitten, dass sie parallel von Subagenten
in eigenen Paketen/Worktrees bearbeitet werden können.

## Paket-Layout (verbindlich)

```
cmd/tiegel            Hauptprogramm (Gio-Fenster)
cmd/tiegel-catalog    Komponenten-Katalog (Storybook-Ersatz)
internal/core/        bus, hooks, module, settings, langdetect, history, autosave, project
internal/api          interne UI↔Backend-API (später Socket für WSL)
internal/editor/      buffer (Piece Table), cursor, search, fold, view-model
internal/syntax       Tree-sitter-Anbindung, Highlight-Queries
internal/ui/          tokens, atoms, molecules, organisms, templates, editorview
internal/lsp          LSP-Client (JSON-RPC über stdio)
internal/dbgp         DBGp-Server (Xdebug)
internal/gitcli       Git-CLI-Wrapper, Diff-/Hunk-Parser, Merge
internal/compose      Compose-Erkennung (compose-go), Pfad-Mapping
internal/interp       Interpreter-Abstraktion (lokal, Compose, WSL)
internal/phpver       PHP-Versionsermittlung, Feature-Tabelle
internal/modules/     langphp, phpruntime, phpfeatures, git, debugxdebug (Modul-Hüllen + UI)
```

Backend-Pakete (`core`, `editor`, `syntax`, `lsp`, `dbgp`, `gitcli`, `compose`, `phpver`) importieren **nie** Gio.

---

## M0 – Fundament (Repo, Build, CI)
- go.mod, Paket-Layout, `CLAUDE.md` mit Konventionen, Makefile, GitHub-Actions-CI (macOS + Linux: build, vet, test).
- **Abnahme:** `make check` grün lokal und in CI.

## M1 – Kern & Modul-System
- AP1.1 Event-Bus (typisierte Topics, async + sync, Abmeldung).
- AP1.2 Hooks: Action / Filter / Guard, Prioritäten, Zeitlimit pro Hook, Panic-Schutz.
- AP1.3 Modul-Registry: `Module`-Interface aus dem Konzept, Abhängigkeitsprüfung, Start/Stop-Reihenfolge, Zyklen-Erkennung.
- AP1.4 Settings-Store (intern, nicht vom Nutzer zu editieren; global + pro Projekt unter OS-App-Verzeichnis).
- AP1.5 Spracherkennung (Endung, Shebang, `<?php`).
- AP1.6 Local History (jede Speicherung versioniert, Wiederherstellung) + Autosave-Logik (Fokusverlust, vor Run/Debug/Commit, Leerlauf-Timer).
- **Abnahme:** Unit-Tests ≥ 80 % Abdeckung für `internal/core/...`; Race-Detector grün.

## M2 – Design-System & Shell
- AP2.1 Design-Tokens (hell/dunkel), Schrift (Go-Fonts mitgeliefert), Abstände, Radien.
- AP2.2 Atome: Button, Icon (eigene Vektor-Icons, keine JetBrains-Assets), Eingabefeld, Checkbox, Badge, Tooltip.
- AP2.3 Moleküle: Suchfeld, Dateizeile, Tab, Breakpoint-Zeile, Diff-Block.
- AP2.4 Komponenten-Katalog `cmd/tiegel-catalog`.
- AP2.5 Hauptfenster-Template: Dateibaum, Tab-Leiste, Editor-Fläche, Statusleiste, Tool-Fenster unten/rechts; Projekt öffnen.
- **Abnahme:** Katalog und Hauptfenster starten; Golden-Screenshot-Tests für Tokens/Atome (headless Gio-Rendering).

## MH – Headless-Modus & interne API (querschnittlich, D41)
- AP-H.1 `internal/api`: Service-Schicht über alle Backend-Pakete (Projekt öffnen, Dateien, Puffer-Operationen, Interpreter, Compose, PHP-Version/Features, Git, LSP, Debug), Methoden + Events, transportunabhängig.
- AP-H.2 `tiegel --headless <befehl>`: Einzelbefehle für Pipelines, menschenlesbar oder `--json`, sinnvolle Exit-Codes.
- AP-H.3 `tiegel --headless serve`: JSON-RPC 2.0 über stdio (Events als Notifications); gleiche API später per Socket (M9).
- Regel: Neue Funktionen zuerst in `internal/api` + Headless-Befehl, dann GUI. E2E-Tests laufen headless.
- **Abnahme:** E2E-Tests starten das Binary mit `--headless` und prüfen Ausgaben/JSON.

## M3 – Editor (Eigenbau) – größter Block
- AP3.1 Piece-Table-Puffer: Einfügen/Löschen O(log n), Zeilenindex, UTF-8/Grapheme-sicher, Undo/Redo mit Gruppierung, Snapshots.
- AP3.2 Cursor-Modell inkl. Mehrfach-Cursor, Auswahl, Wort-/Zeilenbewegung, Klammer-Paare.
- AP3.3 Suchen/Ersetzen (Text, Regex, Groß/Klein), Folding-Bereiche.
- AP3.4 Gio-Editor-Widget: virtuelles Rendering (nur sichtbare Zeilen), Scrollen, Maus, Tastatur, IME, Clipboard, Gutter (Zeilennummern + Slots für Breakpoints/Git/Icons).
- AP3.5 Tree-sitter-Highlighting inkrementell (PHP, JSON, YAML, XML, .env).
- AP3.6 PhpStorm-Keymap (Mac) als Tabelle.
- **Abnahme:** 100k-Zeilen-Datei: Öffnen < 300 ms, Tastendruck-bis-Frame < 16 ms (Benchmark); Fuzz-Tests für den Puffer.

## M4 – lang-php (LSP)
- AP4.1 LSP-Client: JSON-RPC 2.0 über stdio, Initialize, didOpen/didChange (inkrementell), Diagnosen, Cancel.
- AP4.2 Server-Erkennung: Phpactor (Standard), PHPantom, Intelephense optional – nur vom System, kein Download.
- AP4.3 Editor-Features: Vervollständigung-Popup, Hover, Signaturhilfe, Go to Definition/Implementation, Gutter-Icons, Semantic Tokens.
- **Abnahme:** Integrationstest gegen Fake-LSP-Server; manueller Smoke-Test mit Phpactor.

## M5 – php-runtime & php-features
- AP5.1 Interpreter lokal: Suche in PATH/Homebrew, `php -v`/`php -m`.
- AP5.2 Docker Compose: compose-go, PHP-Service-Erkennung, Pfad-Mapping aus Volumes, `docker compose exec`.
- AP5.3 Versionsermittlung (Interpreter → `config.platform.php` → `require.php`).
- AP5.4 Feature-/Deprecation-Tabelle (YAML, eingebettet) mit Tree-sitter-Queries; Diagnosen im Editor; Version an LSP.
- AP5.5 UI: Interpreter-Auswahl, Statusleisten-Version mit Override.
- **Abnahme:** Testfälle mit Beispiel-Compose-Dateien und PHP-Snippets je Feature.

## M6 – git
- AP6.1 Git-CLI-Wrapper: status (porcelain v2), diff-Parser, Hunks, Teil-Staging via `git apply --cached`, commit/amend/push, log, branches.
- AP6.2 Gutter-Änderungsmarker + Rollback eines Blocks.
- AP6.3 Commit-Dialog wie PhpStorm (Dateiliste + Checkboxen auch pro Hunk, Diff-Vorschau, Nachrichtenverlauf, Amend, Commit and Push, Guard `commit.before`).
- AP6.4 Diff-Ansicht, Log, Branches.
- AP6.5 3-Wege-Merge (ours | Ergebnis | theirs).
- **Abnahme:** Tests gegen temporäre Repos (`git init` in TempDir).

## M7 – debug-xdebug
- AP7.1 DBGp-Server (Port 9003): init, feature_set, breakpoint_set (line/conditional), run, step_*, stack_get, context_get, property_get, eval.
- AP7.2 Pfad-Mapping aus php-runtime.
- AP7.3 Debug-Panel: Listener-Knopf, Breakpoints im Gutter, Variablen, Watches, Call Stack, Stepping.
- **Abnahme:** Tests mit simuliertem Xdebug-Client; Smoke-Test mit echtem `php -dxdebug.mode=debug`.

## M8 – Mac-Paketierung & Updates
- App-Bundle, Signieren/Notarisieren (Skripte; Zertifikate liefert der Nutzer), opt-in Update-Prüfung gegen GitHub-Releases, Release-Workflow.

## M9 – Linux & Windows/WSL
- Linux-Builds (Wayland/X11), Backend als eigener Prozess über Socket (`internal/api`), Windows-UI ↔ WSL-Backend.

## M10 – Parität & Fenster-Management (Nutzer-Feedback 2026-11-16)
Grundlage: Funktionsvergleich [vergleich.md](vergleich.md).
- AP10.1 Fenster-Management: Tool-Fenster links/rechts/unten andockbar mit Seitenleisten-Knöpfen, ein-/ausblenden, Größe ziehen, Editor maximieren, Editor-Split (vertikal/horizontal), mehrere Projektfenster.
- AP10.2 Projekte: öffnen/wechseln/schließen, zuletzt geöffnete Projekte, „in neuem/diesem Fenster“, Ordner-Dialog.
- AP10.3 Schrift: Systemschriften (macOS: SF Mono/Menlo, SF Pro) mit Fallback, einstellbar (Familie, Größe, Zeilenhöhe); Emoji/CJK über System-Fallback.
- AP10.4 Kontextmenüs: Editor, Dateibaum, Tabs, Gutter, Git-Listen; Datei-Operationen (neu, umbenennen, löschen, kopieren, Pfad kopieren, im Finder zeigen) – zuerst als API/Headless.
- AP10.5 Tree-sitter-Index: projektweiter Symbol-Index (Klassen, Interfaces, Traits, Methoden, extends/implements/use) → Go to Implementation/Definition ohne LSP, Gutter-Icons „implementiert/überschreibt“, Symbol-/Klassen-Suche.
- AP10.6 Git-Diff-Fenster: eigenes Diff-Fenster (side-by-side, Syntax-Highlighting, Änderungsnavigation), aus Commit-Dialog, Log, Dateibaum, Editor; Datei-Historie, Annotate (blame).
- P0 aus vergleich.md: AP10.7 Dateiwatcher & externe Änderungen (Datenverlust-Risiko!), AP10.8 Find in Files/Replace in Path, AP10.9 Aktionsmodell/Find Action/Search Everywhere, AP10.10 LSP-Ausbau (Find Usages, Alt+Enter, Reformat, Highlight Usages), AP10.11 Git Fetch/Pull/Update/Stash.
- P1 aus vergleich.md: AP10.12 Sitzung & Layout, AP10.13 Hauptmenü, AP10.14 Structure & Breadcrumbs, AP10.15 Run-Konsole & -Konfigurationen, AP10.16 Terminal, AP10.17 Editor-Alltagsaktionen, AP10.18 Debug-Ergänzungen, AP10.19 Local-History-Ansicht, AP10.20 Git-Log-Graph & Operationen, AP10.21 Qualitäts-Tools, AP10.22 Neue PHP-Klasse (PSR-4).
- **Abgelöst durch M11–M17** (Nutzer-Feedback 2026-11-17): AP10.15/10.16 → M15, AP10.21 → M17, Keymap-Anteil von AP10.17 → M13. Restliche offene M10-APs bleiben hier.

---

Die folgenden Meilensteine sind aus dem Einstellungsbaum der Referenz-IDE und einer realen Nutzer-Konfiguration abgeleitet und gegen den Code abgeglichen: [einstellungen-abgleich.md](einstellungen-abgleich.md). Alle folgen D41 (zuerst `internal/api` + Headless-Befehl + Test, dann GUI) und D190 (Kern ↔ Plugin-Einstellungen).

## M11 – Sprachserver als Plugins mit Supervisor (Nutzer-Feedback 2026-11-17)
Heute: findet `lsp/servers` nichts, zeigt die Statusleiste „No language server“ ohne Erklärung und ohne Ausweg.
- AP11.1 Ein Plugin je Server: `lsp-phpactor`, `lsp-phpantom`, `lsp-intelephense` (proprietär, Hinweis beim Einschalten). Jedes Plugin deklariert Binärnamen, Argumente, Optionen und eigene Einstellungen (Pfad, Argumente, Init-Optionen; D190). **Höchstens eines aktiv** pro Projekt (global mit Projekt-Override); „keins“ = Sprachserver aus. `lang-php` bleibt der Client und fällt ohne Server auf Tree-sitter-Index zurück (Navigation, Symbole, Outline). API `lsp.servers/select/disable`; Headless `lsp-select`.
- AP11.2 Supervisor (Kern, `internal/lsp/supervisor`, kein Gio): startet den gewählten Server **nativ** (kein Docker) beim Öffnen des Projekts bzw. der ersten PHP-Datei, prüft vorher Vorhandensein und Ausführbarkeit, wartet auf `initialize`, überwacht den Prozess (Exit, hängende Requests per Zeitlimit), Neustart mit Backoff, nach N Abstürzen in Folge Stopp + Meldung statt Endlosschleife; sauberes `shutdown`/`exit` beim Projektschließen, Abschalten oder Wechsel; offene Dokumente nach Neustart neu synchronisieren. Zustände als Events (`lsp.state`: aus / nicht gefunden / startet / läuft / abgestürzt / gestoppt). Login-Shell-PATH für macOS-GUI einmalig ermitteln und zwischenspeichern. Headless `lsp-status`, `lsp-restart`, `lsp-doctor` (durchsuchte Orte + Grund).
- AP11.3 UI: Statusleisten-Eintrag zeigt den Supervisor-Zustand und ist klickbar → Popup: Server wählen (nur eingeschaltete Plugins), aus/an, Neu starten, Log anzeigen, bei „nicht gefunden“ Grund + durchsuchte Orte + Pfad wählen + Installationshinweis. Gleicher Einstieg aus den „needs a language server“-Hinweisen.
- AP11.4 Server-Log-Fenster (stderr + JSON-RPC-Trace, opt-in).
- AP11.5 Installation auf Klick (D194, Kern-Dienst `install`, kein Gio): Plugins deklarieren ein Rezept (Paketmanager, Paket, Ziel Projekt `--dev` oder global, Interpreter). Für Sprachserver nativ (Composer global bzw. Projekt), für Werkzeug-Plugins im Projekt-Interpreter. Vorschau des Befehls, Bestätigung, Ausführung sichtbar mit Ausgabe/Abbruch, danach erkennt der Supervisor bzw. das Plugin das Programm ohne Neustart. Fehlt Composer selbst: Hinweis, kein Download. Gleicher Knopf in „Plugins › <Name>“ (M12) für `qa-pint`, `phpunit` usw. API `install.plan/run`; Headless `install <plugin> [--dry-run]`.
- **Abnahme:** Installation gegen Fake-`composer` (Befehl, Ziel, Abbruch, kein Aufruf ohne Bestätigung); Fake-LSP-Tests für Start, Absturz → Neustart, Crash-Loop-Stopp, Abschalten mitten im Request, Wechsel des Servers ohne Tiegel-Neustart; ohne Server: Grund sichtbar, Index-Navigation funktioniert; abgeschaltet: kein Prozess läuft.

## M12 – Einstellungs-Architektur: Kern und Plugins
- AP12.1 Schema: Module deklarieren Einstellungen (`module.Settings() []settings.Spec`: Schlüssel unter `<modul>.*`, Typ, Standard, Bereich global/Projekt, Label, Hilfe, Gruppe); Kern-Einstellungen ebenso als Specs. Validierung beim Schreiben, unbekannte Schlüssel bleiben erhalten. API `settings.schema`; Headless `settings --schema`.
- AP12.2 Dialog: Baum „Kern“ (Erscheinungsbild, Editor, Keymap, Dateien, Interpreter, Projekte, Updates) und „Plugins › <Name>“ (nur aktive Module, dort auch An/Aus), Suche über alle Specs, Projekt- vs. globaler Wert sichtbar, „Zurücksetzen“.
- AP12.3 Fehlende Kern-Einstellungen aus dem Abgleich: Soft Wrap, Whitespace, Lineal, `.editorconfig`, Dateityp-Zuordnungen, Standard „neues/selbes Fenster“, Git-Update-Methode (im Plugin `git`).
- **Abnahme:** jedes Modul zeigt seine Einstellungen nur über das Schema; kein Werkzeugname im Kern-Code (Test über Paket-Importe/Strings); Golden-Screenshot des Dialogs.

## M13 – Keymap-Parität & eigene Belegungen
- AP13.1 Datenmodell: Aktion → mehrere Chords, Maustasten (Zurück/Vor), Trackpad-Wischen (falls Gio liefert), Kontext (Editor/global/Tool-Fenster); Nutzer-Overrides als Kern-Einstellung `keymap.overrides`; Konflikterkennung. API `keymap.list/set/reset`; Headless `keymap`.
- AP13.2 Parität mit der macOS-Standardbelegung der Referenz-IDE (Tabelle in einstellungen-abgleich.md §2, als Test-Fixture): Back/Forward zusätzlich **Cmd+Alt+←/→** und Maustasten, Last Edit Location (Cmd+Shift+⌫), Recent Locations (Cmd+Shift+E), File Structure-Popup (Cmd+F12), Parameter Info (Cmd+P), Quick Doc (F1/Ctrl+J), Next/Prev Error (F2/Shift+F2), Join Lines, Toggle Case, Complete Statement, Paste from History, Tool-Fenster Cmd+0…9. Ein Test prüft, dass jede Fixture-Zeile belegt ist oder ausdrücklich als „später (MS)“ markiert.
- AP13.3 Keymap-Editor im Einstellungsdialog (Suche nach Aktion **und** nach Tastenkombination, Aufnahme-Feld) und Presets macOS / PC.
- **Abnahme:** Fixture-Test grün; Override überlebt Neustart; Option/Cmd-Bewegungen unverändert.

## M14 – Docker & OrbStack
- AP14.1 Docker-Verbindung (Plugin `docker`): Kontexte aus `docker context ls` (OrbStack, Docker Desktop, Colima, `DOCKER_HOST`), aktiver Kontext oder pro Projekt gewählt; `docker`-/`compose`-Pfad einstellbar (heute fest), GUI-PATH aus AP11.1. Nur lokale Sockets, nur auf Nutzeraktion bzw. beim Ausführen.
- AP14.2 Interpreter: zusätzlich „Docker-Image“ (`docker run --rm -v`), Interpreter-Info per Probe (Version, Extensions, Xdebug, ini-Dateien) mit Cache und „Neu einlesen“; Compose-Interpreter zeigt Kontext + Dienstzustand.
- AP14.3 Services-Tool-Fenster: Compose-Projekte/Dienste/Container mit Zustand, Start/Stop/Restart/Down, Logs (gestreamt, folgen, filtern), „Shell öffnen“ (Terminal aus M15 bzw. extern), Ports als Links. API `docker.*`; Headless `docker ps|logs|up|down`.
- **Abnahme:** Tests gegen Fake-`docker`-Binary; Smoke-Test mit OrbStack lokal (Skip ohne Docker).

## M15 – Ausführen, Testen, Terminal (PHPUnit-„Play“)
- AP15.1 Run-Infrastruktur (Kern): `run.start/stop/list/output` – Prozess im gewählten Interpreter (lokal/Compose/Image/WSL), Umgebungsvariablen, Arbeitsverzeichnis, Pfad-Rückmapping für klickbare `datei:zeile`, Guard `run.before` (Autosave), Bestätigung beim Schließen laufender Prozesse. Run-Konfigurationen als Projekt-Einstellung; Typen kommen aus Plugins (PHP-Skript, Composer-Skript, Befehl im Kern). Headless `run`.
- AP15.2 Plugin `phpunit`: Erkennung (`phpunit.xml(.dist)`, `vendor/bin/phpunit`, Paratest; fehlt PHPUnit: „Installieren“ per Rezept, AP11.5), Test-Erkennung per Tree-sitter (Klassen mit `TestCase`-Vorfahr über den Index, `test*`-Methoden, `#[Test]`, `@test`, `#[DataProvider]`), Ausführung mit `--teamcity` (gestreamt) über AP15.1, Filter für Suite/Verzeichnis/Datei/Klasse/Methode/Datensatz. API `test.discover/run/rerunFailed`; Headless `test`.
- AP15.3 UI: **grüner „Play“-Pfeil im Gutter** an Klasse und Methode (Run / Debug / mit Konfiguration ändern), Status-Icon des letzten Laufs; Ctrl+R/Ctrl+D/Ctrl+Shift+R; Test-Tool-Fenster mit Baum, Dauer, Fehler-Diff (expected/actual) über das Diff-Fenster, Sprung zur fehlschlagenden Zeile, „Rerun failed“, Run-Konfigurationen-Auswahl in der Toolbar. Debug-Variante setzt `XDEBUG_TRIGGER`/`XDEBUG_SESSION` und startet den Listener.
- AP15.4 Go to Test / Create Test (Cmd+Shift+T) über PSR-4 + Konvention `Tests\…Test`.
- AP15.5 Terminal (Kern): eingebettet (PTY + VT-Emulation, Tabs, Shell aus Einstellung, Option als Meta, Escape-Verhalten, Start im Projekt oder per `docker compose exec` im Dienst). Neue Abhängigkeiten in DECISIONS begründen; Zwischenschritt „Im externen Terminal öffnen“.
- Pest als eigenes Plugin danach (gleiche Schnittstelle).
- **Abnahme:** Fixture-Projekt mit PHPUnit 10/11 lokal und im Compose-Dienst (Skip ohne php/docker); Gutter-Play führt genau die Methode aus; Fehler springt zur richtigen lokalen Zeile.

## M16 – Einstellungen aus der Referenz-IDE importieren
- AP16.1 Kern-Importer (`internal/ideimport`, kein Gio): Konfig-Verzeichnisse finden (macOS `~/Library/Application Support/JetBrains/<Produkt><Version>`, Linux `~/.config/JetBrains/…`, Windows `%APPDATA%\JetBrains\…`; neueste Version zuerst), XML lesen, Makros (`$PROJECT_DIR$`, `$USER_HOME$`) auflösen. Nur lesen, nur lokal, nur auf Nutzeraktion (D191).
- AP16.2 Kern-Abbildungen: Keymap (Eltern-Kette → Overrides auf unser Preset, Aktions-ID-Zuordnungstabelle), Editor-/Konsolen-/Terminal-Schrift, Theme hell/dunkel, Tab-Breite, Terminal-Shell/Option-Meta, External Tools, Recent Projects (nur existierende, opt-in).
- AP16.3 Projekt-Import aus `.idea/`: Interpreter (lokal, `docker-compose://` mit Dienst/Dateien/Remote-Pfad/run-exec → Compose-Interpreter + Mapping), Sprachstufe, Pfad-Mappings, Run-Konfigurationen (PHPUnit Suite/Datei/Klasse/Methode, PHP-Skript; Compound als Liste).
- AP16.4 Plugin-Import-Hooks: Filter-Hook `import.ide.component` – jedes Plugin übernimmt seine Komponenten (`PhpUnit` → `phpunit`, `LaravelPint` → `qa-pint`, `PhpStan` → `qa-phpstan`, `XDebuggerSettings`/Debug → `debug-xdebug`, `DockerSettings` → `docker`, `vcs.xml` → `git`). Der Kern kennt diese Namen nicht.
- AP16.5 UI + Headless: Assistent „Einstellungen importieren…“ (Willkommensbildschirm und Projekt öffnen, wenn `.idea/` vorhanden): Vorschau mit Häkchen je Bereich, Bericht übernommen / übersprungen / nicht unterstützt; `tiegel --headless import-ide [--project DIR] [--dry-run] [--json]`.
- **Abnahme:** Fixture-Konfigurationen (synthetisch, keine echten Nutzerdaten) für App und Projekt; Trockenlauf ändert nichts; Bericht vollständig.

## M17 – Werkzeug-Plugins & Editor-Komfort
- AP17.1 Qualitäts-Tools als **je eigenes Plugin** (`qa-pint`, `qa-php-cs-fixer`, `qa-phpcs`, `qa-phpstan`, `qa-psalm`, `qa-phpmd`): Pfad (Standard `vendor/bin/<tool>`), Konfigdatei, Interpreter (über AP15.1), Timeout, Schweregrad; Formatter-Plugins hängen sich an Reformat bzw. `file.beforeSave`, Analyse-Plugins liefern Diagnosen in Problems (bei Speichern/auf Wunsch). Fehlt das Tool: „Installieren“ per Rezept (AP11.5, D194), sonst keine Downloads.
- AP17.2 Composer-Skripte und External Tools als Run-Typen.
- AP17.3 Live Templates/Snippets (Plugin), Datei-Vorlagen, Generate (Cmd+N: Konstruktor, Getter/Setter über LSP-Code-Actions bzw. Tree-sitter), Surround With, Extract Variable/Method über LSP-Code-Actions, Inlay Hints, Include-Pfade.
- **Abnahme:** Kern-Code enthält keinen Werkzeugnamen; jedes Plugin einzeln abschaltbar; Pint-Format bei Speichern im Compose-Dienst getestet (Skip ohne docker).

## M18 – Shell-Überarbeitung: Gefühl, Ikonografie, Fenster-Management (Nutzer-Feedback 2026-11-20)
Ziel: dasselbe aufgeräumte, dichte Arbeitsgefühl wie die Referenz-IDE – eigene Gestaltung, keine fremden Icons (Clean-Room).
- AP18.1 **Eigenes Icon-Set:** Dateisymbole je Sprache/Dateityp (PHP, TS, JS, JSON, YAML, Markdown, Docker, Shell, XML, SQL, .env, .gitignore, .editorconfig, composer/package-Dateien …) mit Varianten für Tests (`*.spec.ts`, `*Test.php`), Ordnersymbole (Quellen, Tests, Ressourcen, ausgeschlossen, Bibliothek), einheitliche Tool-Fenster-Symbole. Mehrfarbig, pixelgenau bei 16 dp, keine Umbrüche.
- AP18.2 **Workbench & Tokens:** linke/rechte Leiste nur mit Symbolen (Tooltip), obere und untere Gruppe (Run, Debug, Terminal, Problems, Git unten links); keine untere Leiste mehr – **ein** lesbarer Footer (Breadcrumb links, Status rechts). Abstände/Trefferflächen überarbeitet, Kopfzeilen der Panels sauberer. Fenster-Management: zwei Tool-Fenster je Seite geteilt (oben/unten), Panel maximieren, Größen je Projekt.
- AP18.3 **Kopfzeile (Toolbar):** Projekt-Wechsler, daneben Branch-Popup inline (Suche, lokal/remote, neu, Checkout), dann Run-Konfiguration + Run/Debug/Stop, Xdebug-Lauschen, Plugin-Schnellknöpfe (Beitragspunkt für Plugins), Suche, Einstellungen.
- AP18.4 **VCS:** Commit-Tool-Fenster links (Commit/Stash, Werkzeugleiste, Änderungsbaum mit Häkchen und Dateisymbolen, Amend, Nachricht, „Commit“ / „Commit and Push“); Diff öffnet als Editor-Tab im Hauptfenster und wird fokussiert.
- AP18.5 **Mehrere Projekte je Fenster + Ruhezustand:** Projekte im Hintergrund behalten ihren Zustand; „Ruhezustand“ beendet Sprachserver, Watcher, Indexer und gibt Speicher frei, Wiederaufnahme stellt Tabs/Layout wieder her. Headless: `tiegel --headless projects list|open|switch|hibernate|resume`.
- AP18.6 **Terminal zuerst „vernünftig“:** vollwertiges Terminal (Vollbild-Programme wie vim/htop/less, 256/True Color, Maus, Auswahl/Kopieren/Einfügen, Scrollback + Suche, Größenänderung, Links, mehrere Tabs, Shell-Integration/Prompt-Erkennung), Robustheit und Geschwindigkeit.
- AP18.7 **Coding-Agenten als Plugins** (nach AP18.6): zunächst Kopfzeilen-Knopf je Agent (Claude Code, opencode …), Start im eingebetteten Terminal im Projekt. Später, ebenfalls nur als Plugin: kleine eigene Oberfläche statt Kommandozeile – Ein- und Ausgaben werden geparst und in Blöcken dargestellt (Nutzer 2026-11-20: „nicht jetzt, nur als Plugin“).
- AP18.8 **Fenster-Management per Drag & Drop:** Tool-Fenster am Panel-Kopf oder Leistenknopf ziehen, Andockziele mit Vorschau (links/rechts/unten, jeweils primär/geteilt, als Tab in ein vorhandenes Panel); **Ecken-Zuordnung** wählbar: unteres Panel über volle Breite (z. B. Terminal unter Projekt + Editor) oder Seitenpanels über volle Höhe – per Ziehen an die Kante oder Menü; Layout je Projekt gespeichert, „Layout zurücksetzen“, benannte Layouts.
- **Abnahme:** Screenshot-Vergleich vorher/nachher im Review; alle Symbole eigene Zeichnungen; keine Textumbrüche in Knöpfen bei 100 %/125 %/150 %; Diff-Tab per Headless-Befehl prüfbar.

---

## Ausführungsplan (Wellen)

| Welle | Inhalt | Parallel |
| --- | --- | --- |
| 0 | M0 | – |
| 1 | AP1.1–1.6 · AP3.1–3.3 · AP2.1–2.4 · AP4.1 · AP7.1 · AP6.1 · AP5.1–5.4 (Backend) | ja, je Paket ein Agent |
| 2 | AP2.5 Shell · AP3.4–3.6 Editor-Widget + Highlighting | teilweise |
| 3 | Modul-Hüllen + UI: AP4.2–4.3 · AP5.5 · AP6.2–6.5 · AP7.2–7.3 | ja |
| 4 | M8, M9 | ja |
| 5 | M11 (LSP-Plugins + Supervisor) · M12.1 (Einstellungs-Schema) · M13.1–13.2 (Keymap-Modell + Parität) · M14.1 (Docker-Kontexte) | ja, disjunkte Pakete |
| 6 | M12.2–12.3 · M13.3 · M14.2–14.3 · M15.1 (Run-Infrastruktur) | ja |
| 7 | M15.2–15.5 (PHPUnit, Gutter-Play, Terminal) · M17.1 (QA-Plugins, nach M15.1) | ja |
| 8 | M16 (Import – bildet auf M12–M15 ab) · M17.2–17.3 | ja |
| 9 | M18.1–18.5 (Icons · Workbench/Tokens/Footer · Toolbar-Organismus · VCS-Organismen · Projekt-Sitzungen + Ruhezustand im Backend), danach GUI-Integration | ja, disjunkte Pakete |
