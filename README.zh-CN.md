# oci-mirror-proxy

[English](README.md) | [简体中文](README.zh-CN.md)

为 dockerd 提供的本地只读镜像仓库代理。支持运行时管理镜像源、熔断隔离故障、
可选对冲竞速，以及切换镜像源续传中断的镜像层。Docker 仍可回退到官方仓库。

支持 Linux Docker Engine 和 macOS/Windows Docker Desktop（Linux 容器模式），
兼容 containerd 和传统 graphdriver 镜像存储。暂不支持推送和本地 blob 缓存。

## 快速开始

无需克隆仓库，即可安装最新发布版本：

```sh
curl -fsSL https://ocimp.zmide.com/install.sh | sh
oci-mirror-proxy version
```

默认安装到 `/usr/local/bin`，需要时会请求 sudo。

Linux 用户可安装系统服务并自动接入本机 Docker：

```sh
curl -fsSL https://ocimp.zmide.com/install.sh | sudo sh -s -- --service
```

Windows、macOS 服务、更新、卸载和源码构建（需要 Go 1.26 或更新版本）见[安装指南](apps/docs/content/docs/installation.zh-CN.mdx)。
容器部署见 [Docker Compose](apps/docs/content/docs/docker.zh-CN.mdx)。
仓库 API 不提供认证，默认仅监听回环地址；暴露接口前请阅读[安全注意事项](apps/docs/content/docs/index.zh-CN.mdx#安全注意事项)。

## 预览

本次预览中通过本项目耗时 **508.0 秒**，
直接使用镜像站耗时 **740.3 秒**：速度约为 **1.46 倍**，总耗时减少 **31.4%**。

| 直接使用镜像站 · 740.3 秒 | 通过 oci-mirror-proxy · 508.0 秒 |
| :---: | :---: |
| <img src="docs/demos/direct-mirror.gif" alt="直接拉取" width="552" height="363"> | <img src="docs/demos/oci-mirror-proxy.gif" alt="通过代理拉取" width="552" height="363"> |
| [asciinema 录制](docs/demos/direct-mirror.cast) | [asciinema 录制](docs/demos/oci-mirror-proxy.cast) |

GIF 为加速预览，原始录制保留真实时间。

## 文档

访问[文档站](https://ocimp.zmide.com/zh-CN/docs/)。站点位于 [apps/docs](apps/docs/README.md)，独立于代理部署。
中英文正文统一维护在 [`apps/docs/content/docs`](apps/docs/content/docs)。

- [概览与环境要求](apps/docs/content/docs/index.zh-CN.mdx)
- [安装](apps/docs/content/docs/installation.zh-CN.mdx)
- [自动配置 Docker](apps/docs/content/docs/docker-integration.zh-CN.mdx)
- [远端镜像订阅](apps/docs/content/docs/remotes.zh-CN.mdx)与[镜像目录说明](subscriptions/README.zh-CN.md)
- [配置参考](apps/docs/content/docs/configuration.zh-CN.mdx)
- [运维与故障排查](apps/docs/content/docs/operations.zh-CN.mdx)
- [架构说明](apps/docs/content/docs/architecture.zh-CN.mdx)
- [测试说明](apps/docs/content/docs/testing.zh-CN.mdx)

[examples/config.example.yaml](examples/config.example.yaml) 提供了完整配置及各选项说明。
