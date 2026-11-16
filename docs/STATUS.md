# Status

Letzte Aktualisierung: 2026-11-16

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
| 5.5 | Runtime-UI | 🟡 | Module php-runtime/php-features ✅ (Mappings, Override, Hintergrund-Check); Organismen ✅; Anbindung in Shell offen |
| 6.1 | Git-CLI-Wrapper | ✅ | 95 %; Teil-Commit über temporären Index, Myers-Diff, Merge3; git ≥ 2.28 |
| 6.2–6.5 | Git-UI, Commit-Dialog, Merge | 🟡 | Organismen ✅ (CommitPanel, DiffView, LogView, BranchList, MergeView); Anbindung in Shell offen; Gutter-Marker offen |
| 7.1 | DBGp-Server | ✅ | 93 %; Test mit echtem Xdebug offen (lokal nicht installiert) |
| 7.2–7.3 | Debug-Modul + UI | 🟡 | Modul debug-xdebug ✅ (Breakpoints persistent, Stepping, Variablen, Watches); Organismen ✅; Anbindung in Shell offen |
| M8 | Mac-Paketierung | ⚠️ | Skripte, Universal-App, DMG, release.yml, opt-in Update-Check fertig; echte Signatur/Notarisierung braucht Apple-Zertifikate + Secrets (docs/release.md) |
| M9 | Linux/WSL | 🟡 | Backend ✅: Socket-Transport mit Token, `api.Caller` (in-process/remote), Client, docsync, WSL-Interpreter + Pfade, `syntax.*`-API, CI Windows/Cross; GUI-Umschaltung, Pakete/Installer offen |

## Log
- 2026-11-16: Konzept nach `docs/konzept.md` verschoben, Roadmap, Entscheidungen, Agenten-Konventionen angelegt.
- 2026-11-16: Welle 1 gemergt: core, editor-Modell, UI-Baukasten, LSP, DBGp, interp/compose. Nutzerwunsch: Headless-Modus (D41).
- 2026-11-16: Headless-Modus gemergt; Fix: PHP-Version fällt ohne gewählten Interpreter auf ersten lokalen zurück. M8 gemergt (ohne echte Signatur).
- 2026-11-16: Module langphp, phpruntime, phpfeatures, git, debugxdebug als headless Backend gemergt (D42); geteilte Puffer für In-Process-GUI (`App.Document`).
- 2026-11-16: M9-Backend gemergt (D80–D83).
- 2026-11-16: GUI Teil A gemergt (D90–D93). Nächster Schritt: Git- und Debug-Anbindung (Teil B).
