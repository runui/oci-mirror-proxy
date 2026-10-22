# Docker 部署

[English](../docker.md) | [简体中文](docker.md)

## 环境要求与镜像

支持本机 Linux Docker Engine 或 macOS Docker Desktop 及 Compose 插件。
统一 Compose 使用 bridge 网络，仅发布 `127.0.0.1:24817`。
配置、生效和恢复参见[自动配置 Docker](docker-integration.md)。
rootless 网络可能需要额外配置。

镜像可从项目的 Gitea 容器仓库获取：

```text
git.zmide.com/bin/oci-mirror-proxy:latest
git.zmide.com/bin/oci-mirror-proxy:v1.0.1
git.zmide.com/bin/oci-mirror-proxy:dev
```

镜像支持 `linux/amd64` 和 `linux/arm64`。`latest` 对应稳定的 `v*` 标签，版本标签
也包含预发布版本，`dev` 跟随 `master`。生产环境建议固定版本标签或摘要。
如果软件包是私有的，需先运行 `docker login git.zmide.com`。

Dockerfile 采用多阶段构建，运行镜像基于 `scratch`，仅包含静态 Go 二进制及系统
CA 证书，不含 shell，以 `65532:65532` 用户和组运行。

## 使用 Compose 启动

在项目根目录执行：

```sh
mkdir -p config
cp examples/config.docker.yaml config/config.yaml
# 非 root 容器需要能够原子替换配置文件。
sudo chown -R 65532:65532 config
sudo chmod 700 config
sudo chmod 600 config/config.yaml
docker compose pull
docker compose up -d --no-build
docker compose ps
docker compose logs -f
```

可在 `.env` 中指定镜像，也可导出同名环境变量：

```dotenv
OCI_MIRROR_PROXY_IMAGE=git.zmide.com/bin/oci-mirror-proxy:v1.0.1
```

也可以本地构建，无需拉取已发布的应用镜像：

```sh
OCI_MIRROR_PROXY_IMAGE=oci-mirror-proxy:local docker compose build
OCI_MIRROR_PROXY_IMAGE=oci-mirror-proxy:local docker compose up -d --no-build
```

初始配置有意不预设任何上游镜像源。使用前请添加真实镜像源；列表为空时，dockerd
会回退到官方仓库：

```sh
docker compose exec oci-mirror-proxy oci-mirror-proxy upstream add \
  --id mirror-a --url https://mirror-a.example.com
docker compose exec oci-mirror-proxy oci-mirror-proxy status
curl -fsS http://127.0.0.1:24817/v2/
```

必须挂载**整个配置目录**，而非单个 `config.yaml` 文件：上游管理通过原子重命名
保存配置，因此需要目录写权限。配置变更在重建容器后仍会保留。手动编辑时使用
`sudoedit config/config.yaml`，然后重载：

```sh
docker compose exec oci-mirror-proxy oci-mirror-proxy reload
```

健康检查查询本地管理套接字，仅检查进程是否响应，不检查上游可用性或宿主机 dockerd
配置。管理套接字位于私有 tmpfs 中，不暴露到宿主机。日志输出到 stderr；SIGTERM
会触发优雅关闭。不需要特权模式，也不需要挂载 Docker socket 或宿主机 Docker 配置。

## 配置宿主机 dockerd

启动容器**不会**自动让 Docker 使用代理。按[安装说明](../../README.zh-CN.md#安装)
在宿主机安装 CLI，然后使用同一份配置预览并应用宿主机集成：

```sh
sudo oci-mirror-proxy dockerd plan --config "$PWD/config/config.yaml"
sudo oci-mirror-proxy dockerd apply --config "$PWD/config/config.yaml"
sudo oci-mirror-proxy doctor --config "$PWD/config/config.yaml"
docker pull hello-world
```

这些宿主机命令不需要访问容器内私有的管理套接字。不要再在宿主机启动第二个代理服务；
使用安装脚本时不加 `--service`。手动配置与回滚方法见[运维指南](operations.md#配置-dockerd)。

容器内监听 `0.0.0.0`，宿主机仅发布回环端口，避免将无认证接口暴露到局域网。
同网络容器仍可能访问此接口，不要与不可信容器共享网络。
macOS 上保持配置目录归当前用户所有，主机 CLI 不使用 sudo；需要生效时显式使用 `--restart`。

认证镜像源引用的环境变量需通过 Compose `environment` 显式传入（只写入 `.env`
不会注入容器），也可挂载 Compose secrets 并使用 `file:/run/secrets/...` 引用。
自定义 CA 文件应只读挂载，`tls.ca_file` 使用容器内路径。挂载文件必须允许 UID
65532 读取。不要将密钥写入镜像或构建上下文。

## 更新与停止

```sh
docker compose pull
docker compose up -d --no-build
docker compose down
```

`down` 不会删除 `config/`。若永久移除代理，请先在宿主机执行
`sudo oci-mirror-proxy dockerd revert`，恢复原有 Docker 配置。

## 本地构建

运行测试并构建本地镜像：

```sh
docker build --target test .
docker build -t oci-mirror-proxy:local --build-arg VERSION=dev .
```
