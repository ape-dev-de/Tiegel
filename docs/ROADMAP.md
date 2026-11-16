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

---

## Ausführungsplan (Wellen)

| Welle | Inhalt | Parallel |
| --- | --- | --- |
| 0 | M0 | – |
| 1 | AP1.1–1.6 · AP3.1–3.3 · AP2.1–2.4 · AP4.1 · AP7.1 · AP6.1 · AP5.1–5.4 (Backend) | ja, je Paket ein Agent |
| 2 | AP2.5 Shell · AP3.4–3.6 Editor-Widget + Highlighting | teilweise |
| 3 | Modul-Hüllen + UI: AP4.2–4.3 · AP5.5 · AP6.2–6.5 · AP7.2–7.3 | ja |
| 4 | M8, M9 | ja |
