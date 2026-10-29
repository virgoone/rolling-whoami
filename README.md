# rolling-whoami

一个用于测试滚动部署的极简 HTTP 服务：Go 标准库，零第三方依赖，静态编译，`scratch` 运行镜像，非 root 用户。不读取或回显环境密钥、Authorization、Cookie、请求体和查询参数。

## HTTP

| 地址 | 用途 |
| --- | --- |
| `/` | JSON：版本、实例 ID、hostname、启动时间、运行时长、实例请求计数 |
| `/healthz` | 存活检查，200 |
| `/readyz` | 就绪检查；启动等待期间和收到终止信号后返回 503 |
| `/wait?duration=5s` | 慢请求，测试切换时已有请求是否能完成；最长 20 秒，每实例最多 32 个并发慢请求 |

每个响应带 `Cache-Control: no-store`；身份响应还带 `X-App-Version` 和 `X-Instance-ID`。未知路径返回 404，不支持的方法返回 405。

配置只有 `PORT`（默认 8080）和可选的 `STARTUP_DELAY`（默认 0s，最长 1m）。版本来自仓库里的 `VERSION` 文件。收到 SIGTERM/SIGINT 后停止接收连接，最多等待 25 秒让在途请求完成。

## 本地运行 / Docker

```sh
go test -race ./...
go run .
# 另一个终端
curl localhost:8080

docker build -t rolling-whoami:v1 .
docker run --rm -p 8080:8080 --stop-timeout 30 rolling-whoami:v1
```

镜像中没有 shell、curl、包管理器或 CA 证书；服务只提供 HTTP，HTTPS 交给 Berth 入口。Dockerfile 使用 Go 1.26 构建，最终层只有静态二进制。无需预先发布 Docker Hub/GHCR 镜像，Berth 可直接克隆公开仓库构建。可选构建参数 `VCS_REF` 用于填充响应里的 revision（默认 unknown）。

## 在 Berth 部署

创建项目和 Git 服务，仓库地址 `https://github.com/virgoone/rolling-whoami.git`，分支 `main`，Dockerfile 路径 `Dockerfile`。

| 设置 | 值 |
| --- | --- |
| 公开访问 / 端口 | 开启 / 8080 |
| 副本数 | 2 |
| 部署策略 | `rolling` |
| 健康检查 | `/readyz`，间隔 1s，超时 2s，连续通过 2 次，启动上限 60s |
| 每副本资源 | 0.25 CPU / 128 MiB |
| 排空时间 | 10s |
| 停止宽限时间 | 30s |
| 可选运行变量 | `STARTUP_DELAY=3s`，便于观察 readiness 生效 |

不挂卷，不需要数据库或其他平台服务。两个副本部署期间会短暂增加一个新副本，节点需要留出余量。若没有配置 GitHub App 自动部署，推送后在 Berth 手动部署。

## 测试 v1 → v2

1. 部署 `VERSION` 内容为 `v1` 的提交。
2. 开始采样（Python 3 标准库，无需依赖）：

   ```sh
   python3 scripts/probe.py https://YOUR-SERVICE --seconds 180 --output probe.jsonl
   ```

3. 把 `VERSION` 改为 `v2`，提交、推送并在 Berth 部署新提交。
4. 检查采样结果：正常请求和 5 秒慢请求均没有错误，版本从 v1 过渡到 v2，最终只剩两个 v2 实例。脚本不重试、不忽略失败；出现错误会以状态码 1 退出。每次请求记录在 JSONL，统计写入旁边的 `.summary.json`。
5. 可在 Berth 回滚到 v1，再运行同样的采样。

默认 4 个普通请求线程加 1 个慢请求线程，HTTPS 会校验证书。一次有限窗口内零错误只能说明这次测试通过，不能证明所有故障情形下都零停机。
