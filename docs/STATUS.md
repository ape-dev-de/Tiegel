# Status

Letzte Aktualisierung: 2026-11-17

Legende: ⬜ offen · 🟡 in Arbeit · ✅ fertig · ⚠️ blockiert/Teilstand

| AP | Thema | Status | Notiz |
| --- | --- | --- | --- |
| M0 | Fundament, CI | ✅ | Makefile, CI (macOS+Linux), GitHub-Meilensteine M0–M9 |
| 1.1–1.3 | Event-Bus, Hooks, Modul-Registry | ✅ | 97 % Abdeckung; typisierte Hooks (D40) |
| 1.4–1.6 | Settings, Spracherkennung, Local History, Autosave | ✅ | Payloads für run/debug/commit-Guards folgen mit den Modulen |
| 2.1–2.4 | Tokens, Atome, Moleküle, Katalog | ✅ | Gio v0.10.3; Golden-Screenshots noch offen |
| H | Headless-Modus, `internal/api` | ✅ | ~50 API-Methoden, CLI + `serve` (NDJSON/LSP-Framing), E2E-Tests; siehe docs/headless.md |
| 2.5 | Hauptfenster-Shell | ✅ | Workbench: Dateibaum, Tabs, Editor, Problems, Statusleiste, Einstellungen, Willkommen, Fuzzy-Datei-Suche; erstes Frame ~0,8–1,2 s warm |
| 3.1–3.3 | Puffer, Cursor, Suche, Folding | ✅ | Piece Table/Treap, 100k Zeilen laden 2 ms, Insert ~2 µs, 98.8 % |
| 3.4–3.6 | Editor-Widget, Highlighting, Keymap | ⚠️ | Widget + Keymap ✅ (100k Zeilen: Tastendruck→Frame ~0,2 ms); Tree-sitter-Adapter ✅ (tshighlight); Emoji/CJK-Fallback-Font offen |
| 4.1 | LSP-Client | ✅ | Fake-Server-Tests; Smoke-Test mit Phpactor offen (nicht installiert) |
| 4.2–4.3 | lang-php Modul + UI | 🟡 | Modul + API (`lsp.*`, Auto-Sync, Diagnosen) ✅; UI (Completion, Hover, Signatur, Go to Definition/Implementation) ✅; Test mit echtem Phpactor offen |
| 5.1–5.4 | Interpreter, Compose, PHP-Version, Features | ✅ | interp/compose 96–98 %; syntax (Tree-sitter) + phpver 96 %, 41 Features + 13 Deprecations; Erst-Parse 100k Zeilen ~0,5 s → im Hintergrund |
| 5.5 | Runtime-UI | ✅ | Interpreter/Mappings/Hinweise in Einstellungen, Versions-Override in Statusleiste |
| 6.1 | Git-CLI-Wrapper | ✅ | 95 %; Teil-Commit über temporären Index, Myers-Diff, Merge3; git ≥ 2.28 |
| 6.2–6.5 | Git-UI, Commit-Dialog, Merge | ✅ | Commit-Fenster (Hunk/Zeilen), Log, Branches, Merge-Fenster, Gutter-Marker + Rollback; Push nur gegen Fake getestet |
| 7.1 | DBGp-Server | ✅ | 93 %; Test mit echtem Xdebug offen (lokal nicht installiert) |
| 7.2–7.3 | Debug-Modul + UI | ✅ | Debug-Fenster, Breakpoint-Gutter, Ausführungspunkt, Variablen/Watches; Test mit echtem Xdebug offen |
| M8 | Mac-Paketierung | ⚠️ | Skripte, Universal-App, DMG, release.yml, opt-in Update-Check fertig; echte Signatur/Notarisierung braucht Apple-Zertifikate + Secrets (docs/release.md) |
| M10 | Parität & Fenster-Management | 🟡 | ✅ Fenster-Management, Projekte, Menüleiste, Systemschriften, Tree-sitter-Navigation (Cmd+B/Cmd+Alt+B/Cmd+U, Gutter ↑↓, Go to Class/Symbol, Search Everywhere), Kontextmenüs (Editor/Baum/Tabs/Gutter), Datei-Operationen, Dateiwatcher + Konflikt-Banner, Diff-Tabs, Annotate, Historie, Local History, Find in Files, Usages/Alt+Enter/Reformat/Rename (LSP), Fetch/Pull/Stash; CI-Fix (Linux/Windows) in Arbeit; offen: P1-Rest aus vergleich.md (Terminal, Run-Konfigurationen, Sitzung speichern, Debug-Ergänzungen, Log-Graph, Qualitäts-Tools) |
| M9 | Linux/WSL | 🟡 | Backend ✅: Socket-Transport mit Token, `api.Caller` (in-process/remote), Client, docsync, WSL-Interpreter + Pfade, `syntax.*`-API, CI Windows/Cross; GUI-Umschaltung, Pakete/Installer offen |
| M11–M17 | Nutzer-Feedback 2026-11-17: LSP-Plugins + Supervisor, Kern/Plugin-Einstellungen, Keymap, Docker/OrbStack, Run/PHPUnit/Terminal, IDE-Import, QA-Plugins | 🟡 | ✅ Welle 5: M11 (LSP-Plugins, Supervisor, Status-Popup, Installation auf Klick), M12.1–12.3 (Schema, Dialog Kern/Plugins, .editorconfig, Zuordnungen), M13.1–13.2 (Keymap-Modell, Overrides, Parität 88/103), M14.1–14.2 (Docker-Kontexte, Image-Interpreter, Probe-Cache); Review 18 Befunde behoben (docs/reviews/2026-11-17-welle5.md). Offen: Maustasten/Wischen auf echter Hardware prüfen, Windows-Prozessbaum (Job Object) nur cross-kompiliert; ✅ Welle 6: Soft Wrap/Whitespace/Lineal, Keymap-Editor (M13.3), Services-Fenster (M14.3), Run-Infrastruktur + Run-Fenster (M15.1); Review 16 Befunde behoben (docs/reviews/2026-11-17-welle6.md), WSL-Stop nur mit Fake-wsl.exe getestet; ✅ Welle 7: PHPUnit-Plugin mit Gutter-Play, Test-Fenster, Go to Test (M15.2–15.4), eingebettetes Terminal (M15.5), QA-Plugins (M17.1); Review 13 Befunde behoben (docs/reviews/2026-11-18-welle7.md). ✅ Welle 8: Import (M16), Composer + External Tools (M17.2), Live Templates, Datei-Vorlagen, Generate, Inlay Hints (M17.3). Review 9 Befunde behoben (docs/reviews/2026-11-18-welle8.md). Offen: Import gegen echte Konfiguration prüfen; Windows-Pfade (ConPTY, .bat-Schutz) nur cross-kompiliert bzw. simuliert |
| M18 | Nutzer-Feedback 2026-11-20: Shell-Überarbeitung (Gefühl, Ikonografie, Fenster-Management) | 🟡 | ✅ Welle 9: eigenes Icon-Set (18.1), Symbol-Leisten + ein Footer + geteilte Panels + Maximieren (18.2), Kopfzeile mit Projekt-/Branch-Popup, Run/Debug, Xdebug, Plugin-Knöpfen (18.3), Commit-Fenster + Diff als Editor-Tab (18.4), mehrere Projekte je Fenster mit Ruhezustand (18.5), Terminal-Emulation/Darstellung/Auswahl/Suche (18.6), Drag & Drop + unteres Panel über volle Breite + benannte Layouts (18.8), Merge/Rebase, Ordnerrollen aus composer.json; Review 21 Befunde behoben (docs/reviews/2026-11-20-welle9.md). Offen: Terminal auf echter Hardware prüfen (IME, Option-Tasten, Maus in TUIs), AP18.7 Coding-Agenten-Plugins, Kitty-Tastaturprotokoll |

## Log
- 2026-11-16: Konzept nach `docs/konzept.md` verschoben, Roadmap, Entscheidungen, Agenten-Konventionen angelegt.
- 2026-11-16: Welle 1 gemergt: core, editor-Modell, UI-Baukasten, LSP, DBGp, interp/compose. Nutzerwunsch: Headless-Modus (D41).
- 2026-11-16: Headless-Modus gemergt; Fix: PHP-Version fällt ohne gewählten Interpreter auf ersten lokalen zurück. M8 gemergt (ohne echte Signatur).
- 2026-11-16: Module langphp, phpruntime, phpfeatures, git, debugxdebug als headless Backend gemergt (D42); geteilte Puffer für In-Process-GUI (`App.Document`).
- 2026-11-16: M9-Backend gemergt (D80–D83).
- 2026-11-16: GUI Teil A gemergt (D90–D93). Nächster Schritt: Git- und Debug-Anbindung (Teil B).
- 2026-11-16: GUI Teil B gemergt (D100–D103) – MVP-Funktionsumfang vollständig verdrahtet. Review-Fixes in Arbeit.
- 2026-11-16: Review-Fixes (14 Befunde, je Regressionstest) gemergt; siehe docs/reviews/2026-11-16-backend.md.
- 2026-11-16: GUI-Nacharbeiten aus Review gemergt (Hunk-Hash, Recovery-Banner, Container-Hinweis, Dateibaum-Bug).

- 2026-11-16: M10-Backends gemergt (D110–D154), Vergleich in docs/vergleich.md.

- 2026-11-16: M10-GUI-Integration gemergt (D160–D174).

## Offen / braucht den Nutzer
- Echte Tests mit **Phpactor** (nicht installiert) und **Xdebug** (PHP 8.5 lokal ohne Xdebug) – Abnahme M4/M7.
- **Signieren/Notarisieren**: Apple-Developer-Zertifikat + GitHub-Secrets (docs/release.md) – Abnahme M8.
- Emoji/CJK-Ersatzschrift im Editor (Entscheidung zu z.B. Noto-Fonts, Lizenz OFL).
- Windows-GUI ↔ WSL-Backend in der GUI umschaltbar machen (Backend fertig), Linux-Paket, Windows-Installer.
- Golden-Screenshot-Tests als Abnahme M2 (Screenshots werden erzeugt, aber nicht verglichen).
- 2026-11-16: CI-Fixes gemergt (Windows-Pfade/EOL, Build-Tag `headless`, D84/D85). CI auf main komplett grün (macOS, Linux, Windows, Cross).
- 2026-11-16: CI-Fixes M10 gemergt (D180–D183): git ≥ 2.48 Fetch, Windows-Watcher mit einem Handle, Groß/Klein-Umbenennung, Headless-Tests 75 s → 16 s.
- 2026-11-17: Einstellungs-Abgleich mit Referenz-IDE (docs/einstellungen-abgleich.md), Meilensteine M11–M17 geplant (D190–D194).
- 2026-11-17: Welle 5 gemergt (M11–M14 Teil 1, D200–D250), Review-Fixes gemergt; CI-Fixes (gitcli-403-Klassifizierung, Windows-Pfade/Kill).
- 2026-11-17: Welle 6 gemergt (D260–D289); Review von Welle 6 steht aus.
- 2026-11-18: Review-Fixes Welle 6 gemergt (D290–D309): Soft-Wrap-Caret, Flaky-Tests, Services-Logströme/Windows-Terminal, Run-Stop in Debian-Containern, Schließen-Bestätigung, WSL-Stop.
- 2026-11-18: CI-Fixes: Ctrl+R nach Start aus Konfiguration (Auswahl persistiert), Client-Close blockiert nicht an Windows-Pipes, Install-OnDone vor Wait. CI komplett grün (macOS, Linux, Windows, Cross). Welle 7 läuft (M15.2–15.5, M17.1).
- 2026-11-18: Welle 7 gemergt (D310–D359): PHPUnit-Plugin mit Gutter-Play/Test-Fenster/Go to Test (M15.2–15.4), eingebettetes Terminal (M15.5), QA-Plugins (M17.1). Review 13 Befunde behoben (docs/reviews/2026-11-18-welle7.md). Offen: Welle 8 (M16 Import, M17.2–17.3).
- 2026-11-18: Review-Fixes Welle 7 gemergt (D360–D381): Formatter mit echtem Dateinamen, Save-Timeout, .bat-Injection (Windows), Paste-Filter, Container-Shell-Stop, Testausgabe begrenzt. Windows-ConPTY/.bat nur cross-kompiliert; Pint/PHP-CS-Fixer nicht real getestet.
- 2026-11-18: Welle 8 gemergt (D390–D430): Einstellungs-Import aus der Referenz-IDE (M16), Composer-Plugin + External Tools (M17.2), Live Templates, Datei-Vorlagen, Generate, Inlay Hints (M17.3). Offen: Review Welle 8.

- 2026-11-18: Review-Fixes Welle 8 gemergt (D440–D463): Import-Vertrauensgrenze (.idea darf nichts Globales, keine Repo-Programme ohne Opt-in), Projekt-Angebot nur Projekt + Backup, Surround-With-Absturz, Panic-Recovery in der API, Tool-Läufe ohne Auswahlwechsel, gemeinsamer Windows-Batch-Schutz.
- 2026-11-18: CI komplett grün nach Welle 8 + Review-Fixes (macOS, Linux, Windows, Cross). M11–M17 umgesetzt.
- 2026-11-20: Welle 9 (M18) gemergt (D464–D501): eigenes Icon-Set, Symbol-Leisten, ein Footer, Kopfzeile, Commit-Fenster + Diff-Tab, mehrere Projekte je Fenster + Ruhezustand, Terminal-Überarbeitung, Drag & Drop-Fenster-Management, Merge/Rebase, Ordnerrollen aus composer.json. Review 21 Befunde behoben. Außerdem: Chooser-Absturz, Linker-Warnung `-lobjc`.
- 2026-11-20: CI komplett grün nach Welle 9 + Review-Fixes (macOS, Linux, Windows, Cross); Windows-Pfade in Workspace-Tests, Terminal-Busy-Test ohne Job-Kontrolle übersprungen.
- 2026-11-23: Projektbaum und Commit-Fenster scrollen horizontal bei langen Pfaden (D502); Knopf „Select Opened File“ im Projektfenster, Auswahl scrollt in den sichtbaren Bereich (D503). Hänger bei Cmd+, (Settings) und Klicks im nativen Menü behoben: Deadlock auf dem Main-Thread, Rückrufe laufen jetzt über eine eigene Warteschlange; Aktionen fangen Panics (D504).
- 2026-11-23: Hintergrund-Tasks mit Statuszeile, Abbrechen und `tasks`-CLI (D505–D509); UI-Freeze-Watchdog mit lokalen Berichten und Test gegen blockierende Aufrufe im UI-Thread (D510–D514); zentrales PHP-Projektverzeichnis `project.phpRoot` – Gutter-Testläufe funktionieren in verschachtelten Projekten (D515–D519); Einstellungen mit erkannten Kandidaten statt Freitext, Compose-Erkennung in übergeordneten Ordnern, Projekt-Setup (D520–D529); `serve` verliert keine Ereignisse am Verbindungsende (D530). Unabhängiges Review läuft.
- 2026-11-23: Kontextmenü für Tests im Projektbaum und Editor: Run/Debug von Testverzeichnissen, Testdateien, „Run All Tests“ auf Elternordnern, „Run … with Filter…“ (PHPUnit `--filter`) und Modify Run Configuration… (D533).
- 2026-11-23: Tests-Fenster: Konsole des Laufs (Header, Fehlerblöcke, Zusammenfassung), gesammelte Ausgaben je Klasse/Suite, farbige Status-Icons (grün/orange/rot), Fortschrittsbalken; `test.node` ohne ID liefert die Konsole (D535). Mit PHPUnit 11.5 geprüft.
- 2026-11-23: Markdown-Reader ohne WebView: Parser `internal/markdown` (goldmark), API `md.*`, headless `md`, Organismus `mdview`, Vorschau-Tab (auch daneben) mit Live-Update, Links, lokalen Bildern; kursive UI-Schrift auf macOS (D534).
- 2026-11-23: Tests-Fenster zeigt Assertions je Test/Klasse/Lauf und die Fundstelle des Tests (JUnit-Log über stderr, D536). Mit PHPUnit 11.5 geprüft.
- 2026-11-25: `make install` baut Tiegel für diesen Rechner und installiert es (macOS: `Tiegel.app` nach `/Applications`, sonst Binary nach `~/.local/bin`; `APP_DIR`, `BIN_DIR`, `ARCH`).
- 2026-11-25: Find in Files mit Live-Suche, Trefferliste und Code-Vorschau im Dialog (D537); Einfügen/Kopieren ins fokussierte Terminal geht nie mehr in den Editor, Klick ins Terminal-Fenster fokussiert es (D538).
- 2026-11-25: Neues minimalistisches Logo: hellgrauer Tiegel mit Hitzestrichen auf dunkelgrüner Kachel, SVG in `packaging/macos/icon.svg` (D539).
- 2026-11-26: Markdown-Dateien öffnen geteilt (links Code, rechts Live-Vorschau), abschaltbar mit `markdown.openMode` (D542).
- 2026-11-26: Phpactor-Installation lädt das PHAR nach `~/.local/bin` statt Composer global (scheiterte mit „requires php ^7“, D543); Escape geht im fokussierten Terminal an das Programm (D544).
- 2026-11-26: Fix: Tiegel hing, wenn der Projektwechsler ein in einem anderen Fenster offenes Projekt nach vorn holte (D545).
- 2026-11-26: Blockauswahl (mittlere Maustaste/Option+Ziehen), Caret klonen mit Option Option+↑/↓, Spaltenmodus (D546). Phpactor-PHAR startet mit lokalem PHP ≥ 8.2 statt `env php` (D548).
- 2026-11-26: KI-Agent-Seitenleiste über ACP (opencode): Diff, Revert, von Tiegel durchgesetzte Berechtigungen (Commits/Push standardmäßig gesperrt), headless `agent` (D540, D549, D550).
- 2026-11-26: Markdown-Vorschau: Mermaid-Diagramme nativ (Flowchart, Sequenz, State, Class, ER, Pie), Syntax-Highlighting in Codeblöcken, headless `mermaid` (D551, D552).
- 2026-11-26: Projektwechsel standardmäßig im selben Fenster; das verlassene Projekt geht in den Ruhezustand (Terminals laufen weiter), „Open in New Window“ per Rechtsklick/Shift (D547).
- 2026-11-26: Projektbaum: entfernte Einträge verlassen den Index (D553); „Reload All from Disk“ (⌥⌘Y, Knopf im Baumkopf) und „Reload from Disk“ im Kontextmenü (D554).
- 2026-11-26: Dockerfile (inkl. Containerfile, `Dockerfile.*`) wird erkannt und im Editor eingefärbt, dazu Shell, Makefile, INI, SQL und Ignore-Dateien über den eingebauten Lexer (D555).
- 2026-11-26: Projektbaum zeigt per `.gitignore` ignorierte Einträge olivfarben markiert an (abschaltbar mit `files.showIgnored`, D558).
- 2026-11-26: Ignorierte PHP-Dateien werden indiziert und von Quick Open/Shift+Shift gefunden; Cache-/Temp-Ordner bleiben ausgeschlossen (D559).
- 2026-11-26: PHPStan-Ergebnisse ohne eigenen Lauf (Result-Cache 1.x–2.2, Report-Dateien json/checkstyle/junit, Container-Pfade, ignoreErrors/Baseline) dezent im Editor und im Problems-Fenster; „Fix with AI“ über den Agenten; headless `analysis` (D556, D557).
- 2026-11-26: Projektwissen: Agenten-Pipeline describe → validate → write (nur lesend, nur auf Knopfdruck), Markdown außerhalb des Repos, hybride Suche (Ollama/EmbeddingGemma 2 oder OpenAI-kompatibel + BM25, Brute-Force-Store hinter Store-Interface), MCP-Server `mcp knowledge` für opencode; Fenster „Knowledge“, headless `knowledge` (D560–D563).
