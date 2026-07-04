# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

```bash
make build              # go build -o cert-live .
make run                # build + 前台运行(Ctrl+C 退出)
make start              # build + 后台启动(PID 写 .run/cert-live.pid,日志写 logs/app.log)
make stop | restart | status
make tidy               # go mod tidy
make clean              # 删二进制 + .run/ + logs/
make reset-admin        # 交互式重置管理员账密(等价 ./cert-live reset-admin)
```

直接调二进制也行(子命令:`serve` / `reset-admin [user pass]` / `version` / `help`)。

启动后浏览器开 `http://localhost:<APP_PORT>`,默认 8080。账密来自 `.env`(首次启动 seed 一次,之后改用 `./cert-live reset-admin`)。

**注意:仓库没有测试套件**(无 `*_test.go`),也没有 lint 配置。验证改动靠 `go build ./...` + `go vet ./...`。

## 配置加载

环境变量或 `.env`(由 `internal/config` 用 `os.Getenv` + `joho/godotenv` 加载):

| 变量 | 默认 | 用途 |
|---|---|---|
| `APP_PORT` | `8080` | HTTP 监听端口 |
| `GIN_MODE` | `debug` | gin 模式,生产用 `release` |
| `SESSION_SECRET` | 必填 | cookie HMAC 签名密钥,**生产务必换强随机串** |
| `ADMIN_USER` | `admin` | 仅首次启动 seed 一次 |
| `ADMIN_PASS` | 必填 | 同上,bcrypt 入库 |
| `DB_PATH` | `./data/certlive.db` | SQLite 文件路径 |

## 架构

### 入口与依赖注入
`main.go` 解析 CLI → 调 `config.Load()` → `auth.Configure(sessionKey)` 注入 HMAC 密钥 → `store.Open` → `EnsureSchema` → `EnsureLogin` seed 管理员 → `api.New(cfg, st, assetsFS)` 构造 `Server` → `srv.StartScheduler(ctx)` 起 goroutine → `srv.Run(addr)` 阻塞。

`//go:embed templates static` 把模板和静态资源烤进二进制(`main.go:21`),`assetsFS` 透传给 `api.Server`,Server 内部用 `loadTemplates()` + `staticFS()` 解出。

### 请求层(`internal/api`)
- `server.go` — `Server` struct 持有 `cfg / st / scheduler / limiter / assets / http`。`New()` 里构造并启动 `LoginLimiter.StartCleanup()`。
- `routes.go` — gin 引擎,公开路由(`/login`、`/logout`、`/api/captcha`)直接挂,业务接口进 `/api` 组挂 `auth.RequireAuth()` 中间件。
- `handlers.go` — 全部业务 handler。响应统一走 `ok(c, data)` / `fail(c, code, msg)`,信封 `{code, message, data}`。**所有新接口都要走这两个 helper,不要直接 `c.JSON`**,否则前端 `res.code !== 200` 分支判断会乱。

### 调度循环(`internal/scheduler`)— **这是最容易误解的地方**
只有一个循环 `Run()`,开机等 30s 跑首次,之后按 `cycle_interval_min` 间隔循环。**每一轮串行做两件事**:`probeAll(ctx)` 并发探测所有域名(信号量限 10 并发)→ `scanAndPush()` 扫库读已探测的结果,命中条件就推送。

**没有独立的"5 分钟通知扫描"** — 探测和推送是同一事务,推送用的永远是刚那几秒探测到的最新数据。周期配置 `cycle_interval_min` 存在 settings 表,默认 20 分钟,范围 1–60 分钟,每次循环开始从 DB 读(改完设置下一轮就生效,无需重启)。

`RunOnce()` 供 `POST /api/domains/check-all` 手动触发;`CheckOne(id)` 供 `POST /api/domains/:id/check`,后者探测完异步 `maybePushOne`。

### 探测(`internal/probe`)
- `Probe(host, port)` — `tls.DialWithDialer` 拿当前生效证书,10s 超时。**项目核心差异点:只看 live cert,不查 CT log,所以证书被替换后不会对老证书误报。**
- `HTTPProbe(host, port, path)` — TLS 完成后顺带发 HTTP GET,**不跟随重定向**(否则 health check 没意义),10s 超时。

### 持久化(`internal/store`)
- 用 `modernc.org/sqlite`(纯 Go 驱动,**没有 cgo 依赖**,所以交叉编译干净)。
- DSN 强制 `foreign_keys(1)` + `busy_timeout(5000)`。
- `db.SetMaxOpenConns(1)` — SQLite 单写,串行化避免锁竞争;并发靠 `sync.Mutex` 保护。
- **不做 ALTER 迁移**(`store.go:48` 注释明说) — 改 schema 要么 `IF NOT EXISTS` 加新表/字段,要么删 `data/certlive.db` 重来。
- 备份用 `VACUUM INTO` 出一致快照;恢复是关库 → 替换文件 → 重开 → re-run schema。
- 四张表:`domains`(含证书+HTTP 探测结果字段) / `tags` / `domain_tags`(多对多,CASCADE 删除) / `settings`(KV,登录账号也存这里)。
- **多标签 AND 筛选**(`store.go:104`):用 `GROUP BY domain HAVING COUNT(DISTINCT tag_id) = N`,改查询逻辑时注意这个模式。

### 认证 & 反暴力破解(`internal/auth`)
- `auth.go` — bcrypt 密码(`DefaultCost=10`),cookie 值 `user.HMAC-SHA256(secret)`,`secret` 由 `Configure()` 在启动时注入。`CurrentUser` 校验签名。
- `limiter.go` — 按 IP 内存版限流,**不持久化**。常量:`loginWindow=10min` / `loginMaxFails=5` / `loginLockout=15min` / `loginCleanupTick=5min`。`StartCleanup()` 在 `api.New()` 里启动。**改阈值要同步改 `handlers.go:LoginSubmit` 里的 429 提示文案。**

### 通知(`internal/notify`)
飞书 / 企业微信 webhook。**全局互斥锁**强制平台限速(飞书 600ms/条、企微 3s/条),失败按 `1s→2s→4s` 退避重试 3 次。`Render(tmpl, vars)` 把 `{$host}` 等占位符替换掉。

### Captcha(`internal/captcha`)
基于 `base64Captcha`,字符集 `ABCDEFGHJKMNPQRSTUVWXYZ23456789`(剔除易混 `0/O/1/I/L`),4 位、透明 PNG、大小写不敏感、**一次性**(verify 后即从 store 删除)。

## 关键约束 / 易踩坑

- **响应必须走 `ok()` / `fail()`**(handlers.go 顶部两个 helper),前端 `static/js/login.js` 和 `domains.js` 全靠 `res.code !== 200` 分支判断。
- **改 schema 不走 ALTER** — 直接 `schema.go` 加 `IF NOT EXISTS`,或让用户删 `data/certlive.db`。`EnsureSchema` 跑的是 `schema` 常量整段。
- **生产部署在反代后** — `c.ClientIP()` 默认信任 `X-Forwarded-For`,直接暴露公网会被伪造 header 绕过登录限流。代码里没强制配 `SetTrustedProxies`,留给部署方决定。
- **新增配置项**要同时改:`model.DefaultSettings()`(默认值)+ `scheduler.readSettings()`(从 KV 表反序列化)+ `handlers.go:handleUpdateSettings` 的 `allowed` 白名单。漏一处前端就会显示但保存不进去。
- **所有时间戳走 Unix epoch 秒**(不是毫秒),`model.Domain` 里 `CreatedAt` / `NotBefore` / `NotAfter` / `LastChecked` / `HTTPChecked` 都是 int64 秒。
- **`probeOne` 即便失败也写库**(写 `last_error`),下一轮照探。所以 `scanAndPush` 里用 `d.LastError != "" || d.NotAfter == 0` 跳过未探测 / 探测失败的,避免误报。

## Docker

`Dockerfile` 多阶段:`golang:1.25-alpine` 编译 → `alpine:latest` 运行,镜像 ~30MB。`docker-compose.yml` 已带 `TZ=Asia/Shanghai` + `/etc/localtime` 挂载。容器改账密用 `docker exec -it cert-live ./cert-live reset-admin`,直接写挂载的 `data/certlive.db`,无需重启。
