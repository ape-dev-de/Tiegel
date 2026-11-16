# Tiegel – PHP-IDE

Nov 16, 2026 · @Alexis

## Vision & Ziele

Tiegel ist eine schnelle, schlanke PHP-IDE in Go, die PhpStorm-Nutzern ihre gewohnten Wege bietet – ohne JSON-Konfiguration. Mac-first, danach Linux und Windows (WSL).

- **Vertraut:** Bedienung, Shortcuts und Abläufe wie in PhpStorm (Commit-Dialog, Xdebug-Listener, Interpreter-Auswahl).
- **Schnell:** Start und Tippen deutlich flotter als PhpStorm.
- **Ohne Konfig-Dateien:** Docker Compose, PHP-Interpreter und Xdebug werden erkannt und per Oberfläche eingestellt.
- **Modular:** Kleiner Kern, jede Funktion ist ein Modul, das sich an- und ausschalten lässt.
- **Nur PHP:** Fokus auf PHP inkl. mehrerer Versionen und Feature-Erkennung.

## Projekt-Rahmen

Tiegel ist ein Selbstentwicklungs-Projekt, offen auf GitHub, ohne Marke und ohne Verkauf.

- **Name:** Tiegel – wie ein Schmelztiegel, in dem Kern und Plugins zusammenfließen. Keine Markenanmeldung geplant.
- **Hosting:** öffentliches Repository auf GitHub, Releases dort.
- **Lizenz:** MIT – frei nutzbar, änderbar und weitergebbar, nur der Copyright-Hinweis bleibt. Wer gar keine Bedingungen will: Unlicense (gemeinfrei).

## Leitprinzipien

Tiegel ist EU-souverän und Privacy first: Kein Byte verlässt den Rechner, ohne dass der Nutzer es auslöst.

- **Keine Datenabflüsse:** keine Telemetrie, keine Konten, keine Cloud-Dienste, keine Absturzberichte nach außen. Logs bleiben lokal.
- **Keine stillen Downloads:** Language Server und Tools werden mitgeliefert oder vom System genutzt, nie automatisch nachgeladen.
- **Updates nur auf Wunsch:** Prüfung abschaltbar, Bezug aus einer selbst hostbaren Quelle (z.B. GitHub-Releases).
- **Möglichst nativ:** Oberfläche mit Gio in reinem Go – keine WebView, kein JavaScript, keine npm-Lieferkette. Ein natives Binary pro Plattform.
- **Nur Open Source mit aktiver Pflege:** jede Abhängigkeit mit OSI-Lizenz; proprietäre Tools (z.B. Intelephense) nicht im Standard.
- **Standards statt Eigenbau:** LSP, DAP/DBGp, Tree-sitter, Git-CLI, Compose-Spezifikation.

| Baustein | Projekt | Lizenz |
| --- | --- | --- |
| Oberfläche | Gio (gioui.org) | MIT oder Unlicense |
| Editor | Eigenbau auf Gio-Basis | MIT (Projektlizenz) |
| Syntax | Tree-sitter + tree-sitter-php | MIT |
| PHP-Intelligenz | Phpactor / PHPantom | MIT |
| Docker Compose | compose-go | Apache 2.0 |
| Versionskontrolle | Git (CLI) | GPL 2.0 |
| Debugging | Xdebug | Xdebug License (BSD-artig) |

## MVP-Umfang und Nicht-Ziele

Das MVP deckt Code schreiben, Commits wie in PhpStorm und Debuggen ab – sonst nichts.

| Im MVP | Nicht im MVP |
| --- | --- |
| Editor mit PHP-Intelligenz über LSP | Refactorings |
| Mehrere PHP-Versionen + Feature-Prüfung | Datenbank-Tools |
| Interpreter lokal und Docker Compose | Andere Sprachen außer PHP (nur Highlighting für JSON, YAML, XML, .env) |
| Git: Commit-Dialog, Diff, Log, 3-Wege-Merge | Eigene PHP-Analyse (kommt vom Language Server) |
| Xdebug: Breakpoints, Variablen, Stepping | Fremd-Plugins (Schnittstelle ist vorbereitet) |
| Module an/aus in den Einstellungen | PHPUnit-Testbaum (Phase 2) |

## Architektur

Eine Gio-Oberfläche spricht mit einem Go-Backend; das Backend trägt den Kern und alle Module. Alles ist Go, ohne JavaScript.

&#91;embedded content: Architektur · UI, Kern, 5 Module, externe Programme\]

Lokal laufen UI und Backend im selben Prozess über eine interne API. Unter Windows läuft das Backend als eigener Prozess in der WSL und spricht über dieselbe API per Socket; das ermöglicht später auch Remote-Entwicklung. Die PHP-Intelligenz kommt vom Language Server, nicht aus eigener Analyse.

## Oberfläche & Bedienung

Die Oberfläche folgt Atomic Design: ein festes Baukasten-System, aus dem Kern und Plugins alles zusammensetzen.

| Ebene | Beispiele |
| --- | --- |
| Design Tokens | Farben (hell/dunkel), Schrift, Abstände, Radien |
| Atome | Button, Icon, Eingabefeld, Checkbox, Badge, Tooltip |
| Moleküle | Suchfeld, Dateizeile, Tab, Breakpoint-Zeile, Diff-Block |
| Organismen | Dateibaum, Commit-Dialog, Debug-Panel, Merge-Ansicht, Statusleiste |
| Templates | Fenster-Layouts: Editor, Commit, Debug, Merge |

- Plugins bauen ihre UI nur aus diesen Komponenten, kein eigenes Zeichnen. So sieht alles einheitlich aus.
- Komponenten-Katalog als kleine Gio-Demo-App (wie Storybook) als lebende Referenz.

**Editor-Komponente (Eigenbau)**

Gio bringt nur ein einfaches Textfeld mit; der Code-Editor ist der größte Eigenbau im Projekt.

- Textpuffer als Rope oder Piece Table, effizient auch bei großen Dateien.
- Highlighting inkrementell über Tree-sitter aus dem Backend, ergänzt um Semantic Tokens vom Language Server.
- Nur sichtbare Zeilen werden gezeichnet.
- Mehrfach-Cursor, Folding, Suchen/Ersetzen, Klammer-Paare.
- Gutter: Zeilennummern, Breakpoints, Git-Änderungen, Implementierungs-Icons.
- Popups für Vervollständigung, Hover und Signaturhilfe.

**Autosave wie in PhpStorm**

- Speichern bei Fokusverlust (Fenster, Tab-Wechsel, Wechsel ins Terminal).
- Speichern vor Run, Debug und Commit.
- Optional nach X Sekunden Leerlauf.
- Local History: jede Speicherung lokal versioniert und wiederherstellbar, unabhängig von Git.

## Modul-System

Der Kern bleibt klein, jede Funktion ist ein Modul mit eigenem An/Aus-Schalter. Module sprechen nur über den Event-Bus miteinander, nie direkt.

**Kern (immer an):** Fenster, Editor, Dateibaum, Tabs, Einstellungen, Event-Bus, Modul-Verwaltung, Hook-System, Spracherkennung, Autosave, Local History.

```go
type Module interface {
    ID() string
    DependsOn() []string          // z.B. debug-xdebug → php-runtime
    Start(ctx Context) error      // registriert Menüs, Panels, Gutter-Icons, Statusleiste
    Stop() error
}
```

- **MVP:** Module sind einkompiliert und werden per Einstellung aktiviert. Abhängigkeiten werden beim Einschalten geprüft.
- **Später:** Dieselbe Schnittstelle als JSON-RPC über stdio (wie LSP). Fremd-Plugins laufen dann als eigene Prozesse, ohne Architektur-Umbau.
- **Bewusst nicht:** Gos `plugin`-Paket – läuft nicht unter Windows und ist fragil.

**Hooks:** Plugins hängen sich an feste Ereignisse – ähnlich wie Actions und Filters in WordPress.

| Art | Wirkung | Beispiele |
| --- | --- | --- |
| Action | wird benachrichtigt | `project.opened`, `file.opened`, `editor.focusLost`, `debug.breakpointHit`, `interpreter.changed` |
| Filter | darf Daten ändern | `file.beforeSave` (z.B. Formatter), `commit.message` |
| Guard | darf abbrechen | `commit.before` (z.B. Lint schlägt fehl), `run.before` |

```go
hooks.On("file.beforeSave", 10, func(e *SaveEvent) error { /* formatieren */ return nil })
```

Prioritäten legen die Reihenfolge fest; jeder Hook läuft mit Zeitlimit, damit ein Plugin den Editor nicht blockiert.

**Spracherkennung:** über Dateiendung, Shebang (`#!/usr/bin/env php`) und Inhalt (`<?php`). Jede Sprache ist ein Sprach-Modul; im MVP gibt es nur `lang-php` voll.

## Module im Detail

### lang-php

- Syntax-Highlighting und Struktur über Tree-sitter (PHP-Grammatik).
- PHP-Intelligenz über LSP-Client: Phpactor (Standard), alternativ PHPantom (beide Open Source; Intelephense nur optional, da proprietär).
- Vervollständigung, Go to Definition, Go to Implementation mit Gutter-Icons wie in PhpStorm, Hover, Diagnosen.

### php-runtime

- Interpreter-Typen: lokal, Docker Compose, WSL.
- `docker-compose.yml` wird mit compose-go gelesen; PHP-Services werden erkannt und per Dropdown gewählt.
- Pfad-Mapping (Host ↔ Container) wird aus den Volumes abgeleitet und lässt sich in der Oberfläche korrigieren.
- Befehle laufen über `docker compose exec` im gewählten Service.

### php-features

Ermittelt die PHP-Version des Projekts und warnt bei Features, die diese Version nicht kennt.

1. Version, in dieser Reihenfolge:
   1. echter Interpreter (`php -v` lokal bzw. im Container)
   2. `config.platform.php` in `composer.json`
   3. `require.php` in `composer.json` (Untergrenze, z.B. `^8.2` → 8.2)
2. Anzeige in der Statusleiste, per Klick überschreibbar.
3. Feature-Tabelle (YAML): Feature → Mindestversion → Tree-sitter-Query. Neue PHP-Version = neuer Tabelleneintrag, kein neuer Code.
4. Gleiches Prinzip für Deprecations.
5. Die Version wird auch an den Language Server weitergegeben.

```yaml
- feature: enums             min: "8.1"
- feature: readonly_class    min: "8.2"
- feature: property_hooks    min: "8.4"
- feature: pipe_operator     min: "8.5"
```

### git

- Commit-Fenster wie in PhpStorm: Dateiliste mit Checkboxen (auch einzelne Änderungsblöcke), Diff-Vorschau, Nachricht mit Verlauf, Amend, „Commit and Push“.
- Änderungsmarkierungen im Gutter mit Rollback.
- Log, Branches, Diff-Ansicht.
- 3-Wege-Merge: links „ours“, Mitte Ergebnis, rechts „theirs“.
- Umsetzung über die Git-CLI.

### debug-xdebug

- Eigener DBGp-Server in Go auf Port 9003.
- Ablauf wie PhpStorm: Listener-Knopf an, Breakpoint setzen, Request oder Script auslösen.
- Pfad-Mapping kommt automatisch aus php-runtime.
- Variablen, Watches, Call Stack, Step over/into/out, bedingte Breakpoints.

## Plattformen

macOS kommt zuerst; Linux und Windows folgen über dieselbe Backend/UI-Trennung.

| Plattform | UI | Backend | Hinweis |
| --- | --- | --- | --- |
| macOS | Gio (Metal) | lokal | Signieren und Notarisieren nötig |
| Linux | Gio (Vulkan/OpenGL, Wayland/X11) | lokal | – |
| Windows | Gio (Direct3D) | in der WSL | UI spricht per Socket mit dem Backend, wie VS Code mit WSL |

## Roadmap & Aufwand

Mit Claude Code und Vollzeit-Begleitung: roher Prototyp in 4–6 Wochen, alltagstaugliches Mac-MVP in 4–6 Monaten. Der eigene Editor macht den Unterschied zu einer WebView-Lösung aus. Nebenbei (halbtags) etwa doppelt so lange.

| # | Schritt | Dauer (geschätzt) |
| --- | --- | --- |
| 1 | Kern, Modul-System, Atomic-Design-Komponenten, Dateibaum, dazu Editor-Eigenbau (2–3 Monate zusätzlich) | 1–2 Wochen |
| 2 | lang-php (LSP-Anbindung) | ca. 1 Woche |
| 3 | php-runtime (Docker-Erkennung) | 3–5 Tage |
| 4 | php-features (Versions-Prüfung) | 2–4 Tage |
| 5 | git inkl. Commit-Dialog und 3-Wege-Merge | 2–3 Wochen |
| 6 | debug-xdebug | 1–2 Wochen |
| 7 | Mac-Paketierung (Signieren, Notarisieren) | 2–3 Tage |
| 8 | Linux und Windows/WSL | 2–3 Wochen |

Die meiste Zeit geht in Testen, Bedienungs-Feinschliff und Sonderfälle, nicht ins Code-Schreiben. Ab Woche 2–3 das Tool selbst im Alltag nutzen.

## Clean-Room, Risiken, offene Fragen

**Clean-Room-Regeln**

- Kein Code, keine Icons, keine Assets von JetBrains; der Name „PhpStorm“ wird nicht verwendet.
- Bedienkonzepte und Tastenkürzel nachzubilden ist üblich; ohne Marke und Verkauf ist das Risiko gering.

**Risiken**

- **Editor-Performance:** Der Editor ist Eigenbau – größter Aufwand und größtes Risiko (große Dateien, Unicode, Eingabemethoden, Barrierefreiheit). Gio ist noch vor Version 1.0, die API kann sich ändern.
- **Xdebug mit Docker:** Pfad-Mapping und Netzwerk (`host.docker.internal`) sind die häufigsten Fehlerquellen.
- **Language Server:** Qualität von Go to Implementation hängt vom gewählten Server ab.

**Offene Fragen**

- [ ] Repository-Name auf GitHub: ist „tiegel“ noch frei?
- [ ] Editor-Puffer: Rope oder Piece Table?
- [ ] Standard-Language-Server: Phpactor oder PHPantom?
- [ ] Lizenz: MIT oder Unlicense?
- [ ] PHPUnit-Testbaum doch ins MVP?
