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
| `./dev check` | actionlint + gofmt + vet + build + `test -race`(不碰外部服务) |
| `./dev test` / `./dev ci` | `check` 之上自动拉起 PostgreSQL 与 LiveKit,**CI 跑的就是这条** |
| `./dev livekit` | 从源码构建 livekit-server |
| `./dev actionlint` | 装 GitHub Actions 静态检查器(`./dev check` 会用) |
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

`.github/workflows/ci.yml` 刻意写得很薄:**所有检查逻辑都在 `./dev` 里**,
工作流只做 checkout、装 Go、挂一个 PostgreSQL service container,然后跑一行 `./dev ci`。

```sh
./dev act        # 用 Docker 在本地把整个工作流真跑一遍
```

`./dev ci` 会用环境里给定的后端(GitHub 用 service container 提供 PostgreSQL),
缺什么就自己起什么;末尾断言 pgstore 与 voice 套件**确实执行过** ——
它们缺后端时会自动 skip,不断言的话「全绿」可能只是覆盖率空了一块。

### 已验证到什么程度

用 `act` 在 Docker 里完整跑通(Job succeeded),逐步绿:`actions/checkout` →
`actions/setup-go`(把 `go 1.27` 解析成实际版本)→ PostgreSQL service container
(pgstore 套件真的连上了 `127.0.0.1:5432`)→ `./dev ci` 在容器里从源码装好
LiveKit 并启动 → gofmt / vet / build / `test -race` → 跳过断言确认两个外部套件都跑了。

**act 不等于 GitHub**:它用的是 `catthehacker/ubuntu:act-latest` 而非 GitHub 真正的
`ubuntu-latest` 镜像,本机还是 arm64 而 GitHub 是 x86_64。所以这是「高保真复现」,
不是「保证一致」。
