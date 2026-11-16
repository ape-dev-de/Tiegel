# Status

Letzte Aktualisierung: 2026-11-16

Legende: ⬜ offen · 🟡 in Arbeit · ✅ fertig · ⚠️ blockiert/Teilstand

| AP | Thema | Status | Notiz |
| --- | --- | --- | --- |
| M0 | Fundament, CI | ✅ | Makefile, CI (macOS+Linux), GitHub-Meilensteine M0–M9 |
| 1.1–1.3 | Event-Bus, Hooks, Modul-Registry | ✅ | 97 % Abdeckung; typisierte Hooks (D40) |
| 1.4–1.6 | Settings, Spracherkennung, Local History, Autosave | ✅ | Payloads für run/debug/commit-Guards folgen mit den Modulen |
| 2.1–2.4 | Tokens, Atome, Moleküle, Katalog | ✅ | Gio v0.10.3; Golden-Screenshots noch offen |
| H | Headless-Modus, `internal/api` | 🟡 | D41, Welle 2 |
| 2.5 | Hauptfenster-Shell | ⬜ | nach `internal/api` |
| 3.1–3.3 | Puffer, Cursor, Suche, Folding | ✅ | Piece Table/Treap, 100k Zeilen laden 2 ms, Insert ~2 µs, 98.8 % |
| 3.4–3.6 | Editor-Widget, Highlighting, Keymap | 🟡 | Welle 2 |
| 4.1 | LSP-Client | ✅ | Fake-Server-Tests; Smoke-Test mit Phpactor offen (nicht installiert) |
| 4.2–4.3 | lang-php Modul + UI | ⬜ | |
| 5.1–5.4 | Interpreter, Compose, PHP-Version, Features | ✅ | interp/compose 96–98 %; syntax (Tree-sitter) + phpver 96 %, 41 Features + 13 Deprecations; Erst-Parse 100k Zeilen ~0,5 s → im Hintergrund |
| 5.5 | Runtime-UI | ⬜ | |
| 6.1 | Git-CLI-Wrapper | ✅ | 95 %; Teil-Commit über temporären Index, Myers-Diff, Merge3; git ≥ 2.28 |
| 6.2–6.5 | Git-UI, Commit-Dialog, Merge | ⬜ | |
| 7.1 | DBGp-Server | ✅ | 93 %; Test mit echtem Xdebug offen (lokal nicht installiert) |
| 7.2–7.3 | Debug-Modul + UI | ⬜ | |
| M8 | Mac-Paketierung | ⚠️ | Skripte, Universal-App, DMG, release.yml, opt-in Update-Check fertig; echte Signatur/Notarisierung braucht Apple-Zertifikate + Secrets (docs/release.md) |
| M9 | Linux/WSL | ⬜ | |

## Log
- 2026-11-16: Konzept nach `docs/konzept.md` verschoben, Roadmap, Entscheidungen, Agenten-Konventionen angelegt.
- 2026-11-16: Welle 1 gemergt: core, editor-Modell, UI-Baukasten, LSP, DBGp, interp/compose. Nutzerwunsch: Headless-Modus (D41).
