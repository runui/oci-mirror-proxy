# 测试说明

[English](../testing.md) | [简体中文](testing.md)

## 运行检查

执行 `make check` 运行格式检查、vet 和测试（包含竞态检测）。使用 `make fuzz`
运行路径解析模糊测试，也可单独运行配置和目录的模糊测试：

```sh
go test ./internal/config -run '^$' -fuzz FuzzDocumentMerge -fuzztime 30s
go test ./internal/cli -run '^$' -fuzz FuzzRemoteJSON -fuzztime 30s
```

如需静态分析，请安装与项目 Go 工具链兼容的 `staticcheck`，再运行 `make staticcheck`。

## 手动冒烟测试（真实二进制，无需 dockerd）

独立运行的模拟仓库提供 `library/hello:latest`，可在没有 Docker 的情况下进行本地检查。
启动后，等待 `$D/fake.out` 出现 `url=` 和 `blob=` 行，再执行客户端命令：

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

## 检查 dockerd 集成

在运行 dockerd 的 Linux 主机上，按[配置 dockerd](operations.md#配置-dockerd) 完成接入后，
执行 `docker pull hello-world`。Rootless 网络配置见
[Rootless dockerd](operations.md#rootless-dockerd)。
