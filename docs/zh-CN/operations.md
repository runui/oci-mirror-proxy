# 运维指南

[English](../operations.md) | [简体中文](operations.md)

## 命令

使用 `remote show [--url U] [--region cn]` 查看远端目录，无须先订阅。
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
| `dockerd apply [flags] [--no-reload]` | 写入 dockerd 配置，并按需向 dockerd 发送信号。 |
| `dockerd revert [flags] [--no-reload]` | 从当前配置移除指定代理。 |
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

`--store auto` 会向运行中的 dockerd 查询所用镜像存储后端。dockerd 未运行时，请传入 `--store containerd` 或 `--store legacy`。

## 配置 dockerd

### Windows 与 Docker Desktop

Windows 安装脚本会启动代理服务并配置开机自启。在管理员 PowerShell 中使用：

```powershell
$proxy = "$env:ProgramFiles\oci-mirror-proxy\bin\oci-mirror-proxy.exe"
$config = "$env:ProgramData\oci-mirror-proxy\config.yaml"
Get-Service oci-mirror-proxy
& $proxy status --config $config --admin-socket '\\.\pipe\oci-mirror-proxy-admin'
& $proxy remote update --config $config --admin-socket '\\.\pipe\oci-mirror-proxy-admin' --region cn
# 编辑 config.yaml 后：
& $proxy reload --config $config --admin-socket '\\.\pipe\oci-mirror-proxy-admin'
# 修改 listen 后：
Restart-Service oci-mirror-proxy
# 停止 / 启动代理：
Stop-Service oci-mirror-proxy
Start-Service oci-mirror-proxy
```

服务配置位于 `%ProgramData%\oci-mirror-proxy\config.yaml`，日志位于
`%ProgramData%\oci-mirror-proxy\logs`。使用自定义服务名时，请替换命令中的服务名和管道名。
卸载服务时先停止服务，再运行
`& "$env:ProgramFiles\oci-mirror-proxy\service\oci-mirror-proxy.exe" uninstall`；
配置和日志会保留。

普通用户也可运行系统安装的程序，使用上述 `$proxy` 完整路径，或打开新终端直接运行 `oci-mirror-proxy`。
安装器会自动将程序加入 PATH。
在普通 PowerShell 中运行 `init` 和 `serve` 即使用自己的配置，不要传入系统服务的
`--config` 或管理管道。如果服务已占用 24817 端口，启动第二个实例前请修改用户配置的
`listen` 和 `advertise`。用户配置位于 `%APPDATA%\oci-mirror-proxy\config.yaml`。
只有服务管理操作需要管理员 PowerShell。

自动接入 Docker Desktop 时请选择本地 Linux 容器上下文，不要指定 `--store`。
`dockerd plan/apply/revert` 合并 `%USERPROFILE%\.docker\daemon.json` 并保留其他设置，
备份保存在 `%APPDATA%\oci-mirror-proxy\backups`。只有显式指定 `--restart` 才会
重启 Docker，也可在 Desktop 界面应用设置。自动修改拒绝远端引擎、自定义 `DOCKER_HOST`、
`DOCKER_CONFIG` 及 Windows 容器引擎。

```powershell
& $proxy dockerd plan --config $config
& $proxy dockerd apply --config $config
# 手动重启 Docker Desktop，或使用 apply --restart 允许自动重启。
& $proxy doctor --config $config
# 撤销配置修改：
& $proxy dockerd revert --config $config
```

显式 `--store` 离线模式使用 `%ProgramData%\docker` 下的路径，需手动激活。
Windows 引擎不支持 `--rootless`、`--pid-file`。

### Linux Docker Engine

1. 确保代理已运行，且 dockerd 能通过预期地址访问它。`doctor` 会检查这一点。
2. 执行 `dockerd plan`，检查列出的文件。此时不会写入任何内容。
3. 执行 `dockerd apply`。有变更才备份并原子写入，已有配置按内容合并，不依赖托管记录。
4. 传统存储后端需要重载时，会向 dockerd 发送 `SIGHUP`。containerd 后端每次解析都会读取 `hosts.toml`，无需发送信号。
5. 执行 `doctor`，所有检查都应通过。

### 写入的内容

**containerd 存储后端**：为每个仓库写入 `<certs-dir>/<alias>/hosts.toml`：

```toml
server = "https://registry-1.docker.io"

[host."http://127.0.0.1:24817"]
  capabilities = ["pull", "resolve", "referrers"]
```

containerd 先尝试 `[host]` 条目，最后尝试 `server`，因此官方仓库仍是回退目标。对于使用路径前缀的仓库（例如 `ghcr.io`），会生成 `[host."http://127.0.0.1:24817/ghcr.io"]`。

**传统存储后端**：在 `daemon.json` 的 `registry-mirrors` 中将代理放在首位。其他条目保留为回退地址，其他所有配置项保持不变。

传统后端仅支持 `docker.io` 镜像源。对于其他仓库，`plan`、`apply` 和 `doctor` 会给出警告并跳过其 dockerd 镜像源配置，但不改变代理自身的路由。Docker Hub 配置仍会继续。如果未配置任何受支持的仓库，`plan` 和 `apply` 会成功退出，不写文件、不修改已有 apply 状态，也不向 dockerd 发送信号。

### 还原

`dockerd revert` 仅从当前文件中移除目标代理条目，保留其他设置及后续修改，不恢复历史快照、不删除配置目录。代理地址从所选服务配置中取得。containerd 移除操作也检查已不在订阅中的 registry 目录。备份仅供手动恢复，不参与配置判断。

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
| `cannot detect the image store` | 无法通过 `--docker-socket` 访问 dockerd。请启动 dockerd 或显式指定 `--store`。 |

备份存放在当前用户的平台应用目录下的 `backups/`。安装和更新使用同一套安全配置合并逻辑。

客户端命令通过运行中服务的管理 API 获取配置，不再读取服务配置文件。非默认管理端点使用 `--admin-socket` 指定。`serve` 和 `init` 支持通过 `--config` 指定配置文件。
