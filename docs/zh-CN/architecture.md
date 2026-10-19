# 架构说明

[English](../architecture.md) | [简体中文](architecture.md)

## 组件

```text
cmd/oci-mirror-proxy        程序入口，调用 internal/cli
internal/cli               命令、参数解析、输出格式化
internal/app               组装入口，持有配置状态、上游池和路由表
internal/frontend          面向 dockerd 的 Registry v2 HTTP 接口
internal/route             分发器、中间件链、原子替换的路由表
internal/middleware        日志、blob 断点续传
internal/scheduler         顺序和对冲策略，以及 Candidate 契约
internal/upstream          单个上游端点、熔断器、传输层，以及支持协调更新的上游池
internal/auth              各上游的认证器：无认证、Basic、令牌质询流程
internal/secret            敏感信息引用与脱敏
internal/config            配置模型、默认值、验证和文件读写
internal/ociapi            仓库路径解析，叶子包
internal/admin             Unix 套接字上的管理 API 及客户端
internal/daemonconf        dockerd 集成：检测、纯渲染、事务式应用
internal/unixhttp          Unix 套接字上的 HTTP，由 admin 和 daemonconf 共用
internal/fsutil            原子文件写入
internal/cache             blob 缓存的预留扩展点（仅接口）
```

## 依赖规则

- 叶子包不导入其他内部包：`ociapi`、`secret`、`scheduler`、`fsutil`、`unixhttp`、`daemonconf`（接收 `Target` 值，而非配置模型）。
- 接口由使用方定义。例如，`route` 依赖 `CandidateSource` 接口，而非具体上游池；`frontend` 依赖 `Routes` 接口。
- 只有 `app` 和 `cli` 了解完整依赖图。

## 请求流程

1. dockerd 发送 `GET /v2/library/nginx/manifests/latest`。
2. `frontend` 使用 `ociapi` 解析路径，得到别名（`docker.io`）、类型（manifest）、仓库和引用。不符合仓库路径语法的请求返回 404，仅接受 GET 和 HEAD。
3. `frontend` 在路由表中查找别名，查找过程无锁。
4. 执行分发器链：先日志，再 blob 断点续传，最后是路由。
5. 路由从上游池获取已启用上游的快照，然后调用调度策略。
6. 策略尝试上游。每次尝试经过 `Endpoint.Attempt`：检查熔断器、发送请求、应用首字节和总超时、处理至多一次认证质询，并记录结果。
7. 返回第一个可接受的响应（2xx），按白名单复制响应头，并流式传输响应体。

## 故障处理

| 事件 | 处理方式 |
|---|---|
| 连接被拒绝或上游无响应 | 记为失败并尝试下一个上游，等待受首字节超时限制。 |
| 5xx 或 429 | 记为失败。 |
| 带 Bearer 质询的 401 | 获取令牌并重试一次。再次收到 401 时返回该结果，将上游视为可达但无法提供内容。 |
| 404 | 不视为健康问题，继续尝试下一个上游。若所有已尝试上游均返回 404，则代理返回 404，由 dockerd 回退。 |
| 熔断器打开 | 直接跳过，不发起网络请求。 |
| blob 传输中断 | 如果另一个上游返回 `206`，且 `Content-Range` 起点与当前偏移量一致，则从该位置继续下载；否则向调用方暴露原始错误。每次下载最多续传三次。 |
| 客户端断开 | 取消请求上下文，停止策略执行，不再写入。 |
| 所有上游失败，且并非全部为 404 | 返回 502 `UNAVAILABLE`，由 dockerd 回退。 |

## 调度策略

**顺序策略**（默认）：按配置顺序逐个尝试上游，只有当前上游失败后才联系下一个。

**对冲策略**（需显式启用）：立即请求第一个上游。每经过 `race.delay` 仍未收到响应，就启动另一个上游请求，同时进行的请求数不超过 `race.max_parallel`。某次尝试失败时立即启动下一次尝试。第一个可接受的响应胜出，其他尝试被取消。未胜出的响应在后台被读完并关闭，避免连接泄漏。

## 协调更新

无论通过管理 API、CLI 还是文件重载修改上游集合，都经过 `Pool.Reconcile`：

- 定义未改变的上游保留熔断状态和计数器。
- 修改过或新建的上游会重新构建，使用独立的传输层和认证器。
- 修改熔断配置会重建所有上游，因为熔断器持有阈值。
- 构建失败时，上游池保持不变。
- 被移除上游的空闲连接会被关闭。

服务内部通过互斥锁串行处理配置变更。先应用到上游池，再持久化；任一步失败都会重新应用旧配置。请求处理不获取该互斥锁。

## 并发模型

- 路由表使用指向不可变映射的 `atomic.Pointer`，查找不会阻塞。
- 上游池原子发布端点切片。读取方获取快照，因此协调更新不会改变正在执行的请求。
- 每个端点拥有独立的 `http.Transport`，上游之间的 TLS 设置和连接池相互隔离。
- 熔断器和令牌缓存状态由互斥锁保护，计数器使用原子操作。

## 安全边界

- 数据平面不提供认证，默认监听回环地址。
- 管理 API 只能通过受文件权限保护的套接字访问。
- 上游凭据仅用于对应上游。客户端的 `Authorization` 在前端被丢弃。跨主机重定向会移除 `Authorization`，这是 Go `net/http` 的标准行为。
- 内容完整性端到端保证：dockerd 校验每个摘要。故障上游可能导致拉取失败，但无法使内容错误的镜像被接受。

## 扩展点

| 扩展 | 方式 |
|---|---|
| 新认证方式 | 实现 `auth.Authorizer`，并在 `auth.New` 中增加分支。 |
| 新敏感信息后端（如密钥链） | 实现 `secret.Provider` 并注册到解析器，无需修改配置包。 |
| 新调度策略 | 实现 `scheduler.Strategy`，再在 `app.dispatcher` 中选择它。 |
| 新中间件 | 实现 `route.Middleware`，并在 `app.New` 中加入处理链。 |
| blob 缓存 | 实现 `cache.Store`，用查询缓存的中间件包装路由，数据路径不变。 |
| 其他仓库别名 | 添加 `registries` 条目。路由、hosts.toml 输出和 daemon 镜像源逻辑已支持别名。 |
