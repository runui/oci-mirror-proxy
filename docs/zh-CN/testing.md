# 测试说明

[English](../testing.md) | [简体中文](testing.md)

## 测试分层

| 层级 | 位置 | 验证内容 |
|---|---|---|
| 单元测试 | 各包中的 `*_test.go` | 隔离验证组件：注入时钟下的熔断状态机、使用预设候选行为的策略结果、令牌解析与缓存、路径解析、TOML 和 JSON 渲染，以及原子 apply/revert 契约。 |
| 模糊测试 | `internal/ociapi`（`FuzzParse`） | 路径解析不会生成逃逸出 `/v2/` 的规范路径。`testdata/fuzz` 中的语料已纳入版本控制。 |
| 协议及端到端测试 | `internal/app/app_test.go` | 真实前端、路由和上游代码通过真实 HTTP 访问内存模拟仓库。覆盖故障转移时序、熔断跳过、竞速、跨上游续传、令牌认证、在线管理变更、持久化和重载回滚。 |
| dockerd 集成测试 | 此处未自动化 | 参见下文手动步骤。 |

执行 `make check` 运行格式检查、vet、启用竞态检测的测试和依赖策略检查。`make fuzz` 会更长时间地运行路径解析模糊测试。

## 模拟仓库

`internal/testutil/fakeregistry` 是内存中的 Registry v2 服务，支持：

- 接受连接但始终不响应，模拟无响应上游。
- 所有请求均返回指定状态码，模拟故障上游。
- 声明完整长度，但在传输 N 字节后截断完整 blob 响应。
- 对 `Range` 请求返回 500。
- 要求 bearer token，并发布 Bearer 质询。

该包仅用于测试支持。它还可作为独立进程 `internal/testutil/fakeregistry/cmd/fakeregistry` 运行，提供示例镜像 `library/hello:latest`，用于手动验证。

## 手动冒烟测试（真实二进制，无需 dockerd）

英文原文记录的二进制端到端验证流程如下：

```sh
make build
D=$(mktemp -d)
dist/oci-mirror-proxy init --config $D/config.yaml
go run ./internal/testutil/fakeregistry/cmd/fakeregistry > $D/fake.out &   # 留意 url= 行
dist/oci-mirror-proxy serve --config $D/config.yaml --admin-socket $D/admin.sock --log-level debug &
dist/oci-mirror-proxy upstream add --admin-socket $D/admin.sock --id fake --url "$(sed -n 's/^url=//p' $D/fake.out)"
curl -i http://127.0.0.1:24817/v2/
curl -i http://127.0.0.1:24817/v2/library/hello/manifests/latest
curl -i -H 'Range: bytes=0-11' "http://127.0.0.1:24817/v2/library/hello/blobs/$(sed -n 's/^blob=//p' $D/fake.out)"
dist/oci-mirror-proxy status --admin-socket $D/admin.sock
```

原文记录的该次验证结果：

- `/v2/` 返回 200，并带有仓库 API 版本响应头。
- manifest 的 GET 和 HEAD 返回 200，并带有 `Docker-Content-Digest` 响应头。
- 范围请求返回 206，且 `Content-Range` 正确。
- 完整 blob 的实际大小与声明大小完全一致。
- 缺失镜像返回 404 `MANIFEST_UNKNOWN`，日志以 info 级别将其归类为内容未命中。
- 放在首位的故障上游连续失败三次后熔断，随后完全跳过（请求计数停在 3），健康上游继续处理所有请求。
- 手动修改配置并发送 SIGHUP 后变更生效，未改变的上游保留计数器。
- 语法损坏的配置在 SIGHUP 重载时被拒绝，代理继续提供服务。
- SIGTERM 使代理正常退出并移除管理套接字。
- 验证了 containerd 后端的 `dockerd apply` 和 `revert`（创建、拒绝覆盖非托管文件、`--force` 备份覆盖、恢复），以及传统后端（合并时保留无关配置项，随后按字节精确恢复）。
- `doctor` 为每个不可达组件给出具体原因，并以退出码 1 结束。

## 尚未验证的范围

- **真实 dockerd 通过代理拉取镜像。** 开发时的沙箱没有运行 Docker daemon，也无法访问 Docker Hub。containerd 的 `hosts.toml` 和传统 `registry-mirrors` 输出语义已对照 moby 源码检查，但未在真实运行的 dockerd 上验证。正式依赖代理前，请在有 dockerd 的 Linux 主机上执行[配置 dockerd 流程](operations.md#配置-dockerd)，然后用 `docker pull` 拉取镜像。
- **Rootless dockerd。** rootless PID 文件位置是假定值，回环可达性依赖网络驱动，详见[运维指南](operations.md#rootless-dockerd)。
- **长期稳定性。** 尚未测量持续数小时的熔断与令牌行为，以及持续负载下的内存使用情况。

## 工具说明

- 开发机器上安装的 `staticcheck` 使用 Go 1.24 构建，无法分析 Go 1.26 模块。在依赖 `make staticcheck` 前，请先用项目工具链构建当前版本的 staticcheck。`check` 目标不包含此检查。
- 开发环境未安装 `golangci-lint`。依赖规则通过 `make deps-check` 检查，底层使用 `go list`，无需额外工具。
