# API 接口说明

本文档对应当前 Go + Gin API。默认本地地址为 `http://127.0.0.1:8000`，所有 JSON 请求使用 `Content-Type: application/json`。路径参数中的 `:id` 为正整数。

## 认证与通用约定

- 受保护接口使用 `Authorization: Bearer <access_token>`。Access JWT 有效期 15 分钟。
- 登录、注册和刷新成功时，JSON 返回 `access_token`、`token_type`（`Bearer`）、`expires_in`（秒）及 `user`。Refresh JWT 通过 `HttpOnly`、`SameSite=Strict` 的 `hduhelp_refresh` Cookie 下发，有效期 7 天；前端不读取或保存该 Cookie。
- 调用刷新或退出接口时，浏览器需携带 Cookie。刷新还要求 `Origin` 在服务端允许列表内；刷新令牌每次轮换，检测到已消费令牌重放会撤销对应令牌族。
- 用户对象字段：`id`、`username`、`nickname`、`role`、`student_number`、`student_number_verified`。当前学号仅作为账号标识，`student_number_verified` 为 `false`，尚未接入学校统一认证。
- 通用错误 JSON 为 `{ "detail": "错误说明" }`。常见状态码：`401` 未登录/会话失效，`403` 无权限或来源不允许，`404` 资源不存在，`409` 当前状态冲突，`422` 输入无效，`500` 服务端处理失败；健康检查在数据库不可用时返回 `503`。
- 可见内容遵循权限控制：公开信息不返回作者学号、私密核验特征、认领联系方式或认领答案。详情接口仅在请求者为发布者时返回该条公告的私密核验特征；认领答案只对申请者本人、公告发布者可见，管理员仅在参与者举报该申请后可查看。

## 健康检查与账号

| 方法与路径 | 权限 | 请求 | 成功响应 |
|---|---|---|---|
| `GET /api/health` | 公开 | 无 | `200 {"status":"ok"}` |
| `POST /api/auth/register` | 公开 | `username`（2–32 字符，仅字母数字及 `_.-`）、`password`（10–128 字符）、`nickname`（1–40 字符）、`student_number`（4–32 字符） | `201` 会话响应并设置 Refresh Cookie；重复用户名或学号返回 `409` |
| `POST /api/auth/login` | 公开 | `username`、`password` | `200` 会话响应并设置 Refresh Cookie |
| `POST /api/auth/refresh` | Refresh Cookie + 允许的 `Origin` | 无 JSON 字段 | `200` 新会话响应并轮换 Cookie |
| `POST /api/auth/logout` | Refresh Cookie（失效时也可调用） | 无 | `200 {"ok":true}` 并清除 Cookie |
| `GET /api/auth/me` | 用户 | 无 | 当前用户对象 |

## 公共信息与个人公告

公告字段包括 `id`、`kind`（`lost`/`found`）、`item_name`、`description`、`category`、`location`、`event_time`、`lifecycle_status`、`moderation_status`、`rejection_reason`、`withdrawn`、`created_at`、`approved_at`、`author_nickname`。学号不对公众公开。寻物公告创建时状态为“寻找中”，拾获公告为“待认领”。

| 方法与路径 | 权限 | 请求 | 成功响应 |
|---|---|---|---|
| `GET /api/posts` | 公开 | Query：`limit`（1–100，默认 20）、`offset`（默认 0）、`kind`、`status`、`location`（子串）、`q`（物品名或描述关键词） | `200 {"items":[公告],"total":总数,"limit":页大小,"offset":偏移}` |
| `GET /api/posts/:id` | 公开 | 无 | `200` 公告；不存在或未公开时 `404` |
| `GET /api/posts/:id/recommendations` | 公开 | 无 | `200 {"items":[最多5条匹配公告]}`；不满足条件时为空数组 |
| `POST /api/posts` | 用户 | `kind`、`item_name`（1–100）、`description`（5–3000）、可选 `category`、`location`、`event_time`、`private_verification_detail`（仅拾获公告可填，最多300） | `201` 新公告，进入待审核 |
| `GET /api/my/posts` | 用户 | 无 | `200 [公告]`，含本人学号和待审修改信息 |
| `PUT /api/posts/:id` | 公告作者 | 与创建相同的公告字段 | `200`；已公开公告的编辑作为待审核版本提交，审批前旧版本继续展示 |
| `POST /api/posts/:id/status` | 公告作者 | `{ "status": "寻找中" | "已找回" | "待认领" | "已归还" | "已结束" }`，状态值需与公告类型匹配 | `200` 更新后的公告；结束或解决时关闭未处理申请 |
| `POST /api/posts/:id/confirm` | 公告作者 | 无 | `200` 更新后的公告，重置“待确认”期限 |
| `DELETE /api/posts/:id` | 公告作者 | 无 | `200 {"ok":true,"withdrawn":布尔值}`；有关联申请或举报时转为下架，否则删除 |

相似线索仅在公告为有效状态时计算，候选必须是相反类型、审核通过、未下架、未过期、处于活跃状态且由其他用户发布的公告。名称、类别、地点匹配产生理由并参与排序，最多返回 5 条；不使用 AI。超过 30 天未确认的活跃内容仍可浏览，但不参加推荐。该期限从审核通过或作者最近一次确认起算。

## 线索与认领申请

| 方法与路径 | 权限 | 请求 | 成功响应 |
|---|---|---|---|
| `POST /api/posts/:id/requests` | 已登录用户 | `explanation`（5–2000）、`contact_method`（2–120）；如果拾获公告作者设置了私密核验特征，则还必须提供 `verification_answer`（1–500） | `201` 申请对象；寻物公告对应线索，拾获公告对应认领 |
| `GET /api/my/requests` | 用户 | 无 | `200 [本人提交的申请]`，包含本人答案 |
| `GET /api/posts/:id/requests` | 公告作者 | 无 | `200 [该公告申请]`，包含申请者昵称、联系方式及答案 |
| `GET /api/requests/:id` | 申请者或公告作者 | 无 | `200` 申请详情；其他用户返回 `403` |
| `POST /api/requests/:id/withdraw` | 申请者 | 无 | `200 {"ok":true}`；仅待处理申请可撤回 |
| `POST /api/requests/:id/accept` | 公告作者 | 无 | `200` 申请对象；公告变为已找回/已归还，其他待处理申请关闭 |
| `POST /api/requests/:id/reject` | 公告作者 | `{ "reason": "处理原因" }`（3–1000） | `200` 申请对象 |

既有拾获公告若没有私密核验特征，认领继续使用普通说明流程。作者核验特征和申请答案不公开；普通管理员也不能浏览未举报申请的私密内容。

## 举报与管理后台

| 方法与路径 | 权限 | 请求 | 成功响应 |
|---|---|---|---|
| `POST /api/posts/:id/reports` | 已登录用户（不能举报自己的公告） | `{ "explanation": "举报说明" }`（5–2000） | `201 {"id":编号,"status":"待处理"}` |
| `POST /api/requests/:id/reports` | 该申请参与者 | 同上 | `201` 举报摘要；此后管理员举报队列可显示该申请的私密详情 |
| `GET /api/admin/reviews` | 管理员 | 无 | `200 {"posts":[待审新公告],"revisions":[待审修改]}` |
| `POST /api/admin/reviews/:id/approve` | 管理员 | 无 | `200` 已批准公告；修改版本会在批准时整体替换旧版本 |
| `POST /api/admin/reviews/:id/reject` | 管理员 | `{ "reason": "驳回原因" }`（3–1000） | `200 {"ok":true}` |
| `POST /api/admin/posts/:id/take-down` | 管理员 | `{ "reason": "下架原因" }`（3–1000） | `200 {"ok":true}` |
| `GET /api/admin/reports` | 管理员 | 无 | `200 [举报及目标摘要]`；私密申请内容仅在该申请被参与者举报后出现 |
| `POST /api/admin/reports/:id/dismiss` | 管理员 | `{ "reason": "处理原因" }`（3–1000） | `200 {"ok":true}` |
| `POST /api/admin/reports/:id/remove` | 管理员 | `{ "reason": "处理原因" }`（3–1000） | `200 {"ok":true}` 并下架公告或移除申请 |

初始管理员由服务端环境变量创建；普通注册接口不能申请管理员角色。管理操作会写入审计日志。
