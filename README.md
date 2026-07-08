# CertLive ｜ 证活

> **为什么叫「证活」?**
> 一个「证」字,既是名词也是动词,藏着项目要解决的两件事:
> - **证** _(名词 · 证书)_ —— 盯紧域名当前生效的那张 SSL **证书**,剩余天数不足自动告警,到期前不让你错过续期窗口。
> - **证** _(动词 · 验证)_ —— 每轮探测顺带发一次 HTTP 请求,**证明** URL 是否仍可正常访问,状态码异常即时推送。
>
> 一个「活」字,既问证书「还有效」,也问站点「还活着」。**CertLive = Certificate + Live**,既是证书的存活,也是站点的存活。

基于 Golang + Gin 的轻量化 SSL 证书 + HTTP 健康监控系统。核心差异化：**实时探测站点线上正在运行的证书，自动忽略已替换废弃的旧证书，杜绝无效过期告警；HTTP 状态码异常也能即时推送**。

前端纯原生 HTML / CSS / JS，数据持久化用 SQLite 单文件，开箱即用，无外部依赖。

## 核心特性

### 精准证书探测

- 直连目标 `host:port` 完成 TLS 握手，抓取**当前生效**的证书
- 自动识别泛域名 `*.example.com`、单域名、多 SAN 证书
- 解析生效时间、过期时间、剩余天数、签发 CA、序列号、SAN 列表
- 证书被替换后**自动重置告警状态**，不会对同一张已下线证书反复打扰

### HTTP 健康探测

- TLS 握手成功后顺带发 HTTP 请求，记录状态码
- 列表里直接显示 `HTTP 200 / 4xx / 5xx / 失败`，一眼看出站点是否在线

### 标签管理

- 多对多关联：一个域名可贴多个标签，一个标签可被多个域名共用
- 标签可自定义图标（Font Awesome）+ 颜色
- 列表顶部支持「多标签 AND 筛选」、「10 天内到期快捷过滤」
- 拖拽排序

### 登录安全（反暴力破解）

单管理员后台也值得把门焊死。登录链路叠加了四道防线：

- **图形验证码** — 4 位字符（已剔除 `0/O/1/I/L` 避免看错）,透明背景 PNG,大小写不敏感,**一次性**消费(校验后立即失效)
- **IP 维度限流** — 同一 IP 在 10 分钟窗口内累计 5 次密码错误 → 锁 15 分钟,期间任何登录尝试直接返回 `429 + Retry-After`
- **bcrypt 密码哈希** — `DefaultCost=10`,即便数据库泄漏也无法逆推明文
- **HMAC 签名 cookie** — cookie 值为 `user.HMAC-SHA256(secret)`,服务端启动时注入随机 `SESSION_SECRET`,篡改即失效
- **账号枚举防护** — 用户名不存在 / 密码错误统一返回「用户名或密码错误」
- **失败 / 成功审计** — `log.Printf("login: fail/success ip=... user=...")` 写到 stdout,便于接日志聚合

> ⚠️ 反向代理场景下 `c.ClientIP()` 默认信任 `X-Forwarded-For`。生产环境建议用 nginx 反代,并通过 `engine.SetTrustedProxies()` 限制信任的代理来源,否则攻击者可伪造该 header 绕过限流。

### 通知推送（重点）

- **二选一渠道**：飞书机器人 或 企业微信机器人，UI 内可切换
- **双触发条件**，至少启用一个，命中其一即推送：
  - 条件 A：证书剩余天数小于 `N` 天
  - 条件 B：HTTP 状态码不在白名单内（如 `200,204,304`）
- **内容模板**：text / markdown 两种格式，每平台独立模板
- **变量替换**：模板里写 `{$host}`、`{$days}` 等占位符，发送时自动替换
- **频率限制 + 重试**：
  - 飞书：发送间隔 ≥ 600ms（满足 100/min、5/s）
  - 企业微信：发送间隔 ≥ 3s（满足 20/min）
  - 失败按 `1s → 2s → 4s` 退避重试 3 次
- **扫描节奏**：每轮探测周期（默认 20 分钟,可调 1–60 分钟）都会扫库一次,命中条件立即直推,不去重（每次扫到都推）
- **工具栏实时显示推送条件**：域名列表页顶部以 chip 形式展示当前生效的规则（如 `证书 ≤ 30 天 OR HTTP 不在 {200,201,204}`),任一域名命中阈值时对应 chip 红色脉冲高亮,一眼看出哪些数据正在触发推送。点击 chip 直接跳到通知设置

### 数据持久化与备份

- SQLite 单文件，无外部数据库依赖
- 一键导出 `.db` 备份（基于 `VACUUM INTO` 的一致快照）
- 一键上传备份恢复

### 公开 H5 浏览页（免登录查看）

需要把证书状态分享给团队 / 客户又不想给他们开账号?用公开 H5 页面:

- **设置 → 通用**里填一个随机串(如 `team1-abc`)作为 token,保存后访问 `/view/<token>` 即可免登录浏览
- 留空 = 关闭公开访问;改 token 即时生效,不需要重启
- token 不匹配 → 返回标准 `404 page not found`(跟 gin 默认 404 一致,防枚举)
- 移动端卡片式布局,显示**域名 / 证书 / 有效期 / HTTP / 检测时间 / 标签 / 说明** 7 个字段
- **deep link**:通知模板里写 `{$viewurl}`,发送时自动替换为 `/view/<token>?id=<share_id>`,收件人点开直接定位到出问题的那张域名卡片(排到第一 + 蓝色边框高亮 + 「当前关注」徽章)
- ⚠️ URL 一旦泄漏任何人都能看,token 务必用强随机串(如 `openssl rand -hex 8`),不用时及时清空

### 部署

- 单二进制，全平台运行（Windows / Linux / macOS）
- 前端模板和静态资源 **go:embed 进二进制**，部署只需一个文件
- 通过 `.env` 或环境变量配置端口、密钥、管理员账号
- Docker 镜像支持（多阶段构建，~30MB）

## 项目结构

```
cert-live/
├── main.go                    # 入口(CLI 子命令派发 + 服务启动)
├── internal/
│   ├── api/                   # HTTP 路由、handlers、server
│   │   ├── routes.go          # 路由树 + 中间件
│   │   ├── handlers.go        # 全部业务 handler
│   │   └── server.go          # Server struct + 生命周期
│   ├── auth/
│   │   ├── auth.go            # cookie 登录态、bcrypt、HMAC 签名
│   │   └── limiter.go         # 按 IP 的登录失败限流(反暴力破解)
│   ├── captcha/               # 图形验证码(透明 PNG、4 位、一次性)
│   ├── config/                # .env / 环境变量加载
│   ├── model/                 # 数据结构(Domain / Tag / Settings)
│   ├── notify/                # 飞书 / 企业微信 推送(限速 + 退避重试)
│   ├── probe/                 # TLS 握手 + HTTP 健康探测(各 10s 超时)
│   ├── scheduler/             # 周期循环:并发探测 → 扫库 → 推送
│   └── store/                 # SQLite schema + CRUD + 备份恢复
├── static/                    # 前端静态资源(CSS / JS / 图标 / Font Awesome)
├── templates/                 # HTML 模板(login.html / domains.html / h5.html)
├── data/                      # SQLite 数据库(运行时生成)
├── scripts/                   # 启停脚本(start/stop/restart)
├── Dockerfile                 # 多阶段构建(golang:1.25-alpine → alpine)
├── docker-compose.yml         # 一键编排
├── Makefile                   # build / run / start / stop / reset-admin
├── .env.example               # 环境变量样例
└── README.md
```

## 快速开始

### 1. 准备配置

```bash
cp .env.example .env
# 编辑 .env：
#   SESSION_SECRET  改成随机长字符串（cookie 签名密钥）
#   ADMIN_USER      首次启动自动写入的管理员账号
#   ADMIN_PASS      首次启动自动写入的管理员密码
#   APP_PORT        监听端口（默认 8080）
#   DB_PATH         SQLite 文件路径（默认 ./data/certlive.db）
```

### 2. 编译 + 运行

```bash
make run         # 前台运行（编译 + 启动）
# 或
make start       # 后台运行（带 PID + 日志，写入 logs/app.log）
make stop        # 停止后台进程
make restart     # 重启
make status      # 查看运行状态
```

浏览器打开 `http://localhost:8080`，用 `.env` 里设置的管理员账密登录。

### CLI 子命令

```bash
./cert-live                 # 等同于 serve(默认)
./cert-live serve           # 启动 HTTP 服务
./cert-live reset-admin     # 重置登录账号 / 密码(见下)
./cert-live version         # 打印版本号
./cert-live help            # 显示帮助
```

### 修改账号 / 密码

`.env` 里的 `ADMIN_USER` / `ADMIN_PASS` 只在首次启动时 seed 一次，之后改密码不生效。要改账号密码用专用子命令：

```bash
# 交互式（推荐，密码不回显）
./cert-live reset-admin

# 非交互式（脚本/CI 用）
./cert-live reset-admin <新账号> <新密码>
```

**复杂度校验**:账号 ≥ 5 字符、密码 ≥ 6 字符,只允许可打印 ASCII(拒绝汉字 / emoji)。子命令直接读写 `data/certlive.db` 的 `settings` 表(`login_user` / `login_password`),不启动服务,跑完即退出。

### 3. 配置通知推送

登录后点右上角齿轮 → **设置 → 通知管理**：

1. 选平台（飞书 / 企业微信）
2. 填对应机器人的 Webhook 地址
3. 选格式（text / markdown）
4. 编辑推送内容模板（用 `{$变量}` 占位）
5. 至少启用一个触发条件：
   - 条件 A：证书剩余天数小于 `N` 天（默认 30）
   - 条件 B：HTTP 状态码不在白名单（默认 `200,201,204,301,302,304,307,308`）
6. 保存

下一轮探测周期（默认 20 分钟,可在设置里改 `cycle_interval_min`）就会扫库一次,命中条件的域名自动推送。也可以在域名列表点「立即检查」按钮触发单条探测 + 即时推送。

## Docker 部署

镜像已发布到 Docker Hub:[`gongwen/cert-live`](https://hub.docker.com/r/gongwen/cert-live),支持 `linux/amd64` + `linux/arm64` 双架构。多阶段构建:`golang:1.25-alpine` 编译 → `alpine:latest` 运行,最终镜像约 30 MB,二进制用 `go:embed` 把模板和静态资源烤进去,只剩一个可执行文件 + SQLite 数据卷。

三种姿势按需选:**方式一**(拉公共镜像,最快) / **方式二**(本地构建 + docker run) / **方式三**(docker-compose)。

> ⚠️ **docker 默认不切割日志**,容器跑久了 stdout 全堆在 `/var/lib/docker/containers/<id>/<id>-json.log`,会把宿主机根分区撑爆。下面三种方式都配了日志轮转(单文件 10MB、最多 3 个、滚动覆盖,上限约 30MB),需要更长历史可调 `max-size` / `max-file`。

### 方式一:从 Docker Hub 拉镜像(推荐)

不用 clone 代码、不用本地构建,直接拉公共镜像跑起来:

```bash
# 1. 建数据目录
mkdir -p ./data && cd ./data && cd ..

# 2. 一行起容器
docker run -d \
  --name cert-live \
  --restart unless-stopped \
  --dns 119.29.29.29 \
  --dns 223.5.5.5 \
  -p 8080:8080 \
  --log-driver json-file \
  --log-opt max-size=10m \
  --log-opt max-file=3 \
  -v /etc/localtime:/etc/localtime:ro \
  -v "$(pwd)/data:/app/data" \
  -e GIN_MODE=release \
  -e SESSION_SECRET="$(openssl rand -hex 32)" \
  -e ADMIN_USER=admin \
  -e ADMIN_PASS=StrongPass123 \
  gongwen/cert-live:latest
```

浏览器开 `http://localhost:8080` 登录。

**指定版本**(生产推荐,方便回滚):

```bash
docker run -d --name cert-live \
  --dns 119.29.29.29 \
  --dns 223.5.5.5 \
  -p 8080:8080 \
  --log-driver json-file \
  --log-opt max-size=10m \
  --log-opt max-file=3 \
  -v "$(pwd)/data:/app/data" \
  -e SESSION_SECRET="$(openssl rand -hex 32)" \
  -e ADMIN_USER=admin \
  -e ADMIN_PASS=StrongPass123 \
  gongwen/cert-live:v1.2.0
```

版本号跟 GitHub release 一一对应,见 [Tags 页面](https://hub.docker.com/r/gongwen/cert-live/tags)。

### 方式二:本地构建 + `docker run`

适合要改代码、或拿不到 Docker Hub 的场景:

```bash
# 1. clone 代码
git clone https://github.com/gongwalker/cert-live.git
cd cert-live

# 2. 本地构建
docker build -t cert-live:latest .

# 3. 起容器
docker run -d \
  --name cert-live \
  --restart unless-stopped \
  --dns 119.29.29.29 \
  --dns 223.5.5.5 \
  -p 8080:8080 \
  --log-driver json-file \
  --log-opt max-size=10m \
  --log-opt max-file=3 \
  -v /etc/localtime:/etc/localtime:ro \
  -v /etc/timezone:/etc/timezone:ro \
  -v "$(pwd)/data:/app/data" \
  -e GIN_MODE=release \
  -e SESSION_SECRET="$(openssl rand -hex 32)" \
  -e ADMIN_USER=admin \
  -e ADMIN_PASS=StrongPass123 \
  cert-live:latest
```

要点(方式一/二通用):
- **`-p 8080:8080`**:宿主机端口:容器端口,两边一致就行,要改一起改(还要带 `-e APP_PORT=同端口`)
- **`--log-driver json-file --log-opt ...`**:日志驱动 + 滚动策略。docker 默认不切割日志,长跑会撑爆硬盘,务必带上(详见上面那节开头的警告)
- **`-v "$(pwd)/data:/app/data"`**:SQLite 文件持久化,容器删了数据还在
- **`-v /etc/localtime`**:宿主机时区同步到容器(镜像里也装了 `tzdata` 兜底)
- **`SESSION_SECRET`**:必填,cookie 签名密钥,**生产一定要换成强随机串**
- **`ADMIN_USER` / `ADMIN_PASS`**:首次启动 seed 进 settings 的初始账号密码;之后改密码用子命令(见下)

### 方式三:`docker-compose`

项目根目录已经有 `docker-compose.yml`,默认从 Docker Hub 拉镜像(不想本地构建):

```yaml
services:
  cert-live:
    image: gongwen/cert-live:latest      # 直接拉公共镜像
    # build: .                            # 想本地构建就注释掉 image,放开 build
    container_name: cert-live
    restart: unless-stopped
    # 显式指定公共 DNS,避免云厂内部 DNS 在容器内不响应(腾讯云 183.60.x 常见坑)
    dns:
      - 119.29.29.29
      - 223.5.5.5
    # 日志轮转:单文件 10MB,最多 3 个,满了滚动覆盖(约上限 30MB)
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    ports:
      - "8080:8080"
    volumes:
      - ./data:/app/data
      - /etc/localtime:/etc/localtime:ro
      - /etc/timezone:/etc/timezone:ro
    environment:
      - GIN_MODE=release
      - SESSION_SECRET=please-change-me-to-a-long-random-string
      - ADMIN_USER=admin
      - ADMIN_PASS=admin123
      - TZ=Asia/Shanghai
```

启动:

```bash
docker compose up -d                # 拉镜像 + 起容器(首次或改完 compose 后)
docker compose logs -f              # 看日志
docker compose restart              # 重启
docker compose down                 # 停止并删除容器(数据卷保留)
```

### 改账号 / 密码（容器内执行子命令）

```bash
# 交互式
docker exec -it cert-live ./cert-live reset-admin

# 非交互式（脚本 / CI）
docker exec cert-live ./cert-live reset-admin admin NewStrongPass123
```

子命令直接读写挂载的 `data/certlive.db`，不需要重启容器，下次登录就用新账密。

### 升级流程

```bash
git pull
docker compose build         # 重新构建镜像
docker compose up -d         # 替换容器（数据卷 ./data 保留）
```

数据文件 `./data/certlive.db` 跨升级保留，不需要迁移。

### 备份 / 恢复

宿主机直接复制文件就行：

```bash
# 备份（建议先停容器避免文件锁）
docker compose stop
cp ./data/certlive.db ./backup-$(date +%Y%m%d).db
docker compose start
```

或用 Web UI 里的「备份」按钮（`GET /api/backup` 会下 `.db` 文件，基于 `VACUUM INTO` 出一致快照，无需停服务）。

### 故障排查：容器内 DNS 解析超时（`lookup xxx: i/o timeout`）

**症状**：cert-live 日志报 `dial tcp: lookup xxx: i/o timeout`，所有域名探测都失败，但宿主机 `dig`/`nslookup` 同一个域名正常。

**两种常见根因**：

**1. 云厂内部 DNS 在容器内不响应**（典型：腾讯云 `183.60.x`，基于源 IP 鉴权，容器走 docker0 NAT 后响应被丢）

给容器指定公共 DNS 即可（本文档所有 `docker run` 示例默认已加）：

```bash
--dns 119.29.29.29 --dns 223.5.5.5
```

**2. `/etc/docker/daemon.json` 里设了 `"iptables": false`，docker 没建立容器网络的转发规则**

这条更隐蔽 —— 改 DNS server 救不了，因为容器到外网的所有流量（UDP 53 也不例外）都被宿主机 FORWARD 链默认 DROP。

诊断：

```bash
# 看宿主机 FORWARD 链是否有 DOCKER-USER / DOCKER-FORWARD（空的就是 docker 没建规则）
iptables -L FORWARD -n | head -5

# 看 daemon.json 是否禁用了 iptables
cat /etc/docker/daemon.json
```

修复：把 `"iptables": false` 改成 `"iptables": true`（或直接删掉这行），然后 `systemctl restart docker`，docker 会自动重建所有 FORWARD/MASQUERADE 规则。

> 重启 docker 会影响所有容器（短暂断网几秒，数据卷不丢）。没设 `restart: unless-stopped` 的容器需要手动 `docker start <name>` 拉起。

## 推送变量

模板里这些占位符发送时会被替换：

| 变量 | 含义 |
|---|---|
| `{$host}` | 主机名 |
| `{$url}` | 完整 URL（含端口 + 路径） |
| `{$days}` | 剩余天数 |
| `{$http_status}` | HTTP 状态码（无则空） |
| `{$subject}` | 证书主体 |
| `{$issuer}` | 签发 CA |
| `{$expire_date}` | 到期日期 `YYYY-MM-DD HH:MM:SS` |
| `{$time}` | 当前时间 |
| `{$tags}` | 所有标签，空格分隔 |
| `{$notes}` | 说明 |
| `{$viewurl}` | 详情查看 URL（`/view/<token>?id=<share_id>`，公开访问未开启则为空；收件人点开直达该域名卡片） |
| `{$notify_rule}` | 当前推送条件（如 `证书 ≤ 30 天 OR HTTP 不在 {200,201,204}`，跟列表页 chip 一致） |

## 调度节奏

后台只有一个调度循环(`scheduler.Scheduler.Run`),开机 30 秒后跑首次,之后按 `cycle_interval_min` 间隔循环。**每一轮都串行做两件事**:

```
开机 → 等 30s → [并发探测所有域名 → 扫库找命中 → 限速推送] → 等 cycle → 再来一轮 → ...
```

| 阶段 | 行为 | 关键参数 |
|---|---|---|
| 探测 | 并发对每个域名做 TLS 握手 + HTTP GET,结果写回 `domains` 表 | 并发上限 10,单域名超时 10s |
| 推送 | 扫库读已探测的结果,命中条件 A/B 立即推送(不去重) | 平台限速:飞书 600ms/条、企微 3s/条 |

**周期配置**:UI 里的 `cycle_interval_min` 控制,默认 **20 分钟**,可调范围 **1–60 分钟**。每次循环开始时从 DB 读,改完设置下一轮就生效。

> 💡 推送用的是刚刚那几秒探测到的最新数据(同事务串行),所以不会出现「证书都换了还在用老结果告警」的偏差。
>
> ⚠️ **不去重**:只要域名持续命中条件,每一轮都会推一次。比如某域名剩 8 天、阈值 30 天,那它会每 20 分钟推一次直到你处理。如果嫌烦可以把阈值调小、暂时关条件 A、或把该域名删除。

## 配置项一览

环境变量（`.env`）：

| Key | 默认值 | 说明 |
|---|---|---|
| `APP_PORT` | `8080` | 监听端口 |
| `GIN_MODE` | `debug` | gin 模式：`debug` / `release` |
| `SESSION_SECRET` | 必填 | cookie 签名密钥 |
| `ADMIN_USER` | `admin` | 首次启动 seed 进 settings 的管理员账号（仅一次） |
| `ADMIN_PASS` | 必填 | 首次启动 seed 进 settings 的管理员密码（bcrypt 哈希存） |
| `DB_PATH` | `./data/certlive.db` | SQLite 文件路径 |

通知设置（存在 `settings` 表，UI 配置，全部以 `notify_` 前缀）：

| Key | 类型 / 默认 | 说明 |
|---|---|---|
| `notify_channel` | `feishu` / `wecom` (默认 `feishu`) | 当前激活平台 |
| `notify_feishu_webhook` | string (默认空) | 飞书机器人地址 |
| `notify_feishu_format` | `text` / `markdown` (默认 `markdown`) | 飞书推送格式 |
| `notify_feishu_text` | string | 飞书推送模板 |
| `notify_wecom_webhook` | string (默认空) | 企业微信机器人地址 |
| `notify_wecom_format` | `text` / `markdown` (默认 `text`) | 企业微信推送格式 |
| `notify_wecom_text` | string | 企业微信推送模板 |
| `notify_cond_a_enabled` | bool (默认 `true`) | 条件 A 开关 |
| `notify_cond_a_days` | int (默认 `30`) | 条件 A：剩余天数阈值 |
| `notify_cond_b_enabled` | bool (默认 `false`) | 条件 B 开关 |
| `notify_cond_b_codes` | string (默认 `200,201,204,301,302,304,307,308`) | 条件 B：HTTP 状态码白名单 |
| `cycle_interval_min` | int (默认 `20`,范围 1–60) | 探测 + 推送的循环周期(分钟) |
| `public_path` | string (默认空 = 关闭) | 公开 H5 访问的 token;非空时 `/view/<token>` 免登录查看域名证书状态 |

> 🔒 登录相关 key 也存在 `settings` 表,但不在 UI 暴露(由 CLI 子命令管理):
> - `login_user` — 管理员用户名
> - `login_password` — bcrypt 哈希(cost=10)

## API 一览

所有 JSON 响应统一信封:`{ code, message, data }`。成功 `code=200`,失败 `code=HTTP 状态码`(401/403/404/429/500),`data=null`。

**公开接口**(无需登录):

| Method | Path | 说明 |
|---|---|---|
| `GET/POST` | `/login` | 登录页 / 提交登录(失败累计触发限流) |
| `GET` | `/logout` | 清 cookie 重定向到 `/login` |
| `GET` | `/api/captcha` | 生成图形验证码,返回 `{id, img}` |
| `GET` | `/view/:token` | 公开 H5 浏览页(token 在通用设置里配,不匹配返回 404) |

**受保护接口**(需 cookie,401 重定向到 `/login`):

| Method | Path | 说明 |
|---|---|---|
| `GET` | `/api/me` | 当前登录用户 |
| `GET/POST` | `/api/domains` | 列表(支持 `?search=&tag_ids=`) / 新增 |
| `PUT/DELETE` | `/api/domains/:id` | 编辑 / 删除 |
| `POST` | `/api/domains/:id/check` | 立即探测单个域名 |
| `POST` | `/api/domains/check-all` | 触发全量探测 |
| `PUT` | `/api/domains/reorder` | 拖拽排序保存 |
| `GET/POST` | `/api/tags` | 标签列表 / 新增 |
| `PUT/DELETE` | `/api/tags/:id` | 编辑(可改 name/icon/color) / 删除 |
| `PUT` | `/api/tags/reorder` | 标签排序 |
| `GET/PUT` | `/api/settings` | 读取 / 保存通知设置 |
| `GET` | `/api/backup` | 下载数据库快照(`VACUUM INTO`) |
| `POST` | `/api/restore` | 上传 `.db` 备份恢复 |

## 数据模型

SQLite 单文件,启用 `foreign_keys=ON`、`busy_timeout=5000ms`,Go 侧 `MaxOpenConns=1`(避免写锁竞争)。四张表:

### `domains` — 域名 + 最近一次探测结果
- 用户字段:`id` / `host` / `port`(默认 443) / `path`(默认 `/`) / `notes` / `sort_order` / `created_at` / `share_id`(对外分享用的 16 字符 hex,CreateDomain 时随机生成)
- 证书探测:`subject` / `issuer` / `issuer_org` / `sans` / `serial_number` / `not_before` / `not_after` / `is_wildcard` / `days_remaining` / `last_checked` / `last_error`
- HTTP 探测:`http_status` / `http_error` / `http_checked`
- 索引:`host` / `not_after` / `sort_order` / `share_id`(partial UNIQUE,仅非 NULL 值约束)

### `tags` — 标签定义
- `id` / `name`(UNIQUE) / `icon`(Font Awesome 类名) / `color`(hex) / `sort_order` / `created_at`

### `domain_tags` — 多对多关联
- `(domain_id, tag_id)` 复合主键,两端都 `ON DELETE CASCADE`
- 多标签 AND 筛选用 `GROUP BY ... HAVING COUNT(DISTINCT tag_id) = N` 实现

### `settings` — KV 配置
- `key` TEXT PK / `value` TEXT
- 通知相关:`notify_*` 前缀(见上表)
- 登录相关:`login_user` = 用户名、`login_password` = bcrypt 哈希
- 周期:`cycle_interval_min`

## License

见 [LICENSE](LICENSE)。
