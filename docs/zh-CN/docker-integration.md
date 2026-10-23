# 自动配置 Docker

[English](../docker-integration.md) | [简体中文](docker-integration.md)

本机 Linux Docker Engine 与 macOS Docker Desktop 使用相同命令，无需选择平台：

```sh
oci-mirror-proxy dockerd plan --config config/config.yaml
oci-mirror-proxy dockerd apply --config config/config.yaml
oci-mirror-proxy doctor --config config/config.yaml
oci-mirror-proxy dockerd revert
```

程序根据当前 Docker 连接自动识别环境。SSH/TCP 远程连接不会触发本机配置写入。
macOS 下以普通用户运行主机 CLI，不使用 sudo，并确保 PATH 中有 docker。
标准 Compose 使用 bridge 网络，仅将端口发布到主机 loopback；配置需要挂载整个可写目录。
同网络中的容器仍可能访问无认证的 Registry API，避免与不可信容器共享网络。

macOS 集成会备份并合并 `~/.docker/daemon.json`，保留其他字段和 mirror，将代理放到首位。
支持 Docker Hub，不会自动配置其他 Registry，也不要求关闭 containerd image store。

需要重启才能生效时，默认只保存配置并报告待生效。明确允许中断后执行：

```sh
oci-mirror-proxy dockerd apply --config config/config.yaml --restart
```

需要 Docker CLI 支持 `docker desktop restart`；激活失败不会被报告为成功。
`doctor` 同时检查文件与 daemon 已加载的 mirror。Linux 保留原有配置方式和权限要求。

移除代理前先撤销；需要重启时附加 `--restart`：

```sh
oci-mirror-proxy dockerd revert --restart
```

撤销仅移除当前配置中的目标代理条目，保留其他设置和后续修改，不恢复历史快照。服务地址通过现有默认配置发现流程获取。旧状态文件完全不参与判断。

只有实际变更才备份并原子替换，重复操作不重写文件。无效配置及操作期间检测到的外部修改会导致报错，`--force` 不绕过校验。每个目标文件使用独立操作锁；外部编辑器若不遵守锁，仍可能与最终替换竞争。多文件写入不是整体事务，失败时报告已修改文件及备份位置。
进程崩溃可能留下操作锁，确认无其他配置操作后才能移除。重启可能改变默认网络 ID 和日志，
配置恢复不等于虚拟机磁盘快照回滚。

已在 Apple Silicon、Docker Desktop 4.95.0、Engine 29.8.2、containerd image store 上验证
自动配置、重启、普通 Docker Hub 镜像名经受控上游下载真实层、诊断、重复应用及配置恢复（历史版本行为）。
不据此承诺所有 Desktop 版本、其他 macOS 引擎或 BuildKit 的兼容性。
