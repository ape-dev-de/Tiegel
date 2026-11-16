# Tiegel – Hinweise für Agenten

Schnelle, native PHP-IDE in Go mit Gio. Konzept: `docs/konzept.md`, Plan: `docs/ROADMAP.md`, Stand: `docs/STATUS.md`, Entscheidungen: `docs/DECISIONS.md`.

## Regeln
- **Nur Go**, kein JavaScript/WebView. UI ausschließlich mit Gio (`gioui.org`).
- Backend-Pakete (`internal/core`, `editor`, `syntax`, `lsp`, `dbgp`, `gitcli`, `compose`, `phpver`, `api`) importieren **kein Gio**.
- Module kommunizieren nur über Event-Bus/Hooks (`internal/core`), nie direkt miteinander.
- Plugins/Module bauen UI nur aus `internal/ui/{atoms,molecules,organisms}`; Farben/Abstände nur aus `internal/ui/tokens`.
- **Privacy:** kein Netzwerkzugriff außer vom Nutzer ausgelöst (DBGp-Listener lokal, `git push`, Update-Prüfung opt-in). Keine Telemetrie, keine Downloads.
- **Clean-Room:** kein Code, keine Icons, keine Assets von JetBrains; den Namen „PhpStorm“ nicht im Code/UI verwenden.
- Abhängigkeiten nur mit OSI-Lizenz und aktiver Pflege. Neue Abhängigkeit → in `docs/DECISIONS.md` begründen.
- Code, Bezeichner, Kommentare auf Englisch; Doku in `docs/` auf Deutsch.

## Arbeiten
- `make check` = `gofmt`-Prüfung + `go vet` + `go test -race ./...`. Muss vor jedem Commit grün sein.
- Jedes Paket hat Tests; Backend-Pakete Ziel ≥ 80 % Abdeckung. Tests ohne Netz, externe Programme (git, php, docker) nur wenn vorhanden, sonst `t.Skip`.
- Öffentliche APIs mit Doc-Kommentaren. Kleine, fokussierte Pakete.
- Kein `panic` in Bibliothekscode; Fehler mit `fmt.Errorf("...: %w", err)` umhüllen.
- Nebenläufigkeit: `context.Context` als erstes Argument bei blockierenden Aufrufen.
- Nach Abschluss eines Arbeitspakets `docs/STATUS.md` (eigene Zeile) aktualisieren.
