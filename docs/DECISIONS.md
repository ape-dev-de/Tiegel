# Entscheidungen (ADR-light)

Beantwortet die offenen Fragen aus [konzept.md](konzept.md). Jede Entscheidung ist revidierbar; Änderungen hier dokumentieren.

| # | Frage | Entscheidung | Begründung |
| --- | --- | --- | --- |
| D1 | Repository-Name | `ape-dev-de/Tiegel`, Go-Modul `github.com/ape-dev-de/tiegel` | Repo existiert bereits; Modulpfad klein geschrieben (GitHub ist case-insensitiv). |
| D2 | Editor-Puffer | **Piece Table** (mit balanciertem Baum über die Pieces + Zeilenumbruch-Zähler pro Knoten) | Original bleibt unverändert → billiges Undo/Snapshots, gut für große Dateien, wie VS Code. |
| D3 | Standard-Language-Server | **Phpactor** Standard, PHPantom als Alternative | Phpactor ist ausgereifter; beide MIT. Auswahl in den Einstellungen. Kein automatischer Download (Konzept). |
| D4 | Lizenz | **MIT** | `LICENSE` liegt bereits so im Repo. |
| D5 | PHPUnit-Testbaum im MVP | **Nein**, Phase 2 | MVP-Umfang klein halten. |
| D6 | Go-Version | Go 1.24 (`go.mod`) | Lokal installiert, Gio v0.10 verlangt ≥ 1.24. |
| D7 | Tree-sitter-Anbindung | `github.com/tree-sitter/go-tree-sitter` (cgo) + offizielle Grammatik-Bindings | Offizielle Bindings, MIT; cgo ist für Gio auf macOS ohnehin nötig. |
| D8 | Persistenz Einstellungen | Intern verwaltete Dateien unter `os.UserConfigDir()/Tiegel` (global) und `…/projects/<hash>` (pro Projekt) | „Ohne Konfig-Dateien“ heißt: nichts im Projekt, nichts von Hand zu editieren. |
| D9 | Sprache | Code, Bezeichner, Kommentare: Englisch. Doku in `docs/`: Deutsch. UI-Texte: Englisch zuerst (i18n-fähig). | Offenes Projekt, breite Zielgruppe. |
| D30 | Compose-Dateien lesen | `github.com/compose-spec/compose-go/v2` (Apache-2.0) in `internal/compose` | Referenz-Implementierung der Compose-Spezifikation, aktiv gepflegt (Docker); liefert Merge von Override-Dateien, `.env`-Interpolation, Profile und Pfadauflösung exakt wie `docker compose`. Eigener Parser wäre fehleranfällig. Ausgeführt wird weiterhin über die `docker compose`-CLI. |
| D10 | Grapheme-Cluster im Editor | `github.com/rivo/uniseg` (MIT) | Unicode-konforme Grapheme-Grenzen (UAX #29) und Monospace-Breiten für Cursorbewegung, Spalten und Löschen; reines Go ohne cgo, aktiv gepflegt, keine weiteren Abhängigkeiten. Eigenbau der Unicode-Tabellen wäre fehleranfällig. |
