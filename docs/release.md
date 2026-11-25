# Release & Paketierung

Wie aus dem Quellcode ein auslieferbares `Tiegel.app`/DMG wird – lokal und per GitHub Actions. Zertifikate und Apple-Zugänge liefert der Nutzer selbst; ohne sie funktioniert alles mit **Ad-hoc-Signatur** (nur für den eigenen Rechner).

## Überblick

| Datei | Zweck |
| --- | --- |
| `packaging/macos/Info.plist.in` | Vorlage für `Info.plist` (Bundle-ID, Versionen, Dokumenttypen `.php` und Ordner) |
| `packaging/macos/Tiegel.entitlements` | Entitlements für die Hardened Runtime (bewusst leer, Begründung in der Datei) |
| `packaging/macos/mkicon` | Eigenes App-Icon (Schmelztiegel) als Vektor in Go; erzeugt PNG/SVG |
| `scripts/ldflags.sh` | Versions-Linkerflags für `internal/version` |
| `scripts/macos/build-app.sh` | Universal-Binary (arm64 + amd64, cgo) bauen, Icon erzeugen, Bundle zusammensetzen, signieren |
| `scripts/macos/sign.sh` | `codesign` mit Hardened Runtime; Identität aus `TIEGEL_SIGN_IDENTITY`, sonst ad-hoc |
| `scripts/macos/dmg.sh` | DMG mit `Applications`-Verknüpfung, signiert |
| `scripts/macos/notarize.sh` | `notarytool submit --wait` + `stapler staple`; ohne Zugangsdaten übersprungen |
| `.github/workflows/release.yml` | Release bei Tag `v*` |

Alle Skripte haben `--help`, laufen mit `set -euo pipefail` und sind wiederholbar (vorhandene Ergebnisse werden ersetzt).

## Lokal bauen

Voraussetzungen: macOS mit Xcode bzw. Command Line Tools (`xcode-select --install`) und Go laut `go.mod`.

```sh
make app                 # dist/Tiegel.app, universal, ad-hoc signiert
make dmg                 # zusätzlich dist/Tiegel-<version>.dmg
make install             # für diesen Mac bauen und nach /Applications installieren

APP_DIR=~/Applications make install   # anderes Zielverzeichnis
ARCH=universal make install           # beide Architekturen

scripts/macos/build-app.sh --arch arm64   # nur Apple Silicon (schneller)
scripts/macos/build-app.sh --no-sign      # ohne Signatur
```

- **Version:** `git describe --tags --always --dirty` (überschreibbar mit `TIEGEL_VERSION`). Daraus werden `CFBundleShortVersionString` (`x.y.z`, ohne Tag `0.0.0`), `CFBundleVersion` (Anzahl Commits, monoton steigend) und die Werte in `internal/version` (`Version`, `Commit`, `Date` per `-ldflags -X`) gesetzt. `Date` ist die Commit-Zeit (reproduzierbar).
- **Mindest-macOS:** 11.0 (Go 1.24 unterstützt nichts Älteres; Gio/Metal ist kein Engpass). Wird als `LSMinimumSystemVersion` und `-mmacosx-version-min` gesetzt.
- **Universal Binary:** beide Architekturen werden mit `CGO_ENABLED=1` gebaut (Gio und Tree-sitter brauchen cgo) und mit `lipo` vereint. Das Cross-Compiling nach amd64 funktioniert auf Apple Silicon ohne Zusatzwerkzeuge, weil Apples `clang` beide Architekturen kann; das Skript verwendet bewusst `/usr/bin/clang` und `/usr/bin/lipo` (Xcode-Shims), damit kein `clang`/`lipo` aus Conda oder Homebrew dazwischenfunkt.
- **Icon:** `go run ./packaging/macos/mkicon -png icon.png -svg icon.svg` erzeugt das Icon aus Vektorformen; `build-app.sh` macht daraus mit `sips` und `iconutil` die `.icns`. Keine fremden Assets.

Ad-hoc-signierte Apps laufen auf dem eigenen Mac. Auf anderen Macs blockiert Gatekeeper sie („nicht verifizierter Entwickler“); dort hilft nur Rechtsklick → Öffnen oder `xattr -dr com.apple.quarantine Tiegel.app`.

## Signieren und Notarisieren mit eigenem Apple-Developer-Account

Benötigt: Mitgliedschaft im Apple Developer Program (kostenpflichtig) und ein Zertifikat vom Typ **Developer ID Application**.

1. **Zertifikat** in Xcode (Settings → Accounts → Manage Certificates → „+“ → Developer ID Application) oder im Developer-Portal anlegen und in den Schlüsselbund importieren. Identität prüfen:
   ```sh
   security find-identity -v -p codesigning
   # "Developer ID Application: Jane Doe (TEAMID1234)"
   ```
2. **Notarisierungs-Zugang** einmalig im Schlüsselbund ablegen (App-spezifisches Passwort unter appleid.apple.com erzeugen):
   ```sh
   xcrun notarytool store-credentials tiegel-notary \
     --apple-id jane@example.org --team-id TEAMID1234 --password <app-spezifisches-passwort>
   ```
   Alternativ ein App-Store-Connect-API-Schlüssel (`.p8`) über `TIEGEL_NOTARY_KEY`, `TIEGEL_NOTARY_KEY_ID`, `TIEGEL_NOTARY_ISSUER`.
3. **Bauen, signieren, notarisieren:**
   ```sh
   export TIEGEL_SIGN_IDENTITY="Developer ID Application: Jane Doe (TEAMID1234)"
   export TIEGEL_NOTARY_PROFILE=tiegel-notary
   make dmg
   scripts/macos/notarize.sh --require dist/Tiegel-v0.1.0.dmg
   spctl --assess --type open --context context:primary-signature -v dist/Tiegel-v0.1.0.dmg
   ```

`sign.sh` signiert mit `--options runtime --timestamp` (Hardened Runtime, sicherer Zeitstempel – beides Voraussetzung für die Notarisierung) und den Entitlements aus `packaging/macos/Tiegel.entitlements`. Notarisiert wird das DMG; Apple prüft dabei auch die enthaltene App. Das Ticket wird an das DMG geheftet (`stapler staple`), sodass die erste Prüfung auch offline klappt.

### Entitlements

Die Datei ist absichtlich leer:
- **Keine App-Sandbox**, weil Tiegel `php`, `docker`, `git` und Language Server startet und beliebige Projektordner bearbeitet.
- **Kein `com.apple.security.network.server`** für den DBGp-Listener (Port 9003): Netzwerk-Entitlements gehören zur Sandbox und wirken bei nicht-sandboxed Apps nicht. Lauscht Tiegel nicht nur auf Loopback, kann die macOS-Firewall nachfragen – das ist eine Systemeinstellung, kein Entitlement.
- **Keine Hardened-Runtime-Ausnahmen** (JIT, unsignierter ausführbarer Speicher, Library Validation): Go und die cgo-Teile brauchen sie nicht. Kindprozesse sind eigene Programme mit eigener Signatur.

## Release per GitHub Actions

Ablauf:

1. `docs/STATUS.md`/Changelog aktualisieren, auf `main` mergen, CI grün.
2. Tag setzen und pushen:
   ```sh
   git tag -a v0.1.0 -m "Tiegel 0.1.0"
   git push origin v0.1.0
   ```
3. Workflow `Release` (`.github/workflows/release.yml`) läuft:
   - **macos** (macos-latest): `make check`, Universal-App bauen, signieren (mit Secrets: Developer ID, sonst ad-hoc), DMG erzeugen, notarisieren und heften (nur wenn Signatur- **und** Notarisierungs-Secrets vorhanden).
   - **linux** (ubuntu-latest, Bonus, blockiert das Release nicht): `tiegel` für linux/amd64 als `Tiegel-<tag>-linux-amd64.tar.gz`.
   - **publish**: `SHA256SUMS` über alle Artefakte, GitHub-Release anlegen (Tags mit `-`, z.B. `v0.2.0-rc.1`, werden als Pre-Release markiert) oder bei erneutem Lauf die Assets ersetzen.
4. Notizen im Release auf GitHub nachbearbeiten (die Update-Prüfung zeigt sie als `Notes` an).

Ein bestehender Tag lässt sich über „Run workflow“ (`workflow_dispatch`, Eingabe `tag`) neu bauen.

### Secrets (Repository → Settings → Secrets and variables → Actions)

| Secret | Inhalt |
| --- | --- |
| `MACOS_CERTIFICATE_P12` | Developer-ID-Application-Zertifikat samt privatem Schlüssel als `.p12`, base64-kodiert (`base64 -i cert.p12 \| pbcopy`) |
| `MACOS_CERTIFICATE_PASSWORD` | Passwort der `.p12`-Datei |
| `MACOS_SIGN_IDENTITY` | Name der Identität, z.B. `Developer ID Application: Jane Doe (TEAMID1234)` |
| `NOTARY_API_KEY_P8` | App-Store-Connect-API-Schlüssel (`.p8`, Rolle „Developer“), base64-kodiert |
| `NOTARY_API_KEY_ID` | Key-ID des API-Schlüssels |
| `NOTARY_API_ISSUER_ID` | Issuer-ID (App Store Connect → Users and Access → Integrations) |

Fehlen die Secrets, entsteht trotzdem ein (ad-hoc signiertes) Release. Das Zertifikat landet nur in einem temporären Schlüsselbund des Runners, der am Ende gelöscht wird.

## Update-Prüfung (opt-in)

`internal/update` prüft auf Wunsch, ob es eine neuere Version gibt:

- **Standard aus** (`Config.Enabled == false`); eine ausdrückliche Nutzeraktion („Nach Updates suchen“) nutzt `CheckNow`.
- **Quelle frei wählbar:** Standard ist `https://api.github.com/repos/ape-dev-de/Tiegel/releases/latest`. Jede selbst gehostete URL mit kompatiblem JSON funktioniert – ein Objekt oder eine Liste von Objekten mit `tag_name`, `html_url` und optional `body`, `draft`, `prerelease`:
  ```json
  {"tag_name": "v0.2.0", "html_url": "https://example.org/tiegel/0.2.0", "body": "Änderungen …"}
  ```
  Nur `https` (bzw. `http` zu `localhost`).
- **Keine Kennung:** fester User-Agent `Tiegel-Update-Check`, keine Cookies, keine Query-Parameter, keine Geräte-ID, nicht einmal die eigene Version wird gesendet – der Vergleich passiert lokal (Semver, `git describe`-Suffixe wie `-3-gabc123` zählen als die getaggte Version).
- **Kein Download, keine Installation:** Ergebnis ist nur `{Available, Version, URL, Notes}`; die GUI zeigt den Link an.
