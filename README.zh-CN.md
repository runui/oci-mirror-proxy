# oci-mirror-proxy

[English](README.md) | [简体中文](README.zh-CN.md)

为 dockerd 提供的本地只读镜像仓库代理。它位于 dockerd 与用户选择的一个或多个 Registry v2 镜像源之间，补充 dockerd 内置镜像源机制所缺少的能力：

- **运行时管理。** 无需重启 dockerd，即可添加、移除和禁用上游镜像源。
- **故障隔离。** 无响应或故障镜像源只会消耗一次超时等待，随后熔断器将其移出候选列表，直到探测成功。
- **可选竞速。** 通过对冲请求，使用最先返回有效响应的镜像源。
- **镜像层断点续传。** 传输中断后，可从同一字节偏移量切换到另一个镜像源继续下载，摘要仍由 daemon 校验。
- **安全回退。** 代理无法处理请求时，dockerd 会回退到官方仓库，因此代理不会让原本可用的镜像拉取变得不可用。

支持 dockerd 的两种镜像存储后端：containerd（`hosts.toml`）和传统 graphdriver（`daemon.json`，仅限 docker.io）。

## 预览

本次预览中通过本项目耗时 **508.0 秒**，
直接使用镜像站耗时 **740.3 秒**：速度约为 **1.46 倍**，总耗时减少 **31.4%**。

| 直接使用镜像站 · 740.3 秒 | 通过 oci-mirror-proxy · 508.0 秒 |
| :---: | :---: |
| <img src="docs/demos/direct-mirror.gif" alt="直接拉取" width="552" height="363"> | <img src="docs/demos/oci-mirror-proxy.gif" alt="通过代理拉取" width="552" height="363"> |
| [asciinema 录制](docs/demos/direct-mirror.cast) | [asciinema 录制](docs/demos/oci-mirror-proxy.cast) |

GIF 为加速预览，原始录制保留真实时间。

## 项目状态

支持的功能：用于拉取的仓库 API、故障转移、熔断、对冲请求、blob 断点续传、令牌和 Basic 认证、管理套接字、配置重载，以及支持还原的 dockerd 配置管理。

暂不支持：推送、本地 blob 缓存、密钥链后端和指标导出。

## 环境要求

- 构建需要 Go 1.26 或更新版本。
- 支持本机 Linux Docker Engine 或 macOS/Windows Docker Desktop（Linux 容器模式）。对于 rootless dockerd，操作者需要确保其网络命名空间能够访问代理监听地址，详见[运维指南](docs/zh-CN/operations.md#rootless-dockerd)。

## 安装

无需克隆仓库，即可安装最新发布版本：

```sh
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | sh -s -- --prefix "$HOME/.local"
export PATH="$HOME/.local/bin:$PATH"
oci-mirror-proxy version
```

更新已有安装：

```sh
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | sh -s -- --update --prefix "$HOME/.local"
```

在 Linux 上，以 root 权限安装并启动系统服务：

```sh
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | sudo sh -s -- --service
# 更新系统安装并重启服务：
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | sudo sh -s -- --update --prefix /usr/local --service
```

脚本会下载对应的 Linux、macOS 或 FreeBSD 二进制文件，并校验 SHA256。当没有匹配的二进制文件，或指定了 `--build` 时，会使用 Go 1.26.8 或更新版本（也可通过 Go 工具链自动下载）构建发布源码。使用 `--version v1.0.1` 可选择指定版本。

预编译程序支持以下平台：Linux 支持 `amd64`、`arm64`、`armv7`、`riscv64`；macOS、FreeBSD 和 Windows 支持 `amd64`、`arm64`。Windows 使用 `.zip`，其他系统使用 `.tar.gz`。

Windows 用户可下载并检查 PowerShell 安装脚本后，在 PowerShell 中执行，需要时确认 Windows 管理员授权提示：

```powershell
Invoke-WebRequest https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.ps1 -OutFile install.ps1
.\install.ps1
# 更新安装并重启服务：
.\install.ps1 -Update
```

安装器会启动 Windows 后台服务、设置开机自启并自动加入 PATH。
打开新终端后即可直接运行 `oci-mirror-proxy`；更新会保留已有配置。需要 .NET Framework 4.x。

服务管理和 Docker Desktop 接入见[运维指南](docs/zh-CN/operations.md)。
其他安装选项可运行 `Get-Help .\install.ps1 -Full` 查看。

`--service` 选项支持 Linux 的 systemd/OpenRC 和 macOS 的 launchd 系统服务。
不带 `--service` 的 sudo 安装会以经过验证的原用户身份初始化用户配置。
服务管理与历史权限问题的修复参见 [macOS 安装指南](docs/macos-installation.md)。

如需使用下载镜像，将 `DOWNLOAD_BASE_URL` 设为镜像仓库根 URL（需保持相同的 `/releases` 和 `/archive` 布局）。通过管道执行脚本时，应将变量传给 `sh`，而不只是传给 `curl`：

```sh
curl -fsSL https://git.zmide.com/bin/oci-mirror-proxy/raw/branch/master/install.sh | DOWNLOAD_BASE_URL=https://mirror.example.com/bin/oci-mirror-proxy sh -s -- --prefix "$HOME/.local"
```

默认安装目录为 `/usr/local`，需要写入权限。现有配置会保留；不使用 `--service` 时，不会初始化配置、安装或重启服务。脚本不会修改或重启 Docker。不带 `--service` 更新后，需要手动重启正在运行的代理。

## Docker 部署

Docker Engine 和 macOS Docker Desktop 用户可通过 [docker-compose.yaml](docker-compose.yaml) 部署。准备好可写的
配置目录后，执行 `docker compose up -d --no-build`。镜像支持 amd64/arm64，默认
采用 bridge 网络并仅向主机回环地址发布端口，无需挂载 Docker socket。配置初始化、本地构建、
宿主机 dockerd 接入方法见 [Docker 部署指南](docs/zh-CN/docker.md)。

## 快速开始

```sh
make build                                   # 生成 dist/oci-mirror-proxy

dist/oci-mirror-proxy init                    # 写入用户配置文件
dist/oci-mirror-proxy serve &                 # 在后台运行代理

dist/oci-mirror-proxy upstream add \
  --id mirror-a --url https://mirror-a.example.com

sudo dist/oci-mirror-proxy dockerd plan       # 预览变更，不写入任何文件
sudo dist/oci-mirror-proxy dockerd apply      # 写入 dockerd 配置
dist/oci-mirror-proxy doctor                  # 检查代理、dockerd 和配置
```

[examples/config.example.yaml](examples/config.example.yaml) 提供了完整配置及各选项说明；中文字段说明见[配置参考](docs/zh-CN/configuration.md)。

## 文档

### 按区域订阅镜像目录

```sh
oci-mirror-proxy remote update --region cn
oci-mirror-proxy remote list
oci-mirror-proxy remote show --url https://example.com/mirrors.json --region cn
oci-mirror-proxy remote update --all
```

CLI 默认更新仓库的 JSON 目录，也支持自定义 `--url`。每个源保存到独立 `.d` 片段，
最终按 URL 去重，更新采用原子写入并备份上一次结果。
重载不会取消已有 HTTP 拉取或改变其续传候选。
详见[远端订阅](docs/zh-CN/remotes.md)与[镜像目录说明](subscriptions/README.zh-CN.md)。

- [Docker 部署](docs/zh-CN/docker.md)：Docker/Compose 部署及本地构建。
- [配置参考](docs/zh-CN/configuration.md)：所有配置项及其默认值。
- [运维指南](docs/zh-CN/operations.md)：命令、dockerd 集成、重载和故障排查。
- [架构说明](docs/zh-CN/architecture.md)：组件、请求流程和故障处理。
- [测试说明](docs/zh-CN/testing.md)：运行检查和本地冒烟测试。

## 安全注意事项

- 仓库 API 不提供认证，因此默认监听 `127.0.0.1`。将 `listen` 设为非回环地址会向网络暴露未经认证的镜像拉取接口。
- 凭据不会直接写入配置文件：密码使用 `env:NAME` 或 `file:/path` 等引用。日志和 `%v` 输出中的敏感信息会被脱敏。
- 客户端的 `Authorization` 请求头不会被转发到上游。
- 管理套接字通过文件权限保护：目录权限为 `0700`，套接字权限为 `0600`。
- `dockerd apply` 写入文件前会先备份，`revert` 会恢复备份。除非指定 `--force`，否则不会覆盖非本工具创建的文件。

自动配置、生效及撤销参见[自动配置 Docker](docs/zh-CN/docker-integration.md)。
