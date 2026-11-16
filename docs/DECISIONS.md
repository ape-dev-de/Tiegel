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
| D20 | Tree-sitter-Grammatiken (`internal/syntax`) | `tree-sitter/tree-sitter-php` v0.25.1 (Grammatik `php`, d.h. PHP in HTML-Text), `tree-sitter/tree-sitter-json` v0.24.8, `tree-sitter-grammars/tree-sitter-yaml` v0.7.2, `tree-sitter-grammars/tree-sitter-xml` v0.7.0 zu `go-tree-sitter` v0.25.0 (D7); transitiv `mattn/go-pointer` v0.0.1 | Alle MIT, offizielle bzw. von der Tree-sitter-Organisation gepflegte Grammatiken mit Go-Bindings (cgo, kein Download zur Laufzeit). `php_only` wird nicht verwendet: `php` deckt Dateien mit und ohne HTML ab. |
| D21 | Highlight-Queries | Upstream-`highlights.scm` unverändert per `go:embed` eingebettet (`internal/syntax/queries/`, Lizenztexte in `queries/NOTICE`), plus eigene Ergänzung `php_extra.scm` (Operatoren, Interpunktion, Deklarationsnamen, Attribute). Capture-Namen werden über das längste Punkt-Präfix auf eine feste Rollenliste abgebildet. | Keine Pflege eigener Komplett-Queries; Rollen entkoppeln Syntax von Farben. Präzedenz bei gleichem Knoten folgt der Konvention der jeweiligen Query (PHP/YAML/XML: letztes Pattern gewinnt, JSON: erstes). |
| D22 | `.env`-Highlighting | Eigener kleiner Tokenizer statt Tree-sitter-Grammatik | Format ist trivial; keine zusätzliche Grammatik-Abhängigkeit nötig. |
| D23 | YAML-Bibliothek | `go.yaml.in/yaml/v3` v3.0.6 (MIT/Apache-2.0) für die eingebettete Feature-Tabelle (`internal/phpver/features.yaml`) | API-identischer, aktiv gepflegter Nachfolger von `gopkg.in/yaml.v3` (dessen Repository ist archiviert), betreut von der YAML-Organisation. Tabelle in YAML wie im Konzept: neue PHP-Version = neuer Eintrag, kein Code. |
| D24 | PHP-Feature-Prüfung | Alle Feature-/Deprecation-Queries werden zu **einer** Tree-sitter-Query kombiniert (ein Baumdurchlauf pro Prüfung); jeder Eintrag trägt Positiv-/Negativ-Beispiel, Tests prüfen beide und – falls `php` installiert – zusätzlich `php -l`. Funktionsverfügbarkeit (z.B. `str_contains`) und wirkungslose Attribute (`#[\Override]`) nur als Warnung. | Schnell, neue Einträge sind automatisch getestet; Polyfills machen Funktionsprüfungen unsicher. |
