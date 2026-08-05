# 宝塔面板部署教程

本文档说明如何在宝塔面板的 Docker 功能中部署 **NEW API Ultra 发布快照**。
发布仓库的完整、可复现路径仍以根目录 [`DEPLOYMENT.md`](../../DEPLOYMENT.md)
为准；本页不要直接复制上游镜像或未配置密钥的单容器命令。

> 原始 New API/QuantumNous 项目身份和官方文档链接保留如下，供需要时对照：
> [宝塔面板部署](https://docs.newapi.pro/zh/docs/installation/deployment-methods/bt-docker-installation)

## 前置要求

| 项目 | 要求 |
| --- | --- |
| 宝塔面板 | ≥ 9.2.0 |
| 操作系统 | CentOS 7+、Ubuntu 18.04+ 或 Debian 10+ |
| 资源 | 至少 1 核、2 GB 内存（实际需求取决于数据库和上游流量） |
| Docker | Docker Engine 与 Compose v2 |
| 密钥工具 | `openssl`，用于生成稳定的部署密钥 |
| 检查工具 | `curl`，用于等待应用健康接口就绪 |

## 方法一：从发布仓库构建（推荐）

1. 在宝塔面板安装并启动 Docker。
2. 创建网站目录，例如 `/www/wwwroot/new-api-ultra`，并在终端进入该目录：

   ```bash
   git clone https://github.com/guopengnaivoc/NEW-API-Ultra.git .
   if [ ! -e .env ]; then ./scripts/bootstrap-env.sh; fi
   docker compose config --quiet
   docker compose up -d --build
   for attempt in $(seq 1 30); do
     if curl -fsS http://127.0.0.1:3000/api/status; then break; fi
     if [ "$attempt" -eq 30 ]; then echo "NEW API did not become ready" >&2; exit 1; fi
     sleep 2
   done
   docker compose ps
   ```

3. 通过反向代理或本机访问 `http://127.0.0.1:3000`。对公网提供服务时，
   请使用 HTTPS 反向代理，并按 [`DEPLOYMENT.md`](../../DEPLOYMENT.md) 配置
   `SESSION_COOKIE_SECURE`、`SESSION_COOKIE_TRUSTED_URL` 和 `TRUSTED_PROXIES`。

`bootstrap-env.sh` 会生成 PostgreSQL、Redis、会话、加密和数据加密密钥。
请安全备份 `.env`，不要提交到 Git，也不要在重启或升级时重新生成已有实例的
密钥。Compose 默认使用命名卷保存数据库、应用数据和日志。

## 方法二：使用已发布的 GHCR 镜像

只有在 GitHub 仓库的 Packages/Releases 页面确认某个版本标签已经发布后，才使用
对应的镜像，例如：

```bash
cd /www/wwwroot/new-api-ultra
if [ ! -e .env ]; then ./scripts/bootstrap-env.sh; fi
export NEW_API_IMAGE=ghcr.io/guopengnaivoc/new-api-ultra:<tag>
docker compose up -d --no-build
for attempt in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:3000/api/status; then break; fi
  if [ "$attempt" -eq 30 ]; then echo "NEW API did not become ready" >&2; exit 1; fi
  sleep 2
done
```

不要使用 `latest` 或上游 `calciumion/new-api` 镜像来代替本仓库快照。部署前请核对
镜像摘要，并保留 `.env` 与数据库备份。

## 必要环境变量

| 变量 | 说明 | 要求 |
| --- | --- | --- |
| `SESSION_SECRET` | Access/Refresh Session 签名密钥 | 必填，所有共享实例必须一致 |
| `CRYPTO_SECRET` | 缓存键 HMAC 密钥；不得复用为 `DATA_ENCRYPTION_KEYS` | 必填，所有共享实例必须一致 |
| `DATA_ENCRYPTION_KEYS` | 渠道敏感凭据密钥环 | 必填 |
| `DATA_ENCRYPTION_ACTIVE_KEY_ID` | 当前密钥环版本 | 必填 |
| `POSTGRES_PASSWORD` | Compose PostgreSQL 密码 | 必填 |
| `REDIS_PASSWORD` | Compose Redis 密码 | 必填 |

## 常见问题

### 无法访问 3000 端口

先执行 `docker compose ps` 和 `docker compose logs new-api`，确认容器健康；默认端口
只绑定到 `127.0.0.1`。如果通过宝塔反向代理访问，请检查代理目标、HTTPS 和可信代理配置，
不要直接把开发用 HTTP 端口暴露到公网。

### 登录后会话失效或渠道密钥无法保存

确认 `.env` 中的 `SESSION_SECRET`、`CRYPTO_SECRET`、`DATA_ENCRYPTION_KEYS` 和
`DATA_ENCRYPTION_ACTIVE_KEY_ID` 均存在且在重启前后保持不变；`CRYPTO_SECRET` 与
`DATA_ENCRYPTION_KEYS` 必须使用不同的随机值。

### 如何更新

从源码更新时先备份数据库和 `.env`，再执行：

```bash
git fetch --tags
git checkout <审计过的版本标签>
docker compose up -d --build
```

不要执行 `docker compose down -v`，除非确认要删除所有卷和数据。

## 相关链接

- [发布仓库部署指南](../../DEPLOYMENT.md)
- [官方 New API 文档](https://docs.newapi.pro/zh/docs/installation)
- [环境变量配置](https://docs.newapi.pro/zh/docs/installation/config-maintenance/environment-variables)
- [官方 GitHub 仓库](https://github.com/QuantumNous/new-api)

## 许可证与再发布边界

发布前请阅读根目录的 `LICENSE`、`NOTICE`、`THIRD-PARTY-LICENSES.md` 和
`PROVENANCE.md`。`github.com/Calcium-Ion/go-epay v0.0.4` 的仓库元数据
显示为 MIT，但精确的 v0.0.4 标签没有许可证文件；在公开发布二进制或托管
服务前，必须向权利人取得并记录书面再分发确认。
