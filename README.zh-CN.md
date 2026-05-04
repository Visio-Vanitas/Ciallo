# ciallo

[English](README.md)

`ciallo` 是一个 Minecraft Java Edition 反向代理工具。项目名融合了意大利语 `ciao` 和英语 `hello`，都带有“你好”的意思。它在一个公开 TCP 端口上监听，读取 MCJE 初始握手里的 `Server Address`，按主机名把连接路由到配置好的本地后端服务器，然后对登录和游戏流量保持透明转发。

代理只解析初始明文握手。在线模式加密、压缩、登录状态和游戏数据包都由后端服务器负责。

## 功能

- 基于 MCJE 握手 `Server Address` 字段的主机名路由。
- 登录和游玩连接使用透明 TCP 转发。
- 服务器列表 status 响应短 TTL 缓存。
- 后端临时不可用时，可用缓存的 MOTD 生成降级 status 响应。
- 实验性透明 fail2ban，基于代理可见的早退登录断开信号。
- 仅用于 status 路径的保守预连接池。
- YAML 配置。
- MIT 许可证。

## 快速开始

```sh
go mod tidy
go test ./...
go run ./cmd/mcproxy -config configs/example.yaml
```

查看当前测试版版本：

```sh
go run ./cmd/mcproxy -version
```

示例配置：

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

## 配置

完整示例位于 `configs/example.yaml`。

重要字段：

- `listen`：公开 TCP 监听地址，通常是 `:25565`。
- `routes[].hosts`：路由到该后端的握手主机名。
- `routes[].backend`：本地 Minecraft 服务器地址。
- `default_backend`：没有匹配主机名路由时使用的默认后端。
- `status_cache.enabled`：status 响应缓存总开关。
- `status_cache.ttl`：短缓存 TTL，默认 `5s`。
- `motd_cache.enabled`：启用 MOTD 降级快照。
- `motd_cache.fallback_ttl`：后端 status 查询失败时，过期 MOTD 快照仍可被用于降级响应的时长。
- `fail2ban.enabled`：启用实验性内存临时封禁。v0.0.3 默认关闭。
- `fail2ban.max_failures`：窗口期内触发封禁所需的失败次数。
- `fail2ban.window`：登录失败统计窗口。
- `fail2ban.ban_duration`：临时封禁时长。
- `fail2ban.early_disconnect`：短于该时间且没有服务端回包的登录会话会被计为失败。
- `management.enabled`：启用本地管理 HTTP 服务。默认关闭。
- `management.address`：管理服务绑定地址，默认 `127.0.0.1:25575`。
- `pool.enabled`：启用 status 预连接。登录和游戏连接永不复用。
- `logging.level`：`debug`、`info`、`warn` 或 `error`。
- `logging.format`：`text` 或 `json`。默认是 `text`。
- `logging.output`：`stdout`、`stderr` 或 `file`。文件输出带内置轮转。
- `logging.file.*`：文件路径、大小、备份数量、保留天数和压缩设置。

文件日志示例：

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

status 和 login 连接会输出结构化访问日志，包含路由、后端、协议版本、耗时、缓存结果、ping/pong 处理、字节数、fail2ban 动作和错误摘要。不会记录原始包内容、完整 MOTD JSON、加密数据或游戏流量。

## 协议说明

Minecraft Java Edition 连接的第一个包是未加密握手：

```text
Length VarInt
Packet ID VarInt = 0x00
Protocol Version VarInt
Server Address String
Server Port Unsigned Short
Next State VarInt
```

`Next State = 1` 表示服务器列表 status。`Next State = 2` 表示登录。对于登录路径，ciallo 只读取明文 `Login Start` 前缀来观察玩家名，然后转发原始字节。登录和游玩流量可能随后启用压缩和加密，因此代理不会检查这些阶段。

原版在线模式认证由后端服务器在登录流程进入加密后完成。ciallo 不终止加密，也无法看到 Mojang session 验证结果。因此实验性 fail2ban 使用一个保守的透明信号：代理可见的重复早退登录断开，并按路由加 IP 或玩家名进行隔离统计。

v0.0.3 的 fail2ban 状态保存在内存中。启用本地管理服务后，`GET /fail2ban/bans` 可以列出当前封禁，`DELETE /fail2ban/bans?route=<route>&kind=<ip|player>&value=<value>` 可以在不重启代理的情况下解除一条封禁。

参考资料：

- [Minecraft Wiki: Java Edition protocol](https://minecraft.wiki/w/Java_Edition_protocol)
- [Minecraft Wiki: Java Edition protocol encryption](https://minecraft.wiki/w/Java_Edition_protocol/Encryption)
- [wiki.vg protocol mirror](https://c4k3.github.io/wiki.vg/Protocol.html)

## Release

CI 会在 push 和 pull request 上运行格式检查、版本检查、`go vet` 和测试。当前版本存放在 `VERSION`。

`v0.0.x` 是测试版，不创建 GitHub Release。从 `v0.1.0` 开始的正式标签会构建 GitHub Release，并为以下主流系统和架构生成归档：

- Linux amd64 和 arm64。
- macOS amd64 和 arm64。
- Windows amd64 和 arm64。

每个 release 包含 `mcproxy`、`README.md`、`README.zh-CN.md`、`LICENSE`、`configs/example.yaml` 和 `SHA256SUMS`。

## 仓库约定

仓库使用白名单式 `.gitignore`：默认忽略所有文件，再显式允许源码、测试、配置、文档、许可证、Go module 文件和 GitHub workflow。

## 限制

- v1 不支持通配符主机名路由。
- 不做登录 MITM、加密终止、数据包改写或游戏数据缓存。
- 游戏连接永不在客户端之间复用。
- status 缓存 TTL 应保持较短，因为 MOTD 和玩家数量可能快速变化。

## 许可证

MIT。见 `LICENSE`。
