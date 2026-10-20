# 运维指南

[English](../operations.md) | [简体中文](operations.md)

## 命令

远端源使用 `remote list`、`remote update [--url U] --region cn`、
`remote update --all`、`remote remove --url U` 管理；支持 `--dry-run` 和上一次结果备份，
详见[远端订阅](remotes.md)。`upstream add` 无需 `--id`，启用/禁用/删除可用 `--url`
代替旧标签。重载不打断正在传输的 HTTP 请求。

| 命令 | 用途 |
|---|---|
| `init [--config F] [--force]` | 写入默认配置文件。 |
| `serve [--config F] [--admin-socket S] [--log-level L]` | 在前台运行代理。 |
| `status [--admin-socket S]` | 显示各上游的熔断状态、请求数、失败数和最近一次延迟。 |
| `reload [--admin-socket S]` | 重新读取运行中代理的配置文件。 |
| `upstream list [--registry R]` | 列出上游。 |
| `upstream add --id I --url U [--auth T --username N --password-ref REF --first-byte D --overall D --ca-file F --insecure-skip-verify --disabled]` | 添加上游。 |
| `upstream enable\|disable\|remove --id I [--registry R]` | 启用、禁用或移除上游。 |
| `dockerd plan [flags]` | 预览 `apply` 将写入的文件，不执行写入。 |
| `dockerd apply [flags] [--force] [--no-reload]` | 写入 dockerd 配置，并按需向 dockerd 发送信号。 |
| `dockerd revert [flags] [--no-reload]` | 撤销上一次 apply。 |
| `doctor [flags]` | 只读检查代理、dockerd 是否可达以及配置是否就绪。任一检查失败则退出码为 1。 |
| `version`、`help` | 显示版本、帮助。 |

退出码：`0` 表示成功，`1` 表示运行时失败，`2` 表示用法错误。

### dockerd 参数

| 参数 | 默认值（系统安装） | 默认值（rootless） |
|---|---|---|
| `--store auto\|containerd\|legacy` | `auto` | `auto` |
| `--rootless` | 关闭 | 启用后使用用户级路径 |
| `--certs-dir` | `/etc/docker/certs.d` | `$XDG_CONFIG_HOME/docker/certs.d` |
| `--daemon-json` | `/etc/docker/daemon.json` | `$XDG_CONFIG_HOME/docker/daemon.json` |
| `--docker-socket` | `/var/run/docker.sock` | `$XDG_RUNTIME_DIR/docker.sock` |
| `--pid-file` | `/var/run/docker.pid` | `$XDG_RUNTIME_DIR/docker.pid` |
| `--state-dir` | `/var/lib/oci-mirror-proxy` | `$XDG_STATE_HOME/oci-mirror-proxy` |

`--store auto` 会向运行中的 dockerd 查询所用镜像存储后端。dockerd 未运行时，请传入 `--store containerd` 或 `--store legacy`。

## 配置 dockerd

1. 确保代理已运行，且 dockerd 能通过预期地址访问它。`doctor` 会检查这一点。
2. 执行 `dockerd plan`，检查列出的文件。此时不会写入任何内容。
3. 执行 `dockerd apply`。文件会原子写入，已有文件备份到 `<state-dir>/backups/`。除非传入 `--force`，否则拒绝覆盖非本工具创建的文件。
4. 传统存储后端需要重载时，会向 dockerd 发送 `SIGHUP`。containerd 后端每次解析都会读取 `hosts.toml`，无需发送信号。
5. 执行 `doctor`，所有检查都应通过。

### 写入的内容

**containerd 存储后端**：为每个仓库写入 `<certs-dir>/<alias>/hosts.toml`：

```toml
# Managed by oci-mirror-proxy. Changes are overwritten by `oci-mirror-proxy dockerd apply`.
server = "https://registry-1.docker.io"

[host."http://127.0.0.1:24817"]
  capabilities = ["pull", "resolve", "referrers"]
```

containerd 先尝试 `[host]` 条目，最后尝试 `server`，因此官方仓库仍是回退目标。对于使用路径前缀的仓库（例如 `ghcr.io`），会生成 `[host."http://127.0.0.1:24817/ghcr.io"]`。

**传统存储后端**：在 `daemon.json` 的 `registry-mirrors` 中将代理放在首位。其他条目保留为回退地址，其他所有配置项保持不变。

传统后端仅支持 `docker.io` 镜像源。对于其他仓库，`plan`、`apply` 和 `doctor` 会给出警告并跳过其 dockerd 镜像源配置，但不改变代理自身的路由。Docker Hub 配置仍会继续。如果未配置任何受支持的仓库，`plan` 和 `apply` 会成功退出，不写文件、不修改已有 apply 状态，也不向 dockerd 发送信号。

### 还原

`dockerd revert` 从备份恢复各个文件；若文件原先不存在，则将其移除。还会移除 apply 创建且已为空的目录，并删除状态文件。备份会保留以便检查。

## 重载与重启

| 变更 | 生效方式 |
|---|---|
| 上游、竞速、熔断、超时、认证（通过 CLI、管理 API 或编辑文件后重载） | 立即生效。未改变的上游保留熔断状态和计数器。 |
| `listen` | 记录日志，重启后生效。 |
| dockerd 配置（`dockerd apply`） | containerd 后端：对新拉取立即生效；传统后端：向 dockerd 发送信号。 |

重载验证失败时，会继续使用原配置并记录原因。

## Rootless dockerd

代理绑定到 `listen` 指定的地址。rootless dockerd 运行在独立的网络命名空间中，RootlessKit 默认禁止访问宿主机回环地址（参见 moby 源码中的 `contrib/dockerd-rootless.sh` 和 `DOCKERD_ROOTLESS_ROOTLESSKIT_DISABLE_HOST_LOOPBACK`）。本项目不绕过该限制，可选择以下方案之一：

- 保留默认的回环访问限制，将 `advertise` 指向 rootless dockerd 可达的地址，并确保 `listen` 绑定到该地址。
- 在 dockerd 服务覆盖配置中设置 `DOCKERD_ROOTLESS_ROOTLESSKIT_DISABLE_HOST_LOOPBACK=false`。这会允许 rootless dockerd 访问宿主机回环服务，需要明确接受相应的安全权衡。

rootless 默认使用 `$XDG_CONFIG_HOME/docker/daemon.json` 和 `$XDG_RUNTIME_DIR/docker.pid`。如果实际安装使用不同路径，请传入 `--daemon-json` 或 `--pid-file`。

## 日志

日志以 `logfmt` 格式输出到 stderr，级别如下：

- `DEBUG`：每个处理的请求，包括状态码和耗时。
- `INFO`：启动、重载、关闭，以及内容未命中（`content not found on any upstream`）。内容未命中是正常情况，dockerd 会回退到官方仓库。
- `WARN`：上游无法处理请求所导致的请求失败。
- `ERROR`：重载失败和回滚失败。

## 故障排查

| 现象 | 可能原因与处理方式 |
|---|---|
| `dockerd` 未连接代理 | 执行 `doctor`，检查 `advertise`、`listen` 和防火墙。注意 rootless dockerd 默认无法访问宿主机回环地址。 |
| 每次拉取都慢约 5 秒 | 列表首位上游无响应。检查 `status`，熔断后等待开销会消失。可降低 `timeouts.first_byte` 或启用竞速。 |
| 某上游被跳过 | 熔断器已打开。`status` 可查看状态，经过 `breaker.cooldown` 后会再次探测。 |
| 拉取回退到 Docker Hub | 代理返回了 404 或 502。检查 `status` 和 `WARN` 日志。镜像缺失时回退属于正常行为。 |
| 日志出现 `reload failed` | 配置验证失败，日志会说明问题，运行配置保持不变。 |
| `secret ... not set or empty` | 代理进程环境中缺少 `password` 引用的环境变量，或其值为空。 |
| `file exists and was not created by oci-mirror-proxy` | 文件由其他工具管理。先检查内容，如需接管再使用 `--force` 重试，覆盖前会先备份。 |
| `cannot detect the image store` | 无法通过 `--docker-socket` 访问 dockerd。请启动 dockerd 或显式指定 `--store`。 |
