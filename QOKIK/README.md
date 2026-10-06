# 拾光 · 杭电校园失物招领

本地优先的校园失物招领应用。访客可搜索已审核信息；注册用户可发布寻物/拾获信息、提交私密线索或认领并举报；管理员可审核内容、处理举报。学号当前只是账号标识，未连接学校统一认证。站内聊天、图片上传、消息推送和公网部署暂不包含。

## 技术栈

- 前端：Vue 3、TypeScript、Vite
- API：Go、Gin、pgx、PostgreSQL
- 认证：15 分钟 Access JWT（仅保存在前端内存），7 天 Refresh JWT（HttpOnly、SameSite=Strict Cookie，使用时轮换）
- 密码：Argon2id
- 本地服务：Docker Compose 启动 API 和 PostgreSQL；Vite 在宿主机运行

本次迁移不会导入旧 SQLite 数据。PostgreSQL 是新数据库，原有用户需重新注册。已有的 `backend/data/hduhelp.sqlite3` 不会被新服务读取。

## 本地运行

需要 Docker Desktop（且 Docker Engine 已启动）、Node.js 20+ 和 npm。

以下命令均从 `QOKIK/` 项目目录运行。首次先复制环境变量模板，并生成不同的随机密钥：

```powershell
Copy-Item .env.example .env
$secret1 = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$secret2 = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
```

将 `$secret1` 和 `$secret2` 分别填入 `.env` 中的 `POSTGRES_PASSWORD` 和 `JWT_SECRET`，同时设置管理员用户名和强密码。`.env` 已加入 Git 忽略规则，切勿提交。

启动 API 和 PostgreSQL：

```powershell
docker compose up --build -d
Invoke-RestMethod http://127.0.0.1:8000/api/health
```

首次启动时，API 会运行版本化 SQL 迁移并创建初始管理员。若管理员用户名已被普通用户占用，服务会拒绝启动；请在 `.env` 中改用独立用户名。管理员只在空库首次初始化时创建。

另开终端启动前端：

```powershell
cd frontend
npm install
npm run dev
```

打开 `http://127.0.0.1:5173`。Vite 将 `/api` 转发到 Docker 中的 Go API。不要直接双击 `frontend/index.html`，应先启动 API 与 Vite。

停止服务但保留数据库：

```powershell
docker compose down
```

如需删除本地 PostgreSQL 数据卷并重新开始（会清空新数据库）：

```powershell
docker compose down -v
```

## 验证

```powershell
cd backend
go test ./...
go vet ./...
cd ..\frontend
npm run build
```

后端单元测试不依赖数据库。PostgreSQL API 集成测试使用隔离 schema，需提前在本地 Postgres 创建一次专用测试数据库：

```powershell
docker compose exec db createdb -U hduhelp hduhelp_test
$env:TEST_DATABASE_URL = "postgres://hduhelp:$secret1@127.0.0.1:5432/hduhelp_test?sslmode=disable"
cd backend
go test ./... -run TestPostgresAPIAuthAndModeration -count=1
```

测试会在该数据库中创建并删除独立 schema。请只将专用测试库配置到 `TEST_DATABASE_URL`。

浏览器流程使用 Go API 和 PostgreSQL。建议用专用测试 schema 运行，避免把 E2E 用户和内容写入日常开发数据。设置 API 所用的数据库 URL、管理员账号和密码；E2E 会把其中一条测试内容的审核时间回拨 30 天，再通过页面确认有效性。安装 Playwright 和 psycopg 后启动 E2E Vite：

```powershell
python -m pip install playwright "psycopg[binary]"
python -m playwright install chromium
$env:HDUHELP_E2E_DATABASE_URL = "与 API 使用同一测试 schema 的 PostgreSQL URL"
$env:HDUHELP_E2E_ADMIN_USERNAME = "测试管理员用户名"
$env:HDUHELP_E2E_ADMIN_PASSWORD = "与 .env 中的管理员密码相同"
cd frontend
npm run dev -- --config e2e/vite.config.ts
```

另开终端运行：

```powershell
cd QOKIK/frontend
python e2e/journey.py
```

## 主要流程

1. 访客浏览公开信息，按失物/拾获、处理状态、地点或关键词检索。
2. 用户注册并提交内容；管理员通过审核后公开。修改已通过内容时保留旧版本，审批后再替换。
3. 失主对拾获公告提交认领；知情者对寻物启事发送线索。解释和联系方式仅对请求双方可见。
4. 发布者接受申请后，关联内容进入已找回/已归还状态，其他待处理申请关闭。
5. 用户可举报公开内容或自己参与的私密请求。管理员只有在参与者举报后，才会看到该请求详情。
6. Access JWT 过期后前端使用 Refresh JWT 换新；服务端记录令牌族，发现已消费的刷新令牌被重放时撤销该设备令牌族。

## API 概览

- `/api/auth/*`：注册、登录、JWT 刷新、退出、当前账号
- `/api/posts`：公开检索、查看、发布和更新
- `/api/my/posts`、`/api/my/requests`：个人内容与申请
- `/api/posts/{id}/requests`、`/api/requests/{id}/*`：私密申请处理
- `/api/admin/reviews`、`/api/admin/reports`：审核和举报工作台
- `/api/health`：确认 API 与 PostgreSQL 可用

完整接口路径、请求字段、权限及隐私边界见 [API 文档](API.md)。API 认证使用 `Authorization: Bearer <access_token>`。Refresh JWT 只能通过 HttpOnly Cookie 发送。管理端没有普通用户自助提升管理员权限的接口。生产部署、学校统一认证接入和 ICP 备案仍不在当前实现范围内。
