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
| M11–M17 | Nutzer-Feedback 2026-11-17: LSP-Plugins + Supervisor, Kern/Plugin-Einstellungen, Keymap, Docker/OrbStack, Run/PHPUnit/Terminal, IDE-Import, QA-Plugins | 🟡 | ✅ Welle 5: M11 (LSP-Plugins, Supervisor, Status-Popup, Installation auf Klick), M12.1–12.3 (Schema, Dialog Kern/Plugins, .editorconfig, Zuordnungen), M13.1–13.2 (Keymap-Modell, Overrides, Parität 88/103), M14.1–14.2 (Docker-Kontexte, Image-Interpreter, Probe-Cache); Review 18 Befunde behoben (docs/reviews/2026-11-17-welle5.md). Offen: Maustasten/Wischen auf echter Hardware prüfen, Windows-Prozessbaum (Job Object) nur cross-kompiliert; ✅ Welle 6: Soft Wrap/Whitespace/Lineal, Keymap-Editor (M13.3), Services-Fenster (M14.3), Run-Infrastruktur + Run-Fenster (M15.1); Review 16 Befunde behoben (docs/reviews/2026-11-17-welle6.md), WSL-Stop nur mit Fake-wsl.exe getestet. Offen: M15.2–15.5 (PHPUnit-Play, Terminal), M16, M17 |

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
