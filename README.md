# Tiegel

Schnelle, schlanke PHP-IDE in Go mit nativer Oberfläche ([Gio](https://gioui.org)). Mac-first, später Linux und Windows (WSL).
Privacy first: keine Telemetrie, keine Konten, keine stillen Downloads.

> Status: frühe Entwicklung – siehe [docs/STATUS.md](docs/STATUS.md).

- Konzept: [docs/konzept.md](docs/konzept.md)
- Roadmap: [docs/ROADMAP.md](docs/ROADMAP.md)
- Entscheidungen: [docs/DECISIONS.md](docs/DECISIONS.md)

## Bauen

```sh
make check   # gofmt, vet, Tests
make run     # IDE starten
make catalog # Komponenten-Katalog
```

Lizenz: MIT
