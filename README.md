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

## 快速开始

```sh
./dev
```

构建、起服、建好 `alice` / `bob` 两个演示账号(密码都是 `password123`)、打开浏览器。
用两个窗口(一个正常 + 一个无痕,以免共用会话)分别登录这两个账号,在一边输入,
另一边**无需刷新**即时出现。默认内存存储,退出即丢数据。

`./dev` 是本项目唯一的开发入口,原因是这台开发机有几处容易踩的坑
(Go 不在 PATH、没有 Docker、PostgreSQL 与 LiveKit 缺席时测试会静默 skip),
它会替你处理掉,或者明确告诉你缺什么:

| 命令 | 作用 |
|---|---|
| `./dev` | 起服 + 演示账号 + 开浏览器(等同 `./dev demo`) |
| `./dev run` | 只起服,内存存储,零依赖 |
| `./dev run --pg` | 起服 + 本地 PostgreSQL(数据可跨重启) |
| `./dev check` | gofmt + vet + build + `test -race` —— **与 CI 完全一致** |
| `./dev test` | 在 `check` 基础上自动拉起 PostgreSQL 与 LiveKit,跑全量测试 |
| `./dev livekit` | 从源码构建 livekit-server(`./dev test` 需要) |
| `./dev doctor` | 报告本机有什么、缺什么、端口是否被占 |
| `./dev clean` | 清掉本地开发状态(`.dev/`) |

不想用脚本的话,底层等价于:

```sh
export PATH=$HOME/.local/go/bin:$PATH
go run ./cmd/opendiscord        # 内存存储,:8080
```

## Docker Compose(PostgreSQL 持久化)

```sh
docker compose up --build
```

起 app + PostgreSQL + Redis + MinIO(后两者是既定依赖包络,M1 起被代码消费)。

## 测试

```sh
./dev test      # 自动拉起 PostgreSQL 与 LiveKit,跑全量;跑完自己收拾干净
./dev check     # 只跑不需要外部服务的部分(= CI 的内容)
```

需要外部服务的用例在缺少对应环境变量时**自动 skip**,所以 `go test ./...`
在一台只有 Go 的机器上也是全绿的 —— 这也意味着「全绿」不等于「全跑了」。
`./dev test` 结尾会明确告诉你哪些套件真正执行了、哪些被跳过;CI 则直接把
跳过判为失败,避免覆盖率悄悄出现空洞。

手工等价命令:

```sh
OD_TEST_POSTGRES_DSN='postgres://…?sslmode=disable' go test ./internal/store/...

livekit-server --dev --bind 127.0.0.1
OD_TEST_LIVEKIT_URL=http://127.0.0.1:7880 \
OD_TEST_LIVEKIT_KEY=devkey OD_TEST_LIVEKIT_SECRET=secret go test ./internal/voice
```

## CI

`.github/workflows/ci.yml`:gofmt / vet / build / `go test -race`,并起真实的
PostgreSQL 与 LiveKit 让对应套件真正执行。**注意:本机没有 Docker,该工作流
从未在本地验证过,首次推送后需要看一眼 Actions 结果。**

## 配置

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `OD_ADDR` | `:8080` | 监听地址 |
| `OD_STORE` | `memory` | `memory` 或 `postgres` |
| `OD_POSTGRES_DSN` | — | `OD_STORE=postgres` 时必填 |
| `OD_NODE_ID` | `0` | Snowflake 节点 ID(0–1023),多实例时唯一 |

## 代码结构

```
dev                 开发入口脚本(见「快速开始」)
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
