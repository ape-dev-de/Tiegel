# Funktionsvergleich PhpStorm ↔ Tiegel

Stand: 2026-11-16 (main @ `603075a`). Grundlage: [konzept.md](konzept.md), [ROADMAP.md](ROADMAP.md) (M10), [STATUS.md](STATUS.md), [headless.md](headless.md) – **geprüft am Code**, nicht an der Doku (`internal/app/gui` inkl. `keys.go`/`registry.go`, `internal/ui/organisms/*`, `internal/api`, `internal/modules/*`, `internal/editor/*`, `internal/lsp`, `internal/gitcli`, `internal/dbgp`, Ausgabe von `tiegel --headless methods`).

PhpStorm dient hier nur zur Einordnung des Funktionsumfangs (Clean-Room: Name nicht in Code/UI).

## Legende

| Zeichen | Bedeutung |
| --- | --- |
| ✅ | vorhanden und in der GUI nutzbar |
| 🟡 | teilweise – was fehlt, steht dabei |
| ❌ | fehlt |
| ⛔ | bewusst nicht (Verweis aufs Konzept) |

**Priorität:** P0 = blockiert den Alltag · P1 = wichtig · P2 = später.
**Hinweis-Spalte:** `API:` nennt vorhandene Methoden aus `internal/api` (headless erreichbar), `API ❌` heißt: Backend fehlt ebenfalls (D41: zuerst API + Headless-Befehl). Aufwand **S** (≤ 2 Tage) / **M** (≤ 1–2 Wochen) / **L** (mehr).

## Kurzbefund

Das MVP-Gerüst steht (Editor-Eigenbau, LSP-Basis, PHP-Version/Features, Commit-Fenster, Merge, Xdebug). Für den Alltag fehlen aber vor allem **Shell-Funktionen**, die in der Roadmap nie als AP auftauchten:

1. **Keine Erkennung externer Dateiänderungen.** Es gibt keinen Dateiwatcher (`fsnotify` o.ä. fehlt in `go.mod`, keine `mtime`-Prüfung in `internal/api/documents.go`). Ein `git pull`, `composer install` oder Formatter im Terminal wird weder im Dateibaum noch in offenen Puffern noch im Git-Status sichtbar – und **`doc.save` überschreibt eine extern geänderte Datei ohne Rückfrage** (Datenverlust-Risiko). `reload` gibt es nur nach eigenen Git-/History-Aktionen.
2. **Keine projektweite Suche.** `search.inDoc`/`search.replaceInDoc` arbeiten nur in einer Datei; Find in Files / Replace in Path fehlen in API und GUI.
3. **Keine Aktionssuche / Search Everywhere.** Die `Registry` hält Aktionen mit Titel und Shortcut, aber es gibt keine Palette, die sie anbietet (`palette.Chooser` wird nur für Dateien, Recent, Zeile, PHP-Version und Feature-Chooser genutzt). Ohne Menü (siehe 5) sind Aktionen ohne Shortcut damit **unerreichbar**.
4. **LSP nur halb angebunden.** Der Client spricht `completion`, `definition`, `implementation`, `hover`, `signatureHelp`, Diagnosen – aber **nicht** `references`, `formatting`, `codeAction`, `rename`, `documentSymbol`, `workspace/symbol`, `inlayHint`, `documentHighlight`. Damit fehlen Find Usages, Reformat, Alt+Enter (inkl. „Import class“).
5. **Kein Menü, keine Kontextmenüs.** Weder macOS-Menüleiste noch In-Window-Menü; Rechtsklick im Editor wird nur im Gutter ausgewertet (`editorview.secondaryPress`), Dateibaum/Tabs reagieren nicht auf Rechtsklick. Datei-Operationen (neu/umbenennen/löschen) existieren **nicht einmal in der API** (`files.*` kennt nur `list`, `read`, `detectLanguage`).
6. **Projektwechsel nur über Neustart.** `OpenProject` wird ausschließlich vom Willkommensbildschirm aufgerufen; `project.close` ist in der API, aber in der GUI nicht erreichbar. Ein Fenster, kein Ordner-Dialog (Pfad als Textfeld).
7. **Kein Terminal, keine Run-Konfigurationen.** Es gibt keinen Weg, ein Skript, `composer` oder PHPUnit aus Tiegel zu starten (außer Xdebug-Listener, der auf externe Requests wartet).
8. **Schrift:** fest Go Mono/Go Sans (`tokens`, `gofont`), keine Größe/Familie/Zoom einstellbar, kein Emoji/CJK-Fallback (bekannt, AP10.3).
9. **Nichts wird zwischen Sitzungen wiederhergestellt** außer Recent Projects/Files und Breakpoints/Watches: offene Tabs, Caret-Positionen, Tool-Fenster-Größen, Sichtbarkeit.

---

## 1. Projekt & Fenster

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Projekt öffnen | Ordner-Dialog, Recent, Drag&Drop, CLI | 🟡 nur Willkommensbildschirm: Pfad-Textfeld + Recent-Liste, CLI-Argument; kein nativer Ordner-Dialog | P0 | API: `project.open`; AP10.2; S |
| Projekt wechseln/schließen | File › Open Recent, Close Project, „This/New Window“ | ❌ nach dem Öffnen kein Weg zurück (kein Action, kein Menü); `project.close` nur API | P0 | API: `project.close`; AP10.2; S |
| Mehrere Projektfenster | ja, ein Fenster pro Projekt | ❌ genau ein `app.Window`, ein `api.App`-Projekt | P1 | API ❌ (App kennt ein Projekt); AP10.1/10.2; L |
| Tool-Fenster-System | links/rechts/unten, Seitenleisten-Knöpfe, Pin/Float/Undock, Größe | 🟡 `Registry.ToolWindow` mit Seite Bottom/Right; unten Tab-Leiste (Problems, Commit, Debug, Git, Merge), rechts nur `rw[0]` und `RightVisible` wird nie gesetzt; kein Links-Andocken außer Projektbaum, keine Seitenleisten-Knöpfe, kein Float | P0 | AP10.1; M |
| Tool-Fenster-Shortcuts | Cmd+1…9, Shift+Esc, Cmd+Shift+F12 | 🟡 Cmd+1 Project, Cmd+5 Debug, Cmd+6 Problems, Cmd+9 Git, Cmd+K Commit; kein Shift+Esc, kein „alle ausblenden“ | P1 | AP10.1; S |
| Editor-Splits | vertikal/horizontal, Tabs verschieben | ❌ ein Editor | P1 | AP10.1; M |
| Editor maximieren / Distraction-free / Zen | ja | ❌ | P2 | S |
| Layout speichern/wiederherstellen | pro Projekt, Default-Layout | ❌ Splits/Tool-Fenster/Sidebar werden nicht gespeichert | P1 | API: `settings.set` (ui.*) reicht; S |
| Sitzung wiederherstellen (offene Tabs, Caret) | ja | ❌ nur `ui.recentFiles`/`ui.recentProjects` | P1 | API: `settings.*`; S |
| Tabs: Kontextmenü, Close Others/All, Pin, Reihenfolge ziehen | ja | 🟡 Schließen per X/Cmd+W, Ctrl+Tab zyklisch; kein Kontextmenü, Pin, Drag | P1 | AP10.4; S |
| Fenstertitel | Projekt – Datei | ✅ `windowTitle` | – | – |

## 2. Navigation

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Search Everywhere (Shift Shift) | Dateien, Klassen, Symbole, Aktionen | ❌ | P0 | API ❌ (Symbolindex AP10.5); M |
| Find Action (Cmd+Shift+A) | alle Aktionen inkl. Settings | ❌ – Registry-Aktionen sind ohne Shortcut unerreichbar | P0 | reine GUI über `Registry.Actions()`; S |
| Go to File (Cmd+Shift+O) | fuzzy | ✅ `file.quickOpen`, Fuzzy-Filter, Index im Hintergrund | – | API: `files.list` |
| Go to Class (Cmd+O) | ja | ❌ | P0 | AP10.5 (Tree-sitter-Index); M |
| Go to Symbol (Cmd+Alt+O) | ja | ❌ (`workspace/symbol` nicht angebunden) | P1 | AP10.5 oder LSP; S nach Index |
| Recent Files (Cmd+E) | ja | ✅ | – | `ui.recentFiles` |
| Recent Locations (Cmd+Shift+E), Last Edit Location | ja | ❌ | P2 | S |
| Back/Forward | Cmd+[ / Cmd+] | ✅ Navigationsverlauf im Controller | – | – |
| Go to Line (Cmd+L) | ja | ✅ inkl. `line:col`, Klick auf Statusleiste | – | – |
| Breadcrumbs | ja | ❌ | P2 | API: `syntax.outline`; S |
| Structure-Fenster (Cmd+7) / File Structure (Cmd+F12) | ja | ❌ in der GUI | P1 | API: `syntax.outline` ✅ (Tree-sitter, Headless `outline`); S |
| Go to Declaration (Cmd+B, Cmd+Klick) | ja | 🟡 nur über LSP (`lsp.definition`); ohne Phpactor nichts | P0 | AP10.5 (Fallback ohne LSP); M |
| Go to Implementation (Cmd+Alt+B) | ja | 🟡 nur LSP (`lsp.implementation`), Ergebnisliste als Chooser | P0 | AP10.5; M |
| Go to Super Method (Cmd+U) | ja | ❌ | P1 | AP10.5; S nach Index |
| Go to Type Declaration | ja | ❌ (`typeDefinition` nicht angebunden) | P2 | S |
| Find Usages (Alt+F7) / Show Usages (Cmd+Alt+F7) | Tool-Fenster mit Gruppierung | ❌ (`textDocument/references` fehlt) | P0 | API ❌; M (mit Ergebnis-Fenster aus Find in Files) |
| Highlight Usages im Editor | ja | ❌ (`documentHighlight` fehlt) | P1 | S |
| Call Hierarchy | ja | ❌ | P2 | LSP `callHierarchy`; M |
| Type Hierarchy | ja | ❌ | P2 | AP10.5; M |
| Gutter-Icons implementiert/überschreibt | ja, klickbar | ❌ – `editorview.MarkerIcon` existiert, aber kein Provider dafür | P1 | AP10.5; S nach Index |
| Next/Previous Error (F2) | ja | ❌ | P1 | Diagnosen liegen vor; S |
| Projektbaum: Autoscroll from Source | ja | ✅ aktive Datei wird im Baum selektiert | – | – |

## 3. Editor

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Schrift: Familie, Größe, Zeilenhöhe, Ligaturen | ja (JetBrains Mono, Ligaturen) | ❌ fest Go Mono, keine Einstellung, kein Zoom (Cmd+Plus/Minus belegt Folding) | P0 | AP10.3; M |
| Emoji/CJK-Fallback | ja | ❌ (STATUS „offen“) | P1 | AP10.3 |
| Syntax-Highlighting | PSI | ✅ Tree-sitter inkrementell (PHP, JSON, YAML, XML, .env) | – | API: `syntax.highlights` |
| Semantic Highlighting | ja | 🟡 API `lsp.semanticTokens` vorhanden, GUI nutzt sie nicht | P2 | S |
| Multi-Caret | Alt+Klick, Ctrl+G, Cmd+Ctrl+G, Alt+Shift+Klick | ✅ Ctrl+G, Ctrl+Cmd+G, Cmd+Alt+↑/↓ | – | `cursor`-Paket |
| Column Selection | Alt+Drag, Cmd+Shift+8 | 🟡 Block-Drag (`dragBlock`) vorhanden; kein Column-Mode-Toggle | P2 | S |
| Extend/Shrink Selection | syntaxbasiert | 🟡 Alt+↑/↓ vorhanden, aber Wort/Klammer/Zeile statt Syntaxbaum | P2 | `syntax` vorhanden; S |
| Klammer-Paare, Auto-Close | ja | ✅ `cursor.AutoClose` | – | – |
| Folding | ja | ✅ Tree-sitter-/Klammer-Folds, Gutter-Pfeile, Cmd+-/= | – | API: `syntax.folds` |
| Kommentar umschalten (Cmd+/) | Zeile + Block (Cmd+Alt+/) | 🟡 nur Zeilenkommentar | P2 | S |
| Zeilen verschieben/duplizieren/löschen | ja | ✅ | – | – |
| Suchen/Ersetzen in Datei | Regex, Case, Word, Preview | ✅ Findbar mit Regex/Case/Word, Replace/Replace All | – | API: `search.inDoc`, `search.replaceInDoc` |
| Soft Wrap | ja | ❌ | P1 | M (Editor-Geometrie) |
| Code-Vervollständigung | eigene Engine | 🟡 nur via LSP (Phpactor) – ohne Server keine Completion (auch keine Wort-Completion) | P1 | API: `lsp.completion`; Fallback S |
| Parameter-Hints (Cmd+P) | ja | 🟡 Signaturhilfe automatisch nach `(`/`,`; kein Shortcut zum erneuten Aufruf | P1 | API: `lsp.signature`; S |
| Quick Documentation (F1/Ctrl+J) | ja | 🟡 Hover nur per Maus (600 ms), kein Shortcut | P1 | API: `lsp.hover`; S |
| Inlay Hints (Parameternamen, Typen) | ja | ❌ | P2 | LSP `inlayHint`; M |
| Formatter / Reformat Code (Cmd+Alt+L) | ja, PSR-12 | ❌ (`formatting` nicht angebunden, kein PHP_CS_Fixer) | P0 | API ❌; S (LSP) / M (CS-Fixer über Interpreter) |
| Optimize Imports (Ctrl+Alt+O) | ja | ❌ | P1 | LSP codeAction; S nach Code Actions |
| Quick Fixes / Intentions (Alt+Enter) | ja, inkl. „Import class“ | ❌ (`codeAction` fehlt) | P0 | API ❌; M |
| Inspections | ~hunderte | 🟡 LSP-Diagnosen + eigene PHP-Versions-Features/Deprecations (`php.check`); Problems-Fenster nur offene Dateien | P1 | API: `lsp.diagnostics`, `php.check`; ⛔ eigene Analyse (Konzept: „Eigene PHP-Analyse – kommt vom Language Server“) |
| Spellcheck | ja | ❌ | P2 | M (Wörterbuch-Lizenz) |
| Live Templates | ja | ❌ | P2 | M |
| Postfix Completion | ja | ❌ | P2 | M |
| Surround With (Cmd+Alt+T) | ja | ❌ | P2 | S |
| PHPDoc-Generierung (`/**` + Enter) | ja | ❌ | P1 | Tree-sitter reicht; S |
| Generate (Cmd+N: Konstruktor, Getter/Setter) | ja | ❌ | P2 | LSP codeAction (Phpactor kann einiges); M |
| Bookmarks (F3, Mnemonic) | ja | ❌ | P2 | S |
| Clipboard-Historie (Cmd+Shift+V) | ja | ❌ | P2 | S |
| Local History – UI | Show History, Diff, Revert, Labels | 🟡 Speicherung ✅ (jede Speicherung), GUI nur Recovery-Banner nach fehlgeschlagenem Speichern; keine Historie-Ansicht | P1 | API: `history.list/content/restore` ✅; S (mit Diff-Fenster AP10.6) |
| Scratch Files | ja | ❌ | P2 | S |
| Autosave (Fokus, vor Run/Debug/Commit, Leerlauf) | ja | ✅ | – | `autosave.*` |
| Undo/Redo | ja | ✅ mit Gruppierung | – | `doc.undo/redo` |
| EOL/Encoding anzeigen/ändern | ja | 🟡 Anzeige (EOL fest „UTF-8 · LF/CRLF“), nicht änderbar | P2 | S |
| Einrückung erkennen/umstellen | ja, `.editorconfig` | 🟡 Tab-Breite global; keine Erkennung, kein `.editorconfig` | P1 | S |

## 4. Refactoring (nur zur Einordnung)

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Rename (Shift+F6) | ja | ⛔ MVP-Nichtziel (Konzept „Nicht im MVP: Refactorings“) | P2 | LSP `rename` wäre S – günstigster Einstieg nach dem MVP |
| Move Class / Namespace | ja | ⛔ | – | – |
| Extract Method/Variable, Inline, Change Signature | ja | ⛔ | – | Phpactor bietet Teile als Code Action → kommt ggf. „gratis“ mit Alt+Enter |
| Safe Delete | ja | ⛔ | – | – |

## 5. PHP-spezifisch

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Sprachlevel | Einstellung + aus composer.json | ✅ Interpreter → `config.platform.php` → `require.php`, Override in Statusleiste, Weitergabe an LSP | – | API: `php.version`, `php.setVersionOverride` |
| Feature-/Deprecation-Prüfung | Inspections | ✅ 41 Features + 13 Deprecations (YAML), Diagnosen im Editor | – | API: `php.check`, `php.diagnostics` |
| Interpreter lokal | ja | ✅ PATH/Homebrew, `php -v`/`-m` | – | API: `interp.*` |
| Docker Compose als Interpreter | ja | ✅ compose-go, Service-Erkennung, Pfad-Mapping, `exec`/`run` | – | API: `compose.detect`, `runtime.*` |
| Docker (ohne Compose) | ja | ❌ | P2 | M |
| Remote-Interpreter (SSH, Vagrant) | ja | ❌ (WSL ✅ im Backend) | P2 | ⛔ vorerst (Konzept: lokal/Compose/WSL) |
| Composer-Integration | composer.json-Hilfe, Skripte ausführen, Install/Update | ❌ (nur Versionsauslesung) | P1 | API ❌; M (über Run-Konsole) |
| Namespaces/PSR-4 | „New PHP Class“ mit Namespace aus autoload, Namespace-Prüfung | ❌ | P1 | API ❌; S (Teil von Datei-Operationen) |
| PHPDoc | Generierung, Typ-Inferenz | ❌ (siehe Editor) | P1 | S |
| PHPStan / Psalm | integrierte Inspections | ❌ | P1 | über Interpreter als Diagnose-Quelle; M |
| PHP_CS_Fixer / PHPCS | Formatter + Inspections | ❌ | P1 | Hook `file.beforeSave` passt (Konzept); M |
| PHPUnit / Pest | Run-Konfig, Testbaum, Gutter-Run-Icons | ❌ | P1 | ⛔ Testbaum „Phase 2“ (Konzept); einfacher Run + Ausgabe vorher; M |
| Laravel / Symfony | Plugins, Routen, Container | ❌ | P2 | ⛔ Fremd-Plugins später |
| Blade/Twig | ja | ⛔ andere Sprachen nur Highlighting (JSON/YAML/XML/.env) | – | – |
| Language Server | eigene Engine | ✅ Phpactor/PHPantom/Intelephense (vom System), Statusleiste + Neustart | – | ungetestet mit echtem Phpactor (STATUS) |

## 6. Git/VCS

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Commit-Fenster | Dateiliste, Hunk-Auswahl, Diff-Vorschau, Amend, Verlauf, Commit and Push | ✅ inkl. Zeilenauswahl, Nachrichtenverlauf, Guard `commit.before` | – | API: `git.commitSelection` |
| Changelists | mehrere, Default | ❌ | P2 | M |
| Shelve | ja | ❌ | P2 | M |
| Stash/Unstash | ja | ❌ (gitcli kennt kein `stash`) | P1 | API ❌; S |
| Gutter-Änderungsmarker + Rollback | ja | ✅ inkl. Popup mit Diff | – | API: `git.gutter`, `git.rollback` |
| Diff-Fenster (eigenes Fenster, Cmd+D) | side-by-side, Syntax-Highlighting, Navigation, editierbar | 🟡 `gitui.DiffView` (unified/side-by-side, Wortdiff) nur eingebettet im Commit-Fenster und Log-Detail; **kein Syntax-Highlighting**, kein eigenes Fenster, nicht aus Editor/Baum aufrufbar | P0 | AP10.6; API: `git.diff`, `git.show` |
| Annotate/Blame | Gutter-Annotation | ❌ (gitcli ohne `blame`) | P1 | AP10.6; API ❌; S |
| Datei-Historie | Show History | 🟡 Backend `gitcli.Log{Path, Follow}` vorhanden, keine GUI | P1 | AP10.6; S |
| Log | Graph, Filter (Autor, Pfad, Text), Details | 🟡 Liste mit Paging, Detail mit Dateien+Diff; **kein Graph**, keine Filter/Suche | P1 | API: `git.log`; M (Graph) |
| Branches-Popup | lokal/remote, checkout, new, delete, merge, rebase, compare | 🟡 Chooser + Branches-Tab: checkout/new/delete; kein Merge/Rebase/Compare, Remote nur Liste | P1 | API: `git.branches/checkout/createBranch/deleteBranch` |
| Merge (3-Wege) | ja | ✅ Merge-Fenster ours/Ergebnis/theirs | – | API: `git.mergeData`, `git.resolve` |
| Merge/Rebase starten, Cherry-pick, Revert, Reset | ja | ❌ (nur Konflikte lösen, die extern entstanden) | P1 | API ❌; M |
| Fetch / Pull | ja | ❌ (nur `git.push`) | P0 | API ❌; S – Pull ist Alltag |
| Update Project (Cmd+T) | Fetch + Merge/Rebase + Konflikte | ❌ | P1 | baut auf Fetch/Pull + Dateiwatcher; S |
| Push | Dialog mit Commits | 🟡 Cmd+Shift+K pusht direkt, Fehlerarten erkannt; keine Vorschau | P2 | S |
| Git-Status nach externen Änderungen | automatisch | ❌ nur nach eigenen Aktionen/`file.saved` (kein Watcher) | P0 | AP Dateiwatcher |
| Dateistatus-Farben im Baum | ja | ✅ | – | – |

## 7. Debug

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Xdebug-Listener (Telefonhörer) | ja | ✅ Statusleiste + Debug-Fenster, Port 9003 | – | API: `debug.listen/stop` |
| Zero-Config-Validierung | Validate Debugger Configuration | ✅ `runtime.diagnose` (ini-Werte, Mappings, Hinweise), Banner bei fehlenden Mappings | – | – |
| Pfad-Mapping | Servers-Dialog | ✅ aus Compose, korrigierbar | – | `runtime.mappings.*` |
| Zeilen-Breakpoints, bedingt | ja | ✅ Gutter, Bedingung per Chooser, Mute | – | `debug.breakpoints.*` |
| Exception-/Method-/Log-Breakpoints | ja | ❌ | P1 | DBGp `exception`-Typ; S |
| Stepping, Call Stack, Variablen | ja | ✅ | – | – |
| Watches | ja | ✅ persistent pro Projekt | – | `debug.watches*` |
| Evaluate Expression (Alt+F8) | ja | 🟡 API `debug.eval` ✅, GUI nicht angebunden (nur über Watches) | P1 | S |
| Inline Values im Editor | ja | ❌ | P2 | M |
| Run to Cursor (Alt+F9) | ja | ❌ | P1 | temporärer Breakpoint; S |
| Set Value | ja | ❌ | P2 | DBGp `property_set`; S |
| Debug-Konfigurationen (Skript/PHPUnit mit Xdebug starten) | ja | ❌ nur „auf Request warten“ | P1 | baut auf Run-Konfigurationen; M |
| Mehrere Sessions | ja | ✅ Sessions-Liste | – | – |

## 8. Werkzeuge

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Terminal (Alt+F12) | integriert, mehrere Tabs | ❌ | P1 | API ❌; L (PTY + VT-Emulator in Gio); Zwischenlösung „Im externen Terminal öffnen“ S |
| Run-Konfigurationen / Ausführen-Konsole | PHP-Skript, Composer, PHPUnit, Built-in Server, npm | ❌ | P1 | API ❌; M (Prozess im Interpreter, Ausgabe-Fenster, Links auf Dateien) |
| Tasks / Tool-Integration (External Tools) | ja | ❌ | P2 | S nach Run-Konsole |
| HTTP-Client (.http-Dateien) | ja | ❌ | P2 | M |
| Datenbank-Tools | DataGrip-Teil | ⛔ Konzept: „Nicht im MVP: Datenbank-Tools“ | – | – |
| Find in Files / Replace in Path (Cmd+Shift+F/R) | ja, mit Vorschau | ❌ | P0 | API ❌; M |
| Problems-Fenster | ja | ✅ (offene Dateien) | – | – |
| Docker-Tool-Fenster | Container, Logs | 🟡 nur „Container starten & erkennen“ | P2 | M |

## 9. Kontextmenüs, Hauptmenü, Actions

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Editor-Kontextmenü | ja | ❌ (Rechtsklick nur im Gutter) | P0 | AP10.4; S (Organismus „Menu“ fehlt im Baukasten) |
| Dateibaum-Kontextmenü | New, Rename, Delete, Copy Path, Reveal in Finder, Git, Compare | ❌ | P0 | AP10.4 |
| Datei-Operationen (neu/umbenennen/verschieben/löschen/kopieren) | ja | ❌ – **auch keine API** (`files.*` nur lesen) | P0 | AP10.4; S (API zuerst) |
| Tab-/Gutter-/Git-Listen-Kontextmenüs | ja | ❌ | P1 | AP10.4 |
| Hauptmenü | macOS-Menüleiste, unter Linux/Windows im Fenster | ❌ – weder nativ noch im Fenster | P1 | Gio hat keine Menü-API; macOS nativ L (cgo/AppKit), In-Window-Menü aus `Registry` M |
| Aktionsmodell | ID, Titel, Shortcut, Kontext | 🟡 `Registry.Action` (ID, Titel, Mac/PC-Chord, Scope global/editor); kein Enabled-State, keine Gruppen/Menüpfade | P0 | Grundlage für Menü, Palette, Kontextmenüs; S |
| Toolbar | Navigation Bar, Run-Widget | ❌ | P2 | S |

## 10. Einstellungen

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| Einstellungsdialog | Baum mit Suche | 🟡 Overlay: Module an/aus, Tab-Breite, Theme, Autosave-Leerlauf, Update-Prüfung, Interpreter, Mappings, Hinweise; keine Suche | P1 | API: `settings.*`, `modules.*` |
| Keymap-Editor | ja, Presets | ❌ – Keymap ist Code-Tabelle (`keymap/defaults.go`, `keys.go`); keine Nutzer-Bindungen | P2 | S (Datenmodell vorhanden) |
| Themes / Farbschemata | viele, importierbar | 🟡 hell/dunkel/System (eigene Palette „crucible“) | P2 | – |
| Schrift (Editor/UI) | ja | ❌ | P0 | AP10.3 |
| Code Style (PHP) | umfangreich | ❌ | P2 | ⛔ eher via PHP_CS_Fixer-Konfig des Projekts |
| Pro-Projekt-Einstellungen | ja | ✅ intern (Settings-Store global/Projekt) | – | – |

## 11. Plattform

| Funktion | PhpStorm | Tiegel | Prio | Hinweis |
| --- | --- | --- | --- | --- |
| macOS-Menüleiste | ja | ❌ (siehe Abschnitt 9) | P1 | L |
| Cmd+Q / Fenster schließen speichert | ja | ✅ `saveOnClose` | – | – |
| Dunkelmodus folgt System | ja | ✅ (macOS via `defaults`, alle 5 s) | – | – |
| Touch Bar | ja | ⛔ irrelevant | – | – |
| Drag&Drop (Dateien ins Fenster, im Baum verschieben) | ja | ❌ | P2 | Gio-Unterstützung prüfen; M |
| Dateiwatcher / Sync bei externen Änderungen | ja (VFS, „Reload from Disk“, Konflikt-Dialog) | ❌ – kein Watcher, kein Abgleich bei Fensterfokus, Speichern überschreibt externe Änderungen | P0 | API ❌; M; neue Abhängigkeit (`fsnotify`) → DECISIONS |
| „Reveal in Finder“, „Open in Terminal“ | ja | ❌ | P1 | AP10.4; S |
| Native Dialoge (Ordner/Datei öffnen) | ja | ❌ | P1 | AP10.2; M (gio-x/explorer o. cgo) |
| Signiert/notarisiert | ja | ⚠️ Skripte fertig, Zertifikat fehlt | – | M8 |

---

## Lückenliste: Arbeitspakete (Vorschlag, ergänzt AP10.1–10.6)

Bereits vergeben und hier **nicht** wiederholt: AP10.1 Fenster-Management, AP10.2 Projekte, AP10.3 Schrift, AP10.4 Kontextmenüs + Datei-Operationen, AP10.5 Tree-sitter-Index, AP10.6 Git-Diff-Fenster/Historie/Annotate. Jedes neue AP folgt D41: zuerst `internal/api` + Headless-Befehl + Test, dann GUI.

### P0 – blockiert den Alltag

| AP | Titel | Inhalt | Abhängigkeiten | Aufwand |
| --- | --- | --- | --- | --- |
| **AP10.7** | Dateiwatcher & externe Änderungen | Watcher im Backend (rekursiv, .gitignore/vendor gedrosselt, Debounce) → Event `files.changed`; ungeänderte offene Puffer neu laden, geänderte → Konfliktabfrage (Reload / Behalten / Diff); **`doc.save` prüft Hash/mtime vor dem Überschreiben** (Fehler `CodeConflict` statt stillem Überschreiben); Dateibaum und `git.status` aktualisieren; Fallback: Abgleich bei Fensterfokus. Headless: `watch`. Neue Abhängigkeit in DECISIONS begründen. | – (AP10.4, AP10.12 und Git-Pull profitieren) | M |
| **AP10.8** | Find in Files / Replace in Path | `search.inProject` (Text/Regex/Case/Word, Dateimaske, Verzeichnis-Scope, .gitignore, gestreamte Treffer, Abbruch), `search.replaceInProject` mit Vorschau über Dokument-API (Undo pro Datei); Tool-Fenster „Find“ mit Ergebnisbaum + Vorschau; Cmd+Shift+F/R, aus Baum-Kontextmenü „Find in Directory“. Headless: `grep`. | Ergebnis-Fenster wird von AP10.10 (Usages) wiederverwendet | M |
| **AP10.9** | Aktionsmodell, Find Action, Search Everywhere | `Registry.Action` um Gruppe/Menüpfad, Enabled-Prädikat, Kontext erweitern (Basis für Menüs/Kontextmenüs); Palette „Find Action“ (Cmd+Shift+A) über alle Aktionen inkl. Einstellungen/Tool-Fenster; Search Everywhere (Shift Shift) mit Reitern Dateien/Klassen/Symbole/Aktionen; Go to Class (Cmd+O), Go to Symbol (Cmd+Alt+O). | Klassen/Symbole: AP10.5; Menü-Gruppen nutzt AP10.4/AP10.13 | M |
| **AP10.10** | LSP-Ausbau: Usages, Code Actions, Formatierung | Client + API: `lsp.references`, `lsp.codeAction` + `workspace/applyEdit`/`executeCommand`, `lsp.formatting`/`rangeFormatting`, `lsp.documentHighlight`, `lsp.workspaceSymbol`; GUI: Find Usages (Alt+F7, Ergebnis-Fenster), Alt+Enter-Popup (Quick Fix, „Import class“, Phpactor-Code-Actions), Reformat (Cmd+Alt+L), Optimize Imports, Highlight Usages. Headless: `lsp-references`, `lsp-format`, `lsp-actions`. Fallback für Usages ohne LSP: Textsuche aus AP10.8 + Index AP10.5. | AP10.8 (Ergebnis-Fenster), optional AP10.5 | M |
| **AP10.11** | Git: Fetch, Pull, Update Project, Stash | gitcli + API `git.fetch`, `git.pull` (merge/rebase, Konflikte → bestehendes Merge-Fenster), `git.stash` (push/list/apply/pop/drop); GUI: Update Project (Cmd+T) mit Optionen, Fetch-Knopf, Ahead/Behind-Anzeige aktualisieren, Stash-Liste im Git-Fenster; Netzwerk nur auf Nutzeraktion (Privacy-Regel). Headless: `git-pull`, `git-fetch`, `git-stash`. | AP10.7 (Puffer/Baum nach Pull aktualisieren) | S–M |

### P1 – wichtig

| AP | Titel | Inhalt | Abhängigkeiten | Aufwand |
| --- | --- | --- | --- | --- |
| **AP10.12** | Sitzung & Layout speichern | Pro Projekt: offene Tabs + Reihenfolge + aktiver Tab, Caret/Scroll je Datei, Folding, Tool-Fenster (Seite, Größe, offen), Sidebar-Breite, Splits; beim Öffnen wiederherstellen. Über `settings.*` (Projekt-Scope). | AP10.1 (Splits/Seiten), AP10.2 | S |
| **AP10.13** | Hauptmenü | In-Window-Menüleiste (Linux/Windows) und native macOS-Menüleiste (cgo/AppKit, Bridge auf `Registry`-Aktionen, Shortcuts als Key Equivalents), Menü „File/Edit/View/Navigate/Code/Git/Run/Window/Help“ aus Aktionsgruppen. Entscheidung (cgo-Bridge vs. Gio-intern) in DECISIONS. | AP10.9 (Aktionsgruppen) | L (macOS nativ) / M (In-Window) |
| **AP10.14** | Structure-Fenster & Breadcrumbs | Tool-Fenster „Structure“ (Cmd+7) und Popup „File Structure“ (Cmd+F12, filterbar) aus `syntax.outline`, Folgen des Carets; Breadcrumbs über dem Editor (P2-Anteil). | – (API vorhanden) | S |
| **AP10.15** | Run-Konsole & Run-Konfigurationen | API `run.start/stop/list` (Prozess im gewählten Interpreter inkl. Compose/WSL, Pfad-Mapping zurück für Links), Guard `run.before` (Autosave), Run-Fenster mit Ausgabe, klickbaren `datei:zeile`-Links, Rerun; Konfig-Typen: PHP-Skript, Composer-Skript (aus composer.json), PHPUnit/Pest (Datei/Methode, ohne Testbaum), beliebiger Befehl; „Debug“-Variante setzt Xdebug-Trigger. Headless: `run`. | – ; Debug-Variante nutzt M7 | M |
| **AP10.16** | Terminal | Eingebettetes Terminal-Tool-Fenster (PTY, VT100/xterm-Emulation, mehrere Tabs, Startverzeichnis Projekt/Container via `docker compose exec`); Zwischenschritt: Aktion „Open in Terminal“ (externes Terminal.app/Standardterminal). Abhängigkeit (PTY/VT-Parser) in DECISIONS. | AP10.15 teilt Ausgabe-Rendering | L (eingebettet) / S (extern) |
| **AP10.17** | Editor-Alltagsaktionen | Next/Previous Error (F2/Shift+F2), Quick Documentation per Shortcut (F1/Ctrl+J), Parameter Info (Cmd+P), Soft Wrap, Zoom (Schriftgröße per Shortcut), Block-Kommentar, `.editorconfig` + Einrückungserkennung, PHPDoc-Stub bei `/**`+Enter (Tree-sitter), Wort-Completion ohne LSP. | AP10.3 (Zoom) | M |
| **AP10.18** | Debug-Ergänzungen | Evaluate Expression-Dialog (API `debug.eval` vorhanden), Run to Cursor, Exception-Breakpoints (DBGp `exception`), Set Value; Inline Values (P2-Anteil). | – | S–M |
| **AP10.19** | Local-History-Ansicht | „Show History“ für Datei/Ordner aus Tab-/Baum-Kontextmenü: Revisionsliste, Diff gegen aktuell, Revert/Teil-Revert, Labels. | AP10.6 (Diff-Fenster), AP10.4 | S |
| **AP10.20** | Git-Log & Branch-Operationen | Log-Graph, Filter (Branch, Autor, Pfad, Text), Kontextmenü auf Commits: Checkout, Cherry-pick, Revert, Reset, Branch hier anlegen, Compare with Local; Branches-Popup: Merge into Current, Rebase onto, Compare. | AP10.6, AP10.4, AP10.11 | M |
| **AP10.21** | Qualitäts-Tools | PHPStan/Psalm als Diagnosequelle (Projekt-Binary im Interpreter, Ergebnis in Problems, nur bei Speichern/auf Wunsch), PHP_CS_Fixer/PHPCS als Formatter über Hook `file.beforeSave` bzw. Reformat-Aktion; Erkennung aus `vendor/bin`, keine Downloads. | AP10.15 (Prozessausführung im Interpreter), AP10.10 (Reformat-Aktion) | M |
| **AP10.22** | Neue PHP-Datei/Klasse mit PSR-4 | „New PHP Class/Interface/Trait/Enum“ im Baum-Kontextmenü: Namespace aus `composer.json` autoload (PSR-4) ableiten, Vorlage, `declare(strict_types=1)` optional; Namespace-Diagnose bei falschem Pfad (P2). | AP10.4 (Datei-Operationen-API) | S |

### Reihenfolge-Empfehlung

1. **AP10.7** (Datenverlust-Risiko beim Speichern) – zuerst, parallel zu AP10.2/AP10.3.
2. **AP10.9** Aktionsmodell vor AP10.4-Kontextmenüs und AP10.13-Menü (gleiche Datenbasis), dann Find Action.
3. **AP10.8 → AP10.10** (Ergebnis-Fenster teilen); AP10.5 parallel, danach Search Everywhere/Go to Class aus AP10.9.
4. **AP10.11** nach AP10.7.
5. P1 nach Nutzwert: AP10.12, AP10.14, AP10.17, AP10.15, AP10.18, AP10.19, AP10.22, AP10.20, AP10.21, AP10.13, AP10.16.
