# OpenDiscord 协议规范(草案 v0)

> 状态:**Draft**,对应里程碑 M0。本文档先于实现,实现必须与本文档一致;不一致视为 bug。
> 变更策略:v0 期间允许破坏性修改,每次修改需同步更新本文档与 `web/` 参考客户端。

本协议由两部分组成:

1. **REST API** —— 资源的创建/查询/修改,请求-响应模型
2. **Gateway** —— WebSocket 事件流,服务端向客户端实时推送状态变更

设计原则(摘自 PLANNING.md):

- 客户端是 API 的普通消费者,不存在"客户端专用私有接口"
- Bot 与人类客户端同构:同一套 REST + Gateway
- 概念与 Discord 对齐(guild / channel / role / message),便于心智迁移,但字段与语义自主定义

---

## 1. 通用约定

### 1.1 基础地址与版本

- REST 前缀:`/api/v0`(v0 = 不稳定草案;首个稳定版将冻结为 `/api/v1`)
- Gateway 地址:通过 `GET /api/v0/gateway` 发现,不硬编码

### 1.2 数据格式

- 请求与响应均为 `application/json; charset=utf-8`
- 时间戳:ISO 8601 UTC 字符串,如 `"2026-08-21T07:12:03.451Z"`

### 1.3 ID(Snowflake)

所有对象 ID 为 64 位无符号整数,**在 JSON 中一律编码为十进制字符串**(避免 JS 精度丢失):

```
| 41 bits: 毫秒时间戳(自定义纪元 2026-01-01T00:00:00Z) | 10 bits: 节点 ID | 12 bits: 序列号 |
```

ID 按时间有序,可直接用于消息分页游标。

### 1.4 认证

- REST:`Authorization: Bearer <token>` 请求头
- Token 由 `/auth/register` 或 `/auth/login` 签发,为不透明字符串,服务端可随时吊销
- Bot Token 与用户 Token 同构(M3 起加 `Bot ` 前缀区分)

### 1.5 错误格式

所有非 2xx 响应携带统一错误体:

```json
{ "code": "channel_not_found", "message": "channel does not exist" }
```

- `code`:机器可读、稳定的错误码(snake_case),客户端据此分支
- `message`:人类可读英文说明,可能变化,不得用于程序判断

通用错误码:`unauthorized`(401)、`forbidden`(403)、`not_found`(404)、
`validation_failed`(400)、`rate_limited`(429,预留)、`internal`(500)。

### 1.6 限流(预留)

v0 不实施限流,但响应头位置预留:`X-RateLimit-Limit` / `X-RateLimit-Remaining` / `X-RateLimit-Reset-After`。客户端从第一天就应处理 429。

---

## 2. 对象模型

### User

```json
{
  "id": "48291057123328",
  "username": "shay",
  "display_name": "Shay",
  "avatar": null,
  "created_at": "2026-08-21T07:00:00Z"
}
```

密码散列、Token 等敏感字段永不出现在任何 API 响应中。

### Guild(服务器)

```json
{
  "id": "48291057123329",
  "name": "General",
  "owner_id": "48291057123328",
  "created_at": "2026-08-21T07:00:00Z"
}
```

v0 实现约束:实例启动时自动创建一个默认 Guild,所有注册用户自动加入。多 Guild、
成员管理、角色/权限(allow/deny 位掩码)在 M1 进入本规范。

### Channel

```json
{
  "id": "48291057123330",
  "guild_id": "48291057123329",
  "type": 0,
  "name": "general",
  "topic": null,
  "position": 0,
  "created_at": "2026-08-21T07:00:00Z"
}
```

`type`:`0` = 文本频道。预留:`1` = DM,`2` = 语音,`3` = 分类,`4` = 公告,`5` = 论坛。

### Message

```json
{
  "id": "48291057200001",
  "channel_id": "48291057123330",
  "author": { "...": "User 对象(嵌入)" },
  "content": "hello world",
  "created_at": "2026-08-21T07:12:03.451Z",
  "edited_at": null
}
```

`content` 为原始文本(客户端负责 Markdown 渲染;服务端不存 HTML)。上限 4000 字符。
预留字段:`attachments`、`reactions`、`reply_to`、`pinned`(M1)。

---

## 3. REST API(v0 实现范围)

### 认证

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/auth/register` | `{username, password}` → `{user, token}`;username 2–32 字符,唯一 |
| POST | `/auth/login` | `{username, password}` → `{user, token}` |
| GET | `/users/@me` | 当前用户 |

### 发现

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/gateway` | `{url}`,Gateway 的 WebSocket 地址 |

### Guild 与频道

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/guilds` | 当前用户所在 Guild 列表 |
| GET | `/guilds/{guild_id}/channels` | 频道列表,按 `position` 排序 |
| POST | `/guilds/{guild_id}/channels` | `{name}` → 创建文本频道;name 1–100 字符,小写化 |

### 消息

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/channels/{channel_id}/messages` | 查询参数 `limit`(默认 50,最大 100)、`before`(消息 ID 游标);按 ID 降序返回 |
| POST | `/channels/{channel_id}/messages` | `{content}` → Message;同时触发 Gateway `MESSAGE_CREATE` |

**写入路径规则(重要)**:所有写操作走 REST,Gateway 仅下行推送。这保证 Bot 与
客户端行为一致、限流点唯一、WebSocket 断线不丢写。

---

## 4. Gateway(WebSocket)

### 4.1 帧格式

单一 JSON 帧:

```json
{ "op": 0, "t": "MESSAGE_CREATE", "s": 42, "d": { } }
```

| 字段 | 说明 |
|---|---|
| `op` | 操作码 |
| `t` | 事件名,仅 `op=0` 时存在 |
| `s` | 服务端递增序号,仅 `op=0` 时存在(为 M1 的断线重放预留) |
| `d` | 载荷 |

### 4.2 操作码

| op | 名称 | 方向 | 说明 |
|---|---|---|---|
| 0 | DISPATCH | S→C | 事件推送 |
| 1 | HEARTBEAT | C→S | 心跳,`d` 为客户端已收到的最大 `s`(可为 null) |
| 2 | IDENTIFY | C→S | 认证,`d = {token}` |
| 10 | HELLO | S→C | 连接建立后首帧,`d = {heartbeat_interval_ms}` |
| 11 | HEARTBEAT_ACK | S→C | 心跳应答 |

### 4.3 连接生命周期

```
客户端                                服务端
  │  ──── WebSocket 握手 ────────────→  │
  │  ←──── op:10 HELLO ───────────────  │   heartbeat_interval_ms = 30000
  │  ──── op:2 IDENTIFY {token} ─────→  │
  │  ←──── op:0 READY ────────────────  │   d = {user, guilds:[{...含 channels}]}
  │  ←──── op:0 MESSAGE_CREATE ───────  │   (此后持续推送)
  │  ──── op:1 HEARTBEAT ────────────→  │   每 interval 发送
  │  ←──── op:11 HEARTBEAT_ACK ───────  │
```

- HELLO 后 10 秒内未 IDENTIFY,服务端关闭连接(关闭码 4001)
- IDENTIFY 的 token 无效 → 关闭码 4004
- 服务端连续 2 个心跳周期未收到 HEARTBEAT → 关闭连接(4009),客户端应重连
- v0 无断线续传:重连即重新 IDENTIFY,并用 REST 回填错过的消息(`before`/`after` 游标);`RESUME` 语义在 M1 加入

### 4.4 事件(v0 实现范围)

| 事件 | 载荷 | 触发 |
|---|---|---|
| `READY` | `{user, guilds}`,guilds 内嵌 `channels` | IDENTIFY 成功后 |
| `MESSAGE_CREATE` | Message 对象 | 有新消息 |
| `CHANNEL_CREATE` | Channel 对象 | 新频道创建 |

M1 规范化队列:`MESSAGE_UPDATE/DELETE`、`TYPING_START`、`PRESENCE_UPDATE`、
`GUILD_MEMBER_ADD/REMOVE`、`MESSAGE_REACTION_ADD/REMOVE`。

### 4.5 扇出语义

v0:事件广播给该 Guild 的所有已认证连接(单默认 Guild = 全体在线连接)。
M1 起按频道权限过滤(权限系统进入规范时同步定义可见性规则)。

---

## 5. 与实现的对应关系

| 规范章节 | 实现位置 |
|---|---|
| REST | `internal/httpapi` |
| Gateway | `internal/gateway` |
| 对象与存储 | `internal/store`(接口)、`memstore` / `pgstore`(实现) |
| Snowflake | `internal/ids` |
| 参考客户端 | `web/index.html` |
