# admin

Single-binary server monitoring dashboard written in Go (stdlib only).
The web front-end (HTML/JS/CSS) is embedded in the binary and served with the REST API.

Monitored services:

| Service    | What                                                                 | How                                   |
|------------|----------------------------------------------------------------------|---------------------------------------|
| system     | hostname, OS, uptime, load, CPU %, memory/swap, disks, listening ports | `/proc`, `statfs`                     |
| docker     | engine version, containers (state, ports inside → outside, networks), networks | Docker Engine API over the unix socket |
| mysql      | up/down, version, port, uptime, connections, databases (size, tables) | TCP probe + `mysql` client            |
| postgres   | up/down, version, port, uptime, connections, databases (size, owner) | TCP probe + `psql` client             |
| iptables   | chain policies, port rules (port, protocol, target…), all rules       | `iptables -S` (filter table)          |
| systemd    | services with load / active / sub state (running, failed, stopped…)  | `systemctl list-units --type=service` |

The dashboard opens with an **Overview** card that lists every monitored service with its
health (ok / up / down / error / stale / disabled), a one-line summary and its last update.

Each collector runs in its own goroutine on its own interval and stores its latest
snapshot in memory. The API only reads that snapshot, so requests are instant; every
entry carries `updated_at` (last success), `last_attempt`, and `error`. On a failed
collection the last good data is kept and the error is shown next to it.

## Build

```sh
make build          # produces ./admin (static, CGO disabled)
make test
```

## Run locally

```sh
cp config.example.json config.json   # edit credentials
./admin run -config config.json -port 8090
```

## Install as a systemd service

```sh
sudo ./admin install -port 9000          # or: sudo make install PORT=9000
```

This:
1. copies the binary to `/usr/local/bin/admin`
2. creates `/etc/admin/config.json` (mode 0600) if missing, with a **generated password printed once**;
   if it exists, only the port / listen address are updated
3. writes `/etc/systemd/system/admin.service`, then enables and (re)starts it

Change the port later with `sudo admin install -port 9100` again, or edit
`/etc/admin/config.json` and run `sudo systemctl restart admin`.

Remove with `sudo admin uninstall` (add `-purge` to also delete `/etc/admin`).

The service runs as root because reading iptables rules and the Docker socket require it.

## Configuration

See [config.example.json](config.example.json). Notes:

- `auth.username` empty disables HTTP Basic auth (not recommended).
- `system.mounts` restricts the disk list; empty auto-detects real filesystems.
- Set `enabled: false` to disable a collector.
- `systemd.units` restricts the list to these services (`"nginx"`, `"docker*"`; the `.service`
  suffix is optional) and also shows them when stopped; names that do not exist are reported
  as `not found`. Empty lists every active or failed service, or every loaded one with `"all": true`.
- Database credentials: create a read-only monitoring user, for example:
  ```sql
  -- MySQL
  CREATE USER 'monitor'@'127.0.0.1' IDENTIFIED BY '...';
  GRANT PROCESS, SHOW DATABASES ON *.* TO 'monitor'@'127.0.0.1';
  -- PostgreSQL
  CREATE ROLE monitor LOGIN PASSWORD '...' IN ROLE pg_monitor;
  ```
  The MySQL password goes through a temporary 0600 defaults file, and the PostgreSQL one through `PGPASSWORD`.
  Neither is passed on the command line.

## API

All endpoints except `/healthz` require Basic auth when it is enabled.

| Endpoint                   | Description                         |
|----------------------------|-------------------------------------|
| `GET /api/status`          | all services + hostname + server time |
| `GET /api/services/{name}` | one service: `system`, `docker`, `mysql`, `postgres`, `iptables`, `systemd` |
| `GET /healthz`             | liveness (`ok`)                     |

Entry format:

```json
{
  "name": "docker",
  "enabled": true,
  "interval_seconds": 10,
  "updated_at": "2026-11-02T18:56:20+01:00",
  "last_attempt": "2026-11-02T18:56:20+01:00",
  "duration_ms": 4,
  "error": "",
  "data": { "...": "service specific" }
}
```
