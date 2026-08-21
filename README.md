# OpenDiscord

可自托管的、Discord 级体验的开源社区通讯平台(早期开发中,当前处于 M0 阶段)。

- 项目规划:[docs/PLANNING.md](docs/PLANNING.md)
- 协议规范(先于实现):[docs/PROTOCOL.md](docs/PROTOCOL.md)

## 当前状态(M0 骨架)

已实现:注册/登录(bcrypt + 不透明 Token)、默认 Guild 与频道、消息收发、
WebSocket Gateway(HELLO/IDENTIFY/READY/心跳/事件广播)、内嵌参考 Web 客户端。
两个浏览器可实时聊天 —— 这是 M0 的验收闭环。

LiveKit 集成假设已用真实 livekit-server 验证(令牌签发 → 客户端入房 → SFU 侧确认),
`internal/voice` 是其模块草图,协议见 PROTOCOL.md §5;语音的 REST/Webhook 尚未接线。

尚未实现(见 PLANNING.md 里程碑):权限/角色、线程、搜索、语音功能本体(M2)、
Bot 平台(M3)。`pgstore` 已通过真实 PostgreSQL 冒烟测试(注册/频道/消息/重启持久化);
docker compose 因开发机无 Docker 尚未整体验证。

## 本地运行(无任何依赖)

```sh
go run ./cmd/opendiscord
```

打开 http://localhost:8080 ,注册两个账号(两个浏览器/无痕窗口),即可实时聊天。
默认使用内存存储,重启丢数据。

## Docker Compose(PostgreSQL 持久化)

```sh
docker compose up --build
```

起 app + PostgreSQL + Redis + MinIO(后两者是既定依赖包络,M1 起被代码消费)。

## 测试

```sh
go test -race ./...
```

需要外部服务的用例在缺少对应环境变量时自动 skip,所以上面这条命令在一台
只有 Go 的机器上也是全绿的。要真正跑它们:

```sh
# store 双实现一致性(memstore 与 pgstore 跑同一套用例)
OD_TEST_POSTGRES_DSN='postgres://…?sslmode=disable' go test ./internal/store/...

# LiveKit 令牌与入房一致性
livekit-server --dev --bind 127.0.0.1
OD_TEST_LIVEKIT_URL=http://127.0.0.1:7880 \
OD_TEST_LIVEKIT_KEY=devkey OD_TEST_LIVEKIT_SECRET=secret go test ./internal/voice
```

## 配置

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `OD_ADDR` | `:8080` | 监听地址 |
| `OD_STORE` | `memory` | `memory` 或 `postgres` |
| `OD_POSTGRES_DSN` | — | `OD_STORE=postgres` 时必填 |
| `OD_NODE_ID` | `0` | Snowflake 节点 ID(0–1023),多实例时唯一 |

## 代码结构

```
cmd/opendiscord/    入口:装配、默认 Guild 种子、READY 组装
internal/httpapi/   REST(PROTOCOL §3)
internal/gateway/   WebSocket 事件流(PROTOCOL §4)
internal/auth/      注册/登录/Token
internal/store/     存储接口 + memstore(内存)/ pgstore(PostgreSQL)
                    storetest/ 是两者共用的一致性测试套件
internal/voice/     LiveKit 令牌签发与房间映射(PROTOCOL §5,草图)
internal/ids/       Snowflake ID
web/                内嵌参考客户端(开发工具,正式客户端在 M1)
docs/               规划与协议规范
```
