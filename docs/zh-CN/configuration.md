# 配置参考

[English](../configuration.md) | [简体中文](configuration.md)

配置文件采用 YAML 格式。不支持的字段会被拒绝，因此拼错的选项会在启动时报错，而不是被静默忽略。可以手动编辑文件，也可以使用 CLI 修改；CLI 会在保存前验证每次变更。

时长使用 Go 语法，例如 `500ms`、`5s`、`1m30s`。

## 顶层配置

| 配置项 | 默认值 | 说明 |
|---|---|---|
| `version` | `1` | 配置格式版本，仅接受 `1`。 |
| `listen` | `127.0.0.1:24817` | 仓库 API 的监听地址。 |
| `advertise` | 自动推导 | dockerd 连接代理时使用的 `host:port`。当 `listen` 使用通配地址时，实际部署应显式设置此项，且必须指定具体主机。 |
| `admin.socket` | 用户级默认路径 | 管理套接字路径。默认为 `$XDG_RUNTIME_DIR/oci-mirror-proxy/admin.sock`；未设置 `XDG_RUNTIME_DIR` 时使用用户缓存目录。 |
| `registries` | `docker.io` | 仓库别名到仓库配置的映射。 |

设置了 `advertise` 时，dockerd 使用该地址；否则使用 `listen`，其中通配主机地址（`0.0.0.0`、`::`）会映射为 `127.0.0.1`。

## 仓库

`registries` 下的每个键都是仓库别名：

- `docker.io` 使用 dockerd 期望的根路径 `/v2/`。
- 其他别名（例如 `ghcr.io`）使用带前缀的路径 `/ghcr.io/v2/`。别名必须是不带端口的主机名，`v2` 为保留名称。

| 配置项 | 默认值 | 说明 |
|---|---|---|
| `origin` | `docker.io` 为 `https://registry-1.docker.io`，其他为 `https://<alias>` | 官方仓库地址。代理无法处理请求时，dockerd 回退至此。 |
| `race.enabled` | `false` | 为此仓库启用对冲请求。 |
| `race.delay` | `500ms` | 启用竞速后，在此时间内未收到响应则启动另一个上游请求。必须为正值。 |
| `race.max_parallel` | `2` | 每个请求的最大并发尝试数。启用竞速时至少为 2。 |
| `breaker.threshold` | `3` | 连续失败多少次后跳过该上游。 |
| `breaker.cooldown` | `30s` | 上游被跳过后，等待多久再允许一次探测请求。 |
| `upstreams` | `[]` | 有序上游列表，排在前面的优先尝试。 |

竞速会向上游发送额外请求。Docker Hub 有拉取频率限制，因此默认关闭竞速，请按需启用。

## 上游镜像源

| 配置项 | 默认值 | 说明 |
|---|---|---|
| `id` | 必填 | 在仓库内唯一，由 1 至 63 个字母、数字、`-` 或 `_` 组成。 |
| `url` | 必填 | Registry v2 服务的基础 URL。支持 `http` 或 `https`，不得包含查询参数、片段或凭据，允许 `/hub` 等基础路径。 |
| `disabled` | `false` | 在文件中保留该上游，但不参与请求调度。 |
| `timeouts.connect` | `10s` | 建立连接的最长时间，包括 TCP 连接和 TLS 握手。慢速或经过代理的网络可适当增大。 |
| `timeouts.first_byte` | `5s` | 等待响应头的最长时间，也是遇到无响应上游时的等待开销。 |
| `timeouts.overall` | `60s` | 元数据请求（manifest、标签列表和 referrers）的总时限，不限制 blob 传输。 |
| `auth.type` | `none` | `none`、`basic` 或 `token`，见下文。 |
| `auth.username` | | `basic` 的用户名，或令牌服务要求认证时 `token` 使用的用户名。 |
| `auth.password` | | 敏感信息引用，例如 `env:MIRROR_PASSWORD` 或 `file:/run/secrets/mirror`。不接受明文密码。 |
| `tls.ca_file` | | 此上游的额外 CA 证书文件，会追加到系统根证书集合。 |
| `tls.insecure_skip_verify` | `false` | 禁用此上游的证书校验。除测试外应避免使用。 |

### 认证类型

- **`none`**：请求不携带凭据。
- **`basic`**：每次请求都通过 HTTP Basic 认证发送 `username` 和 `password`。
- **`token`**：上游返回 `WWW-Authenticate: Bearer` 质询后，代理向其中指定的 realm 请求令牌，按 scope 缓存至即将过期，并重试一次请求。仅当令牌服务要求时才设置 `username` 和 `password`。

凭据仅用于其所属上游，不会发送给其他上游，也不会转发客户端自身的 `Authorization` 请求头。

### 敏感信息引用

`password` 接受引用，而非密码本身：

| 引用 | 解析结果 |
|---|---|
| `env:NAME` | 环境变量 `NAME` 的值，必须已设置且非空。 |
| `file:/path` | 文件内容，移除末尾换行。 |

引用在构建上游时解析，因此缺失环境变量会在启动或重载时报错，而不是等到第一次请求时才报错。

## 默认超时的实际效果

使用默认配置时，列表首位的无响应上游会让每次请求等待约 5 秒，直到触发熔断。连续三次失败后，该上游会被跳过 30 秒，然后进行一次探测。启用竞速并使用默认参数时，无响应上游带来的等待约为 500 毫秒，因为到达此延迟后会启动下一个上游请求。

## 修改配置

- **通过 CLI 或管理 API**：变更经过验证后，无需重启即可应用到运行中的代理，然后原子写入配置文件。验证失败时，运行状态和文件均保持不变。
- **手动编辑**：修改文件后，执行 `oci-mirror-proxy reload` 或向进程发送 `SIGHUP`。验证失败的文件会被拒绝，继续使用原有运行配置。
- **监听地址**：修改 `listen` 后会记录日志，重启后才生效。
