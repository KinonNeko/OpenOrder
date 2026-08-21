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

**错误码注册表**。下表即全集:服务端**不得返回未登记的 `code`**,新增错误码必须
先改本表 ——「协议先于实现」同样适用于错误码。

| `code` | HTTP | 含义 |
|---|---|---|
| `validation_failed` | 400 | 请求体或查询参数不合法 |
| `unauthorized` | 401 | 缺少 Token,或 Token 无效 / 已吊销 |
| `invalid_credentials` | 401 | 用户名或密码错误(仅 `/auth/login`) |
| `forbidden` | 403 | 已认证但无权限(预留,M1 权限系统启用后生效) |
| `not_found` | 404 | 通用资源不存在(预留;已有专用码的资源必须用专用码) |
| `channel_not_found` | 404 | 频道不存在 |
| `username_taken` | 409 | 注册时用户名已被占用 |
| `rate_limited` | 429 | 触发限流(预留,见 §1.6) |
| `internal` | 500 | 服务端内部错误,`message` 不暴露细节 |

`invalid_credentials` 与 `unauthorized` 刻意分开,尽管都是 401:前者是「这次登录尝试
失败」,客户端应提示重填表单;后者是「你手上的凭证不再有效」,客户端应清除本地 Token
并回到登录页。合成一个码会让客户端无法区分这两种截然不同的处置。

§5(语音)另行登记 `voice_disabled` / `not_a_voice_channel` / `voice_forbidden` /
`voice_channel_full`,随该章实现时生效。

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

**成员关系是显式存储的对象**,不是「所有用户都是所有 Guild 的成员」这条隐含规则。
`GET /guilds` 与 Gateway `READY` 都按成员关系过滤,事件扇出(§4.5)同理。之所以在
v0 就把它建出来,是因为 M1 的角色/权限必须挂在成员关系上 —— 留到那时再补,等于让
权限系统先建在一个不存在的实体上。

v0 实现约束:实例启动时自动创建一个默认 Guild;用户**注册时自动加入当时已存在的
全部 Guild**(v0 即那一个)。加入/退出 Guild 的接口、邀请、成员列表、角色与
allow/deny 位掩码在 M1 进入本规范。

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
| GET | `/guilds` | 当前用户所在的 Guild 列表,按 ID 升序 |
| GET | `/guilds/{guild_id}/channels` | 频道列表,按 `position` 排序 |
| POST | `/guilds/{guild_id}/channels` | `{name}` → 创建文本频道;name 1–100 字符,小写化 |

### 消息

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/channels/{channel_id}/messages` | 查询参数 `limit`(缺省 50,取值 1–100)、`before`(消息 ID 游标);按 ID 降序返回 |
| POST | `/channels/{channel_id}/messages` | `{content}` → Message;同时触发 Gateway `MESSAGE_CREATE` |

**查询参数校验**:非法的查询参数一律返回 `validation_failed`,**不静默回落到缺省值**。
`?limit=0`、`?limit=abc`、`?limit=999` 都是错误,而不是「当作 50」—— 静默回落会让
分页 Bug 表现为「少了几条消息」,而不是一个明确的失败。省略参数才用缺省值。

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
| `s` | **本连接上**的递增序号,从 1 开始、连续无空洞,仅 `op=0` 时存在 |
| `d` | 载荷 |

`s` 是**每连接独立**的,不是全局计数器。这是刻意的:§4.5 规定 M1 起事件按频道权限
过滤,届时若 `s` 取全局值,每个客户端都会看到大量空洞,也就无从用「序号跳变」判断
自己是否漏收。每连接连续编号,客户端只需比较相邻两帧的 `s` 即可检测丢失,这也是
`RESUME` 能按序号重放的前提。READY 是本连接的第 1 帧,`s` 恒为 `1`。

### 4.2 操作码

| op | 名称 | 方向 | 说明 |
|---|---|---|---|
| 0 | DISPATCH | S→C | 事件推送 |
| 1 | HEARTBEAT | C→S | 心跳,`d` 为客户端在**本连接**上已收到的最大 `s`(尚未收到任何事件时为 `null`) |
| 2 | IDENTIFY | C→S | 认证,`d = {token}` |
| 10 | HELLO | S→C | 连接建立后首帧,`d = {heartbeat_interval_ms}` |
| 11 | HEARTBEAT_ACK | S→C | 心跳应答 |

v0 服务端**接受但不使用** HEARTBEAT 的 `d` —— 它是 `RESUME` 的重放起点,要到 M1
实现 `RESUME` 时才产生作用。客户端从第一天起就应正确填写,否则 M1 上线时所有旧
客户端都无法续传。

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

关闭码:

| 关闭码 | 触发 | 客户端应对 |
|---|---|---|
| 4001 | HELLO 后 10 秒内未收到 IDENTIFY | 重连并及时 IDENTIFY |
| 4002 | 协议错误:帧不是合法 JSON,或 IDENTIFY 之前收到了其他操作码 | 视为客户端 Bug,不应重试 |
| 4004 | IDENTIFY 的 token 无效或已吊销 | 清除本地 Token,回到登录页 |
| 4009 | 连续 2 个心跳周期未收到 HEARTBEAT | 重连 |

4001 与 4002 分开,是为了让「客户端没来得及发」和「客户端发错了」在服务端日志与
客户端处置上都能区分 —— 前者重连有用,后者重连只会再撞一次。

v0 无断线续传:重连即重新 IDENTIFY,并用 REST 回填错过的消息(`before` 游标);
`RESUME` 语义在 M1 加入,届时以 §4.1 的每连接 `s` 为重放起点。

### 4.4 事件(v0 实现范围)

| 事件 | 载荷 | 触发 |
|---|---|---|
| `READY` | `{user, guilds}`,guilds 内嵌 `channels` | IDENTIFY 成功后 |
| `MESSAGE_CREATE` | Message 对象 | 有新消息 |
| `CHANNEL_CREATE` | Channel 对象 | 新频道创建 |

M1 规范化队列:`MESSAGE_UPDATE/DELETE`、`TYPING_START`、`PRESENCE_UPDATE`、
`GUILD_MEMBER_ADD/REMOVE`、`MESSAGE_REACTION_ADD/REMOVE`。

### 4.5 扇出语义

v0:事件广播给该 Guild **成员**的所有已认证连接(§2 的成员关系是过滤依据;
v0 单默认 Guild + 注册即加入,实际等于全体在线连接)。
M1 起在成员关系之上再按频道权限过滤(权限系统进入规范时同步定义可见性规则)。

---

## 5. 语音(草案 · M2)

> 状态:**Draft,未实现**。本章先于实现落笔(见文首变更策略)。
> 其中的**集成假设已实测验证**:2026-08-21,以 livekit-server v1.13.5
> (darwin/arm64,源码构建)验证「后端签发令牌 → 客户端入房 → SFU 侧确认参与者在位」,
> 见 `internal/voice` 的一致性测试。接口形状仍可变。

语音基于自托管 LiveKit(PLANNING §3.1 决策 C)。本章定义主系统与 LiveKit 的
职责边界,以及客户端看到的那一面。

### 5.1 模型:房间是频道的投影

语音频道即 `type: 2` 的 Channel,在 LiveKit 中对应一个 Room,命名 `ch_<channel_id>`。

该映射是**全函数、无状态**的:房间名由频道 ID 直接算得,反之亦然。服务端
**不存储**「频道 ↔ 房间」对照表 —— 没有对照表,就没有对不上的可能。房间由
LiveKit 在首个参与者加入时惰性创建;频道删除时服务端显式销毁房间。

### 5.2 授权边界(本章最重要的约束)

**LiveKit 不做任何授权判断。** 权限一律由主系统裁决,裁决结果编码进一枚短时效
签名令牌,LiveKit 只执行我们已经做出的决定。

由此推出三条硬性规则:

- 客户端**不得**直接向 LiveKit 申请令牌,任何时候都拿不到 API Secret
- 令牌 TTL 以分钟计(默认 5 分钟)。它是一次「入房许可」,不是会话凭证 ——
  握手完成后会话由 LiveKit 维持,令牌过期**不影响**已在房内的参与者
- 参与者 identity 恒为**用户 Snowflake**,不是 username(username 可变,identity 必须稳定)。
  副作用恰是我们想要的:LiveKit 对同一 identity 在同一房间只允许一个参与者,
  于是「同一账号不能重复占用同一语音频道」无需额外实现

### 5.3 REST

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/channels/{channel_id}/voice/join` | 申请入房凭证 |
| DELETE | `/channels/{channel_id}/voice/join` | 主动离开语音(幂等) |

POST 响应:

```json
{
  "url": "wss://livekit.example.org",
  "token": "<JWT>",
  "room": "ch_48291057123330",
  "expires_at": "2026-08-21T07:17:03.451Z",
  "grants": { "publish": true, "subscribe": true, "publish_data": true }
}
```

`grants` 是服务端裁决结果的**回显**,仅供客户端渲染 UI(如 `publish: false` 时
禁用麦克风按钮)。它**不具备安全意义** —— 真正的强制写在令牌里,客户端篡改
回显不会让 LiveKit 多放行任何东西。

错误码:

| 错误码 | HTTP | 触发 |
|---|---|---|
| `voice_disabled` | 503 | 本实例未配置 LiveKit |
| `not_a_voice_channel` | 400 | 频道 `type` ≠ 2 |
| `voice_forbidden` | 403 | 无该频道的连接权限(M1 权限系统裁决) |
| `voice_channel_full` | 409 | 达到 `user_limit` |

Channel 对象在本章实现时新增(仅 `type: 2` 有意义):`user_limit`(整数,`null` = 不限)。
预留:`bitrate`。

### 5.4 语音状态与 Gateway 事件

谁在哪个语音频道里,**以 LiveKit 为准**,服务端只做镜像。理由是掉线、超时、
网络切换只有 SFU 看得见 —— 服务端自行记账必然与实际漂移。

LiveKit 经 Webhook 通知服务端参与者进出,服务端转译为 Gateway 事件广播:

| 事件 | 载荷 | 触发 |
|---|---|---|
| `VOICE_STATE_UPDATE` | VoiceState 对象 | 加入 / 离开 / 静音状态变化 |

```json
{
  "user_id": "48291057123328",
  "channel_id": "48291057123330",
  "self_mute": false,
  "self_deaf": false,
  "server_mute": false
}
```

`channel_id` 为 `null` 表示该用户已离开语音。

Webhook 入口:`POST /api/v0/livekit/webhook`,以 LiveKit 的 `Authorization` 头
(同一 API Key/Secret 签名)校验;**该端点不接受用户 Token**,也不在 `/auth` 保护范围内。

READY 载荷在本章实现时追加 `voice_states` 字段(当前 Guild 内全部语音状态),
使客户端一上线即可渲染语音频道内的人。

### 5.5 审核动作

服务器静音、踢出语音、移动成员(PLANNING §2.1)由客户端走 REST 发起,服务端
用一枚**管理令牌**调用 LiveKit 的 RoomService 执行。管理令牌永不下发客户端,
单次调用现签、有效期数十秒。

| 动作 | LiveKit 调用 |
|---|---|
| 服务器静音 | `MutePublishedTrack` |
| 踢出语音 | `RemoveParticipant` |
| 移动成员 | 目标房间重新签发令牌 + 源房间 `RemoveParticipant`(LiveKit 无原生移动语义,由服务端组合) |

### 5.6 待定(实现前需拍板)

1. **`user_limit` 的强制点**:服务端签发前检查(存在 TOCTOU 窗口),还是依赖
   LiveKit Room 的 `max_participants`(需在房间创建时设定,而房间是惰性创建的)?
   倾向后者 —— 由服务端显式创建房间并带上上限,放弃惰性创建。
2. **多节点部署下的 Webhook 路由**:Webhook 落到哪个实例不确定,而 Gateway 连接
   分散在各实例,需要 Redis 扇出(Redis 已是 PLANNING §3.2 的既定依赖)。
3. **视频 / 屏幕共享的 grant 粒度**:当前 `publish` 是麦克风、摄像头、屏幕共享的
   合一开关。Discord 将「视频」与「直播/屏幕共享」拆为独立权限位;是否跟进,
   须在 M1 权限位掩码定型时一并决定 —— 位掩码一旦发布就难改。

---

## 6. 与实现的对应关系

| 规范章节 | 实现位置 |
|---|---|
| REST | `internal/httpapi` |
| Gateway | `internal/gateway` |
| 对象与存储 | `internal/store`(接口)、`memstore` / `pgstore`(实现) |
| Snowflake | `internal/ids` |
| 语音(§5) | `internal/voice`(令牌签发与房间映射;REST/Webhook 尚未接线) |
| 参考客户端 | `web/index.html` |
