# ciallo

[简体中文](README.zh-CN.md)

`ciallo` is a Minecraft Java Edition reverse proxy. The name blends Italian `ciao` and English `hello`. It listens on one public TCP port, reads the initial handshake `Server Address`, routes the connection to a configured local backend, and then stays transparent for login and game traffic.

The proxy deliberately parses only the initial plaintext handshake. Online-mode encryption, compression, login state, and game packets remain owned by the backend server.

## Features

- Host-based routing from the MCJE handshake `Server Address` field.
- Transparent TCP forwarding for login and play connections.
- Short-TTL cache for server list status responses.
- MOTD fallback for status responses when a backend is temporarily unavailable.
- Experimental transparent fail2ban based on early login disconnect signals visible to the proxy.
- Conservative pre-connection pool for status paths only.
- YAML configuration.
- MIT licensed.

## Quick Start

```sh
go mod tidy
go test ./...
go run ./cmd/mcproxy -config configs/example.yaml
```

Current test-build version:

```sh
go run ./cmd/mcproxy -version
```

Example config:

```yaml
listen: ":25565"

routes:
  - hosts:
      - "survival.example.com"
    backend: "127.0.0.1:25566"

  - hosts:
      - "creative.example.com"
    backend: "127.0.0.1:25567"

default_backend: "127.0.0.1:25566"
```

## Configuration

The full example lives in `configs/example.yaml`.

Important fields:

- `listen`: public TCP address, usually `:25565`.
- `routes[].hosts`: handshake host names routed to this backend.
- `routes[].backend`: local Minecraft server address.
- `default_backend`: fallback backend when no host route matches.
- `status_cache.enabled`: global status response caching switch.
- `status_cache.ttl`: short cache TTL, default `5s`.
- `motd_cache.enabled`: enables MOTD fallback snapshots.
- `motd_cache.fallback_ttl`: how long an expired MOTD snapshot can be used when a backend status query fails.
- `fail2ban.enabled`: enables experimental in-memory temporary bans. It is disabled by default in v0.0.3.
- `fail2ban.max_failures`: failures within the window before a ban.
- `fail2ban.window`: rolling window for login failures.
- `fail2ban.ban_duration`: temporary ban duration.
- `fail2ban.early_disconnect`: login sessions shorter than this with no server bytes count as failures.
- `management.enabled`: enables the local management HTTP server. Disabled by default.
- `management.address`: management bind address, default `127.0.0.1:25575`.
- `pool.enabled`: enables status pre-connections. Login and play connections are never reused.
- `logging.level`: `debug`, `info`, `warn`, or `error`.
- `logging.format`: `text` or `json`. The default is `text`.
- `logging.output`: `stdout`, `stderr`, or `file`. File output uses built-in rotation.
- `logging.file.*`: file path, size, backup count, age, and compression settings.

File logging example:

```yaml
logging:
  level: "info"
  format: "json"
  output: "file"
  file:
    path: "logs/ciallo.log"
    max_size_mb: 100
    max_backups: 7
    max_age_days: 14
    compress: true
```

Status and login connections emit structured access logs with route, backend, protocol, duration, cache result, ping/pong handling, byte counts, fail2ban action, and error summary. Packet bodies, MOTD JSON, encryption data, and game traffic are not logged.

## Protocol Notes

The first packet on a Minecraft Java Edition connection is an unencrypted handshake:

```text
Length VarInt
Packet ID VarInt = 0x00
Protocol Version VarInt
Server Address String
Server Port Unsigned Short
Next State VarInt
```

`Next State = 1` is server-list status. `Next State = 2` is login. For login, ciallo only reads the plaintext `Login Start` prefix to observe the player name and then forwards the original bytes. Login and play traffic may enable compression and encryption, so this proxy does not inspect those phases.

Vanilla online-mode authentication is performed by the backend server after the login flow enters encryption. ciallo does not terminate encryption and cannot see the Mojang session verdict. The experimental fail2ban mechanism therefore uses a conservative transparent signal: repeated early login disconnects visible at the proxy, scoped by route plus IP or player name.

Fail2ban state is in memory for v0.0.3. When the local management server is enabled, `GET /fail2ban/bans` lists active bans and `DELETE /fail2ban/bans?route=<route>&kind=<ip|player>&value=<value>` clears one without a restart.

References:

- [Minecraft Wiki: Java Edition protocol](https://minecraft.wiki/w/Java_Edition_protocol)
- [Minecraft Wiki: Java Edition protocol encryption](https://minecraft.wiki/w/Java_Edition_protocol/Encryption)
- [wiki.vg protocol mirror](https://c4k3.github.io/wiki.vg/Protocol.html)

## Releases

CI runs formatting, version checks, vet, and tests on pushes and pull requests. The current version is stored in `VERSION`.

`v0.0.x` versions are test builds and do not create GitHub Releases. Formal tags from `v0.1.0` onward build a GitHub Release with archives for:

- Linux amd64 and arm64.
- macOS amd64 and arm64.
- Windows amd64 and arm64.

Each release includes `mcproxy`, `README.md`, `README.zh-CN.md`, `LICENSE`, `configs/example.yaml`, and `SHA256SUMS`.

## Repository Hygiene

The repository uses an allowlist `.gitignore`: everything is ignored by default, then source, tests, configuration, documentation, license, module files, and GitHub workflows are explicitly allowed.

## Limits

- No wildcard host routing in v1.
- No login MITM, encryption termination, packet rewriting, or play-data caching.
- Game connections are never reused across clients.
- Status cache TTL should stay short because MOTD/player counts can change quickly.

## License

MIT. See `LICENSE`.
