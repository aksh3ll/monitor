# AGENTS.md

Single-binary server monitoring dashboard in Go. The README covers what is monitored, the
API, configuration and installation. This file covers how to work on the code.

## Commands

```sh
make build      # CGO_ENABLED=0 static binary ./admin
make test       # go vet ./... && go test ./...
gofmt -l .      # must print nothing
node --check web/static/app.js   # quick syntax check of the front-end
```

Run locally with a throwaway config, without auth and with the collectors you don't
have turned off:

```sh
echo '{"port":8199,"listen":"127.0.0.1","auth":{"username":""},"mysql":{"enabled":false}}' > /tmp/cfg.json
./admin run -config /tmp/cfg.json
curl -s localhost:8199/api/status | python3 -m json.tool
```

Never edit `/etc/admin/config.json` or run `admin install` / `uninstall` unless asked.
Those commands change the real systemd service.

## Hard constraints

- **Go standard library only.** No third-party modules. `go.mod` has no `require` block; keep it that way.
- **No front-end build step and no JS/CSS libraries.** `web/static/` is plain HTML/CSS/JS,
  embedded with `//go:embed` ([web/embed.go](web/embed.go)). Changing it requires rebuilding the binary.
- **The CSP forbids inline code** (`script-src 'self'; style-src 'self'`, set in
  [server.go](internal/server/server.go)). Don't add inline `<script>`, `onclick=` or `style="..."` attributes.
  Setting `el.style.x` from JS is fine.
- **Never use `innerHTML`.** Build the DOM with the `h(tag, attrs, ...children)` helper in
  [app.js](web/static/app.js). It writes text through text nodes, and collector data (container
  names, unit descriptions, iptables comments) is untrusted.
- **Secrets stay off command lines.** DB passwords go through a 0600 temp defaults file (MySQL)
  or `PGPASSWORD` (Postgres). Config is saved 0600.
- The service runs as root. Collectors must stay read-only: don't run commands that change the system.

## Architecture

```
main.go                  CLI (run/install/uninstall/version) + collector registration
internal/config          JSON config, defaults (Default), interval sanitising (Validate)
internal/collector       one file per collector + shared helpers (runCmd, parseTSV) + tests
internal/store           latest snapshot per collector, mutex-protected
internal/server          REST API, Basic auth, security headers, static files
internal/service         systemd unit install/uninstall
web/static               index.html, app.js, style.css, icons/*.svg
```

Each collector runs in its own goroutine (`collector.Run`) with a timeout of `min(interval, 30s)`.
It writes into the store, and the API only reads the store. A collector that returns data *and*
an error keeps the data and shows the error, which is how partial failures are reported
(see iptables with IPv6).

## Adding a collector (checklist)

1. `internal/collector/<name>.go`: implement `Collector` (`Name()`, `Collect(ctx) (any, error)`).
   Shell out through `runCmd(ctx, env, bin, args...)` after `exec.LookPath`. Keep parsing in pure
   functions so they can be tested.
2. `internal/config/config.go`: add a struct with `Enabled`, `Interval` (`interval_seconds`), a
   field on `Config`, defaults in `Default()`, and the interval pointer in `Validate()`.
3. `main.go`: `add(collector.NewX(cfg.X), cfg.X.Enabled, cfg.X.Interval)`.
4. `config.example.json`: add the section.
5. `internal/collector/collector_test.go`: table-style test of the parser, using sample command output.
6. Front-end: a `renderX(entry)` that starts with `serviceState(entry)` (it handles
   disabled/pending/error) and returns cards via `card({...})`. Add a `<section>` with a grid id to
   `index.html`, call `replace(id, renderX(sv.x))` in `render()`, add an entry to `SERVICES` and
   `serviceDetail` / `serviceHealth` so the Overview card shows it, and add an icon in `icons/`
   (48×48 viewBox, stroke-only, one accent colour).
7. README: services table, config notes, the `/api/services/{name}` list.

## Front-end conventions

- Status colours are CSS classes `ok` / `warn` / `bad` / `off` on `pill()`, meters and text.
  Reuse them instead of adding new colours.
- `card({ title, sub, icon, status, body, entry, wide })`. Pass `entry` to get the
  "updated … / every Ns" footer, and use `wide` for tables.
- The page re-renders everything every 5s, so UI state (inputs, open `<details>`) is lost on
  refresh. Keep that in mind before adding interactive controls.
- JSON fields are `snake_case`, and Go structs use explicit `json:` tags. Return empty slices,
  not `nil`, for lists the UI iterates over.

## Style

- `gofmt`, short doc comments on exported items and non-obvious helpers, errors wrapped with
  context (`fmt.Errorf("parse %s: %w", ...)`).
- Code, comments and UI text are in English.
