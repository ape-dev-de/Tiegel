# Einstellungs-Abgleich: Referenz-IDE ↔ Tiegel

Stand: 2026-11-17 (main @ `6ae711b`). Ergänzt [vergleich.md](vergleich.md) (Funktionen) um die Sicht **„was lässt sich einstellen“**. Daraus abgeleitet: Meilensteine M11–M17 in [ROADMAP.md](ROADMAP.md).

## Methode

1. **Quelle:** eine reale, über Jahre gepflegte Installation der Referenz-IDE (Anwendungs-Konfiguration `options/*.xml`, `keymaps/*.xml`) und mehrere echte PHP-Projekte (`.idea/php.xml`, `phpunit.xml`, `php-test-framework.xml`, `runConfigurations/*.xml`, `workspace.xml`). Es wurden **nur Strukturen** ausgewertet; keine Pfade, Projektnamen oder Inhalte stehen in diesem Dokument.
2. **Raster:** der Einstellungsbaum der Referenz-IDE (Abschnitte unten). Je Zeile: was ist dort einstellbar, was kann Tiegel (am Code geprüft: `internal/core/settings`, `internal/app/gui/keys.go`, `internal/editor/keymap/defaults.go`, `internal/interp`, `internal/lsp/servers`, `tiegel --headless methods`), **Ebene** (Kern oder Plugin), **Import** (lässt sich der Wert übernehmen?) und Meilenstein.
3. **Gewichtung:** Was in der realen Konfiguration tatsächlich *benutzt* wird, steht oben: Compose-Interpreter (`docker-compose://`, Dienst `app`, Remote-Pfad, Modus „run“), PHPUnit-Run-Konfigurationen pro Testsuite (Dutzende pro Projekt), Qualitäts-Tools aus `vendor/bin` (PHPStan, Pint, PHPCS, Psalm, PHPMD) über den Projekt-Interpreter, PHP-Sprachstufe pro Projekt, Docker-Kontext OrbStack, Terminal mit Option-als-Meta, nahezu unveränderte macOS-Standard-Keymap.

**Konsequenz für die Keymap:** Nutzer der Referenz-IDE ändern die Keymap kaum – sie erwarten die *Standard*-Belegung. Parität mit der macOS-Standard-Keymap ist deshalb wichtiger als ein Keymap-Editor; der Import muss vor allem die (wenigen) Abweichungen übernehmen.

## Kern ↔ Plugin (Nutzerwunsch 2026-11-17)

Einstellungen werden in **Kern** und **Plugins** getrennt (D190):

- **Kern** (immer da, Abschnitt „Kern“ im Dialog): Erscheinungsbild, Schrift, Editor-Verhalten, Keymap, Dateien/Watcher, Autosave, Local History, Projekte, Fenster, Update-Prüfung, Interpreter-Liste (weil Run, Test, Debug, LSP und alle Tools sie teilen).
- **Plugins** (Abschnitt „Plugins › <Name>“, nur sichtbar wenn das Modul an ist): alles, was ein einzelnes Werkzeug betrifft – z.B. **Laravel Pint**, PHPStan, Psalm, PHPCS, PHP-CS-Fixer, PHPMD, PHPUnit/Pest, Docker, Xdebug, Git, je Sprachserver ein Plugin.
- Jedes Modul **deklariert** seine Einstellungen (Schlüssel unter eigenem Namensraum `<modul>.*`, Typ, Standard, Bereich global/Projekt, Beschriftung); der Dialog, `settings.*` und `tiegel --headless settings` erzeugen sich daraus. Der Kern kennt kein Werkzeug beim Namen.
- Der **Import** (M16) folgt derselben Trennung: der Kern übernimmt Kern-Werte, jedes Plugin registriert einen eigenen Import-Hook für „seine“ Komponenten (Pint liest `LaravelPint`, PHPStan liest `PhpStan`, …). Unbekanntes landet im Import-Bericht als „nicht unterstützt“.

## Legende

✅ vorhanden · 🟡 teilweise · ❌ fehlt · ⛔ bewusst nicht. **Import:** ✔ übernehmbar · ~ teilweise · – nein.

## 1. Erscheinungsbild & Verhalten (Kern)

| Einstellung | Tiegel | Ebene | Import | MS |
| --- | --- | --- | --- | --- |
| Theme hell/dunkel/System | ✅ `ui.theme` | Kern | ✔ (`laf.xml`, `colors.scheme.xml` → hell/dunkel) | M16 |
| UI-Schriftgröße | ✅ `ui.fontSize` | Kern | ✔ | M16 |
| Native Datei-Dialoge | ✅ (`native`) | Kern | – | – |
| Bestätigung beim Schließen laufender Prozesse | ✅ `run.confirmClose` (ask/terminate/keep, D267) | Kern | ✔ (`processCloseConfirmation`) | M15 |
| Projekt in neuem/diesem Fenster öffnen (Nachfrage) | ✅ `projects.openIn` (ask/new/this) | Kern | ✔ (`confirmOpenNewProject2`) | M12 |
| Zuletzt geöffnete Projekte | ✅ `ui.recentProjects` | Kern | ✔ (`recentProjects.xml`, nur existierende Pfade, opt-in) | M16 |
| Tipps beim Start, Statusleisten-Widgets | ⛔ / 🟡 | Kern | – | – |
| Einstellungsdialog mit Baum + Suche | ✅ Baum „Core“/„Plugins“, Suche, Quelle + Zurücksetzen (D216) | Kern | – | M12 |

## 2. Keymap (Kern)

Tiegel-Stand (M13 abgeschlossen): ein Katalog aller belegbaren Aktionen (`internal/editor/keymap`, D220) mit Presets macOS/PC, mehreren Chords je Aktion, Kontexten global/Editor/Tool-Fenster, Maustasten und Gesten (D224), eigenen Belegungen in `keymap.overrides` (D221, eigene schlagen Standard D247, kaputte Zeilen bleiben erhalten D248) und Konflikterkennung (D223). Parität mit der macOS-Standardbelegung ist als Fixture getestet (D225). Bearbeitung im **Keymap-Editor** des Einstellungsdialogs (AP13.3, D280–D284) und per `tiegel --headless keymap …`.

| Aktion (macOS-Standard der Referenz) | Tiegel | MS |
| --- | --- | --- |
| Back / Forward: Cmd+[ / Cmd+] **und Cmd+Alt+← / Cmd+Alt+→**, Maustasten 4/5, Trackpad-Wischen | ✅ (Zwei-Finger-Wischen offen, D224) | M13 |
| Last Edit Location (Cmd+Shift+⌫) | ✅ | M13 |
| Recent Locations (Cmd+Shift+E) | ✅ | M13 |
| Wortweise Bewegung/Auswahl/Löschen mit Option, Zeilenanfang/-ende mit Cmd | ✅ | – |
| Go to Class/File/Symbol (Cmd+O / Cmd+Shift+O / Cmd+Alt+O), Search Everywhere (⇧⇧), Find Action (Cmd+Shift+A) | ✅ | – |
| Find/Replace in Path (Cmd+Shift+F/R), Find Usages (Alt+F7), Alt+Enter, Reformat (Cmd+Alt+L), Rename (Shift+F6) | ✅ | – |
| File Structure-Popup (Cmd+F12) | ✅ | M13 |
| Parameter Info (Cmd+P), Quick Documentation (F1/Ctrl+J) | ✅ | M13 |
| Next/Previous Highlighted Error (F2/Shift+F2) | ✅ | M13 |
| Go to Test / Create Test (Cmd+Shift+T) | ❌ (Fixture: später) | M15 |
| Run/Debug (Ctrl+R/Ctrl+D), Run Context Configuration (Ctrl+Shift+R), Rerun (Cmd+R im Run-Fenster) | 🟡 Run, Run Current File, Rerun, Run-Fenster Cmd+4 (D269); Debug fehlt | M15 |
| Generate (Cmd+N), Surround With (Cmd+Alt+T), Live Templates (Cmd+J) | ✅ Surround With (Cmd+Alt+T/J), Insert Live Template (Cmd+J) (D424); Generate siehe D430 | M17 |
| Join Lines (Ctrl+Shift+J), Toggle Case (Cmd+Shift+U), Paste from History (Cmd+Shift+V) | ✅ | M13 |
| Complete Statement (Cmd+Shift+Enter) | ❌ (Fixture: später) | M17 |
| Extract Variable/Method (Cmd+Alt+V/M) | ✅ Code-Actions `refactor.extract` des Language Servers (D425) | M17 |
| Tool-Fenster Cmd+0…9 | 🟡 0/1/3/5/6/7/9 (D228); 2/4/8 folgen mit ihren Fenstern | M14/M15/M17 |
| Terminal (Alt+F12) | ❌ | M15 |
| Debug: F8/F7/Shift+F8/F9, Cmd+F8 | ✅ | – |
| Nutzer-Bindungen, mehrere Chords je Aktion, Konflikterkennung | ✅ (D220–D223, D247, D248) | M13 |
| Keymap-Editor: Suche nach Aktion und nach Tastenkombination, Aufnahme-Feld (inkl. ⇧⇧, Maustasten, Gesten), Konfliktwarnung mit „Keep both“ / „Remove from other“, „No shortcut“, Zurücksetzen je Aktion und gesamt, ungültige Zeilen entfernen | ✅ (D280–D284) | M13 |
| Keymap-Presets (macOS / PC / „Referenz-IDE macOS“) | ✅ macOS/PC; das macOS-Preset entspricht der Referenz-Standardbelegung (D225) | M13 |
| Tool-Fenster-Fokuskontext (Cmd+D = Show Diff nur im Tool-Fenster) | 🟡 GUI bindet global und leitet weiter (D229) | – |
| Terminal: Option als Meta | ❌ (kein Terminal) | M15 |

## 3. Editor (Kern)

| Einstellung | Tiegel | Ebene | Import | MS |
| --- | --- | --- | --- | --- |
| Schrift: Familie, Größe, Zeilenhöhe | ✅ `editor.fontFamily/fontSize/lineHeight` | Kern | ✔ (`editor-font.xml`) | M16 |
| Konsolen-/Terminal-Schrift | ❌ | Kern | ✔ (`console-font.xml`, `terminal-font.xml`) | M15 |
| Tab-Breite | ✅ `editor.tabWidth` | Kern | ✔ (`codestyles`) | M16 |
| Soft Wrap (aus / Dateimuster / alle, Umschalter pro Editor) | ✅ `editor.softWrap`, `editor.softWrapFiles` (D285, D288, D289) | Kern | ✔ (`editor.xml` Soft-Wrap-Muster) | M12 |
| Whitespace anzeigen (keins / Auswahl / am Zeilenende / alle) | ✅ `editor.showWhitespace` (D286) | Kern | ✔ (`editor.xml`) | M12 |
| Lineal / rechter Rand | ✅ `editor.rightMargin` (Projekt), `editor.showRightMargin`; `.editorconfig` `max_line_length` (D287) | Kern | ✔ (`codestyles` RIGHT_MARGIN) | M12 |
| Zeilennummern | ✅ immer an (Widget `HideLineNumbers` nur für Diff-Ansichten) | Kern | – | – |
| `.editorconfig` | ✅ `files.editorconfig` (D214) | Kern | – (Datei im Projekt) | M12 |
| Farbschema (eigene Farben) | 🟡 Palette „crucible“, nicht einstellbar | Kern | ~ hell/dunkel | – |
| Code Style PHP (umfangreich) | ⛔ (Projekt-Formatter, siehe Plugins Pint/CS-Fixer) | – | – | – |
| Inspektionen | ⛔ (Diagnosen kommen aus LSP + Plugins) | – | – | – |
| Datei- und Code-Vorlagen | 🟡 nur „New PHP Class“ | Kern | ~ (`fileTemplates/`) | M17 |
| Live Templates | ✅ Plugin `live-templates` (D420–D424): Tab, Cmd+J, Surround With, Tab-Stopps mit verknüpftem Bearbeiten; eigene Vorlagen als Liste `live-templates.user` | Plugin `live-templates` | ~ | M17 |
| Dateitypen/Zuordnungen | ✅ `files.associations` (D213) | Kern | ✔ (`filetypes.xml`) | M12 |
| Inlay Hints | ❌ | Plugin `lang-php` | – | M17 |

## 4. Versionskontrolle (Plugin `git`)

| Einstellung | Tiegel | Import | MS |
| --- | --- | --- | --- |
| Git-Pfad, Update-Methode (Merge/Rebase), Auto-Fetch | 🟡 `git.updateMethod` ✅; Git-Pfad, Auto-Fetch fehlen | ✔ (`vcs.xml`) | M12 |
| Log-Spalten, Änderungen von Eltern zeigen | ❌ | ✔ | – (mit AP10.20) |
| GitHub/GitLab-Konten | ⛔ (Privacy; nur Git-CLI) | – | – |

## 5. Build, Ausführung, Deployment

| Einstellung | Tiegel | Ebene | Import | MS |
| --- | --- | --- | --- | --- |
| Docker: Verbindung (Kontext/Socket: OrbStack, Docker Desktop, Colima), `docker`/`docker compose`-Pfad | 🟡 `compose` erkennt Compose-Dateien; kein Kontext, kein Socket, Pfad fest | Plugin `docker` | ✔ (`docker-tools.xml`) | M14 |
| Services-Fenster (Container, Logs, Start/Stop, Exec) | ❌ | Plugin `docker` | – | M14 |
| Run-Konfigurationen | 🟡 `run.*` mit Kern-Typen PHP-Skript, Befehl, Composer-Skript; Typen-Hook `run.configTypes` für Plugins (D265); PHPUnit fehlt | Kern (Run-Infrastruktur) + Plugins (Typen) | ✔ (`runConfigurations/*.xml`, `workspace.xml`) | M15 |
| Deployment (SFTP/FTP) | ⛔ (Konzept) | – | – | – |
| Debugger (Xdebug-Port, Break at first line, Eval-Modus) | 🟡 Port/Listener | Plugin `debug-xdebug` | ✔ (`debugger.xml`, `php.xml`) | M16 |
| Coverage | ❌ | Plugin `phpunit` | – | – (später) |

## 6. Sprachen & Frameworks › PHP

| Einstellung | Tiegel | Ebene | Import | MS |
| --- | --- | --- | --- | --- |
| **CLI-Interpreter** lokal | ✅ `interp` (local) | Kern | ✔ | M16 |
| **Interpreter in Compose** (Dienst, Compose-Dateien, Remote-Projektpfad, Modus *exec* oder *run*) | ✅ `compose` exec/run/auto; Remote-Pfad über Mappings; Docker-Kontext/Socket nicht wählbar | Kern + Plugin `docker` | ✔ (`php.xml` `PhpInterpreters`) | M14 |
| Interpreter aus Docker-Image (ohne Compose) | ❌ | Plugin `docker` | ✔ | M14 |
| Interpreter-Info (Version, Extensions, Xdebug, ini) zwischengespeichert | 🟡 Version | Kern | – (neu ermitteln) | M14 |
| PHP-Sprachstufe pro Projekt | ✅ Versions-Override | Kern | ✔ (`php_language_level`) | M16 |
| Include-Pfade | ❌ | Plugin `lang-php` | ✔ | M17 |
| Composer (Pfad, `composer.json` synchronisieren) | 🟡 PSR-4 gelesen | Kern | ~ | – |
| Server + Pfad-Mappings (Debug) | ✅ Mappings | Kern | ✔ (`php.xml`/`deployment.xml`/`remote-mappings.xml`) | M16 |
| **Test-Frameworks** (PHPUnit über Composer-Autoload oder `phpunit.phar`, `phpunit.xml`, Bootstrap, Paratest; Pest) | ❌ | Plugin `phpunit` (Pest als eigenes Plugin) | ✔ (`php.xml` `PhpUnit`, `phpunit.xml`) | M15 |
| **Qualitäts-Tools**: Laravel Pint, PHP-CS-Fixer, PHPCS, PHPStan, Psalm, PHPMD (Pfad, Konfigdatei, Interpreter, Timeout, Schweregrad) | ✅ je Plugin `<id>.path/config/args/useInterpreter/timeout/severity/…`, Format bei Speichern, Prüfen bei Speichern/auf Wunsch (AP17.1, D350–D359) | **je ein Plugin** (`qa-pint`, `qa-phpstan`, …) | ✔ (`php.xml` je Komponente) | M17 |
| Sprachserver (Wahl, Pfad, Optionen, aus) | 🟡 Erkennung in `vendor/bin`, Composer-global, PATH; **ohne Treffer nur „No language server“**, nicht abschaltbar | je Server ein Plugin (`lsp-phpactor`, …) + Kern-Supervisor, nativ | – | M11 |
| Frameworks (Laravel, Symfony, WordPress, Drupal) | ⛔ jetzt / Plugins später | Plugin | – | – |

## 7. Werkzeuge

| Einstellung | Tiegel | Ebene | Import | MS |
| --- | --- | --- | --- | --- |
| Terminal: Shell, Option als Meta, Escape → Editor | ❌ | Kern (Terminal) | ✔ (`terminal.xml`, `advancedSettings.xml`) | M15 |
| External Tools | ❌ | Kern | ✔ (`tools/External Tools.xml`) | M17 |
| HTTP-Client | ❌ | Plugin | – | – |
| KI-Assistenten, Telemetrie, Lizenz | ⛔ (Privacy) | – | – | – |

## 8. Projektübergreifend aus `.idea` (Import-Quelle, M16)

| Datei | Inhalt | Ziel in Tiegel |
| --- | --- | --- |
| `php.xml` › `PhpInterpreters` | Interpreter (lokal / `docker-compose://` mit Dienst, Compose-Dateien, Remote-Pfad, run/exec) | Kern-Interpreter + Mapping (Plugin `docker`) |
| `php.xml` › `PhpProjectSharedConfiguration` | Sprachstufe | Versions-Override |
| `php.xml` › `PhpUnit` | `phpunit.xml`, Autoload, Paratest-Pfad | Plugin `phpunit` |
| `php.xml` › `LaravelPint`, `PhpStan`, `Psalm`, `PhpCodeSniffer`, `MessDetector`, `PHPCSFixer…` | Tool-Pfade, Konfig, Interpreter | je Plugin `qa-*` |
| `runConfigurations/*.xml`, `workspace.xml` › `RunManager` | PHPUnit (Suite/Datei/Klasse/Methode, Interpreter), PHP-Skript, Compound | Run-Konfigurationen (M15); Compound → Liste |
| `deployment.xml`, `remote-mappings.xml` | Pfad-Mappings | Kern-Mappings |
| `vcs.xml` | Git-Wurzeln | Plugin `git` |
| `$PROJECT_DIR$`, `$USER_HOME$` | Makros | auflösen; nicht existierende Pfade → Bericht |

## Nicht übernommen (bewusst)

Lizenz-/Konto-Dateien, KI-Plugins und deren Einstellungen, Statistik/Telemetrie, Fremd-Plugins ohne Gegenstück, Code-Style-Details (Projekt-Formatter ist die Quelle), Inspektionsprofile, Datenbank-Tools.
