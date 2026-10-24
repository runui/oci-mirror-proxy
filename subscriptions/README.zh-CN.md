# 镜像目录

[English](README.md) | [简体中文](README.zh-CN.md)

[`mirrors.json`](mirrors.json) 是仓库提供的镜像目录；[`schema.json`](schema.json)
描述 JSON 格式并提供编辑器提示。订阅命令见[远端订阅](../apps/docs/content/docs/remotes.zh-CN.mdx)。

## 格式

- `version`：目录格式版本，目前为 `1`。
- `regions`：区域标签及其显示名称。
- `mirrors`：镜像站条目，包含 `name`、可选的 `website`、`regions` 区域标签及
  `endpoints` 中各 registry 的 URL。一个镜像站可以支持多个区域。

## 贡献镜像源

欢迎添加公共镜像站、修正区域或地址、补充运营方使用限制，或反馈失效端点，
也欢迎镜像站运营者提交自己的公共服务。不会修改 JSON 也可以
[提交 Issue](https://github.com/runui/oci-mirror-proxy/issues/new)，提供运营方网站、
各 registry 的地址、适用区域和使用限制，帮助其他用户发现和使用镜像源。

### Fork 并提交 PR

1. [Fork 仓库](https://github.com/runui/oci-mirror-proxy/fork)到自己的 GitHub 账户，
   创建如 `add-example-mirror` 的分支。
2. 在 Fork 中编辑 `subscriptions/mirrors.json`，向 `mirrors` 数组追加条目；
   同一运营方已有条目时直接更新，也可使用 GitHub 网页编辑器，无需搭建开发环境。
3. 提交变更，向 `runui/oci-mirror-proxy:master`
   [发起 PR](https://github.com/runui/oci-mirror-proxy/compare)，附上官方端点文档、
   区域和使用限制说明。反馈连接问题时请提供镜像、所在区域、时间和错误信息，
   不要附带凭据或私有令牌。

在 `mirrors` 数组中追加类似条目，以下域名仅为占位示例：

```json
{
  "name": "Example Public Mirror",
  "website": "https://mirror.example.com/docs",
  "regions": ["cn", "apac"],
  "endpoints": [
    {"registry": "docker.io", "url": "https://docker.mirror.example.com"},
    {"registry": "ghcr.io", "url": "https://ghcr.mirror.example.com"}
  ]
}
```

区域标签需在顶层 `regions` 中声明；确有需要时可同时添加新标签及显示名称。
按服务适用区域选择标签，不要仅根据服务器所在地判断。地址应为公共 Registry v2
基础地址，而非网站首页或某个镜像的完整地址。请提供运营方官方说明，并在本 README
补充白名单、限流、认证要求或 tag 缓存等使用注意事项，不要提交密码、查询令牌或私有端点。

### 本地格式化和校验 JSON

本地格式化工具需要 Node.js 20.19+（20.x）或 22.12+ 以及 npm。在仓库根目录执行：

```sh
npm ci
npm run format
npm run format:check
npm run validate
npm run check
```

格式化命令仅处理 `subscriptions/**/*.json`。`npm run validate` 使用 `schema.json`
校验 `mirrors.json`；`npm run check` 同时执行格式检查和 Schema 校验。
`npm ci` 还会安装 Git pre-commit hook，使用 Oxfmt 检查已暂存的订阅 JSON 文件，
并在订阅 JSON（包括 Schema）变动时，使用暂存的 Schema 校验暂存的目录数据。
hook 不会自动修复或暂存文件。检查失败时，修正报告的错误（格式问题可执行
`npm run format`），检查并暂存修正后的文件，再重新提交。
构建和运行 Go 应用不需要 Node.js。

### 订阅自己的 Fork

无需等待 PR 合并，也可以直接订阅自己的目录。提交并推送 `mirrors.json` 后，
使用其原始文件 URL；将 `YOUR-USER`、`YOUR-BRANCH` 替换为自己的 GitHub 账户和分支名：

```sh
oci-mirror-proxy remote update \
  --url https://github.com/YOUR-USER/oci-mirror-proxy/raw/refs/heads/YOUR-BRANCH/subscriptions/mirrors.json \
  --region cn --dry-run

oci-mirror-proxy remote update \
  --url https://github.com/YOUR-USER/oci-mirror-proxy/raw/refs/heads/YOUR-BRANCH/subscriptions/mirrors.json \
  --region cn
```

请选择目录中存在的区域。每个 URL 都是独立订阅，添加 Fork 不会替换已有源。
如果只想使用自己的目录，可先执行 `remote list`，再用
`remote remove --url OLD-CATALOG-URL` 删除旧源。后续修改前，可通过 GitHub 的
**Sync fork** 同步上游变更。

## 已收录镜像站

### DaoCloud Public Image Mirror

- 官方文档：https://github.com/DaoCloud/public-image-mirror
- Registry 端点：`docker.m.daocloud.io`、`ghcr.m.daocloud.io`、
  `gcr.m.daocloud.io`、`k8s.m.daocloud.io`、`quay.m.daocloud.io`。
- 运营方存在白名单、限流、拥挤和同步期间可变 tag 旧缓存等限制，
  公共镜像覆盖范围有限，使用前请阅读官方说明，并优先使用固定摘要。
- 区域：`cn`、`apac`、`eu`、`na`。

### ZimaOS Image Mirror

- 运营方网站：https://www.zimaos.com
- 分别代理 Docker Hub、GCR、Quay 和 GHCR：`storeproxy.zimaos.com`、
  `gcr.zimaos.com`、`quay.zimaos.com`、`ghcr.zimaos.com`。
- 区域：`cn`、`apac`、`eu`、`na`。

### 1ms Image Mirror

- 运营方网站：https://1ms.run
- 分别代理 Docker Hub、GCR、Quay 和 GHCR：`docker.1ms.run`、
  `gcr.1ms.run`、`quay.1ms.run`、`ghcr.1ms.run`。
- 区域：`cn`、`apac`、`eu`、`na`。

### 1Panel Docker Hub Mirror

- 运营方网站：https://1panel.cn
- Registry 端点：`docker.1panel.live`。
- 区域：`cn`，仅限中国大陆访问。

## 项目贡献者

感谢每一位完善项目和镜像目录的贡献者！

[![项目贡献者](https://contrib.rocks/image?repo=runui/oci-mirror-proxy)](https://github.com/runui/oci-mirror-proxy/graphs/contributors)

[目录修改记录](https://github.com/runui/oci-mirror-proxy/commits/master/subscriptions/mirrors.json)
