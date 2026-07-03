# CertLive ｜ 证活

基于 Golang + Gin 的轻量化 SSL 证书 + HTTP 健康监控系统。核心差异化：**实时探测站点线上正在运行的证书，自动忽略已替换废弃的旧证书，杜绝无效过期告警；HTTP 状态码异常也能即时推送**。

前端纯原生 HTML / CSS / JS（无 Vue / React），数据持久化用 SQLite 单文件，开箱即用，无外部依赖。

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
- **扫描节奏**：固定每 5 分钟扫库一次，命中条件立即直推，不去重（每次扫到都推）

### 数据持久化与备份

- SQLite 单文件，无外部数据库依赖
- 一键导出 `.db` 备份（基于 `VACUUM INTO` 的一致快照）
- 一键上传备份恢复

### 部署

- 单二进制，全平台运行（Windows / Linux / macOS）
- 前端无构建工具，磁盘文件直出
- 通过 `.env` 配置端口、密钥、管理员账号

## 项目结构

```
cert-live/
├── main.go                    # 入口
├── internal/
│   ├── api/                   # HTTP 路由、handlers、server
│   ├── auth/                  # cookie 登录态、密码哈希
│   ├── captcha/               # 登录图形验证码
│   ├── config/                # .env 加载
│   ├── model/                 # 数据结构（Domain / Tag / Settings）
│   ├── notify/                # 飞书/企业微信 推送
│   ├── probe/                 # TLS + HTTP 探测
│   ├── scheduler/             # 定时巡检 + 5 分钟通知扫描
│   └── store/                 # SQLite schema + CRUD
├── static/                    # 前端静态资源（CSS / JS / 图标）
├── templates/                 # HTML 模板
├── data/                      # SQLite 数据库（运行时生成）
├── scripts/                   # 启停脚本
├── Makefile                   # build / run / start / stop
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

### 修改账号 / 密码

`.env` 里的 `ADMIN_USER` / `ADMIN_PASS` 只在首次启动时 seed 一次，之后改密码不生效。要改账号密码用专用子命令：

```bash
# 交互式（推荐，密码不回显）
./cert-live reset-admin

# 非交互式（脚本/CI 用）
./cert-live reset-admin <新账号> <新密码>
```

子命令直接读写 `data/certlive.db` 的 `settings` 表，不启动服务，跑完即退出。

### 3. 配置通知推送

登录后点右上角齿轮 → **设置 → 通知管理**：

1. 选平台（飞书 / 企业微信）
2. 填对应机器人的 Webhook 地址
3. 选格式（text / markdown）
4. 编辑推送内容模板（用 `{$变量}` 占位）
5. 至少启用一个触发条件：
   - 条件 A：证书剩余天数小于 `N` 天
   - 条件 B：HTTP 状态码不在白名单
6. 保存

5 分钟内调度器会扫库一次，命中条件的域名会自动推送。

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

## 调度节奏

| 任务 | 间隔 | 触发条件 |
|---|---|---|
| 证书 + HTTP 探测 | `check_interval` 设置（默认 6 小时） | 开机后 5s 跑首次，之后按间隔 |
| 通知推送扫描 | 固定 5 分钟 | 命中条件 A 或 B 且未推送过的域名 |

通知扫描基于已探测的结果（`days_remaining` / `http_status`）判断，**不会**触发新的 TLS 握手，所以 5 分钟一轮的开销极低。

> ⚠️ 不去重：只要域名持续命中条件，每次扫描都会推一次。比如某域名剩 8 天、阈值 30 天，那它会每 5 分钟推一次直到你处理。如果嫌烦可以把阈值调小或暂时关条件 A。

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

| Key | 类型 | 说明 |
|---|---|---|
| `notify_channel` | `feishu` / `wecom` | 当前激活平台 |
| `notify_feishu_webhook` | string | 飞书机器人地址 |
| `notify_feishu_format` | `text` / `markdown` | 飞书推送格式 |
| `notify_feishu_text` | string | 飞书推送模板 |
| `notify_wecom_webhook` | string | 企业微信机器人地址 |
| `notify_wecom_format` | `text` / `markdown` | 企业微信推送格式 |
| `notify_wecom_text` | string | 企业微信推送模板 |
| `notify_cond_a_enabled` | bool | 条件 A 开关 |
| `notify_cond_a_days` | int | 条件 A：剩余天数阈值 |
| `notify_cond_b_enabled` | bool | 条件 B 开关 |
| `notify_cond_b_codes` | string | 条件 B：HTTP 状态码白名单 |
| `check_interval` | int（分钟） | 证书探测周期 |

## API 一览（需登录 cookie）

| Method | Path | 说明 |
|---|---|---|
| `GET` | `/api/me` | 当前登录用户 |
| `GET/POST` | `/api/domains` | 列表 / 新增域名 |
| `PUT/DELETE` | `/api/domains/:id` | 编辑 / 删除 |
| `POST` | `/api/domains/:id/check` | 立即探测单个域名 |
| `POST` | `/api/domains/check-all` | 触发全量探测 |
| `PUT` | `/api/domains/reorder` | 拖拽排序保存 |
| `GET/POST` | `/api/tags` | 标签列表 / 新增 |
| `PUT/DELETE` | `/api/tags/:id` | 编辑 / 删除 |
| `PUT` | `/api/tags/reorder` | 标签排序 |
| `GET/PUT` | `/api/settings` | 读取 / 保存通知设置 |
| `GET` | `/api/backup` | 下载数据库快照 |
| `POST` | `/api/restore` | 上传备份恢复 |

## 数据模型

- `domains` — 域名 + 最近一次证书/HTTP 探测结果
- `tags` / `domain_tags` — 标签 + 多对多关联
- `settings` — KV 配置（`key` TEXT PK, `value` TEXT）。除了通知配置，**登录账号也存这里**：`login_user` = 用户名、`login_password` = bcrypt 哈希

## License

见 [LICENSE](LICENSE)。