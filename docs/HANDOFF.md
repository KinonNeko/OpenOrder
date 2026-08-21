# 交接文档(→ 下一个会话)

> 目的:让一个全新会话(无本会话上下文)无缝接管 OpenDiscord 开发。
> 最后更新:2026-08-21,LiveKit 集成验证完成时。

## 一、项目是什么

开源、可自托管的 Discord 替代品。完整定位、需求优先级、架构决策、里程碑见
[PLANNING.md](PLANNING.md)(必读);API/Gateway 协议见 [PROTOCOL.md](PROTOCOL.md)(必读,
**协议先于实现**:改行为必须先改协议文档)。

## 二、当前进度

### M0 已完成

- **协议规范 v0**(PROTOCOL.md §1–§4):REST + Gateway(HELLO/IDENTIFY/READY/心跳/关闭码),错误码体系,Snowflake ID
- **Go 模块化单体**:`go build ./... && go vet ./...` 通过,gofmt 干净
  - `internal/httpapi` REST;`internal/gateway` WS 扇出;`internal/auth` bcrypt+不透明 Token
  - `internal/store` 接口 + `memstore`(默认)+ `pgstore`(PostgreSQL)
  - `web/` 内嵌参考客户端(纯 HTML/JS,开发工具性质)
- **实时聊天闭环已验证**:两个浏览器(alice/bob)双向实时收发,中文/emoji 正常
- **pgstore 已用真实 PostgreSQL 18 冒烟验证**:schema 自动建表、注册、发消息、建频道、重启后数据与 Token 持久
- docker-compose.yml + Dockerfile 已写好,但**因开发机无 Docker 未整体验证**

### LiveKit 集成验证已完成(M2 前置探针)

架构决策 C(PLANNING §3.1)的地基假设 **已实测成立**:

- 以 **livekit-server v1.13.5**(darwin/arm64,源码构建)本地起服验证了完整链路:
  后端签发令牌 → 客户端经 signal WS 入房 → **SFU 侧 RoomService 确认该 identity 在位**
- 交付:`internal/voice`(令牌签发 + 房间映射)、PROTOCOL.md **§5 语音章节草案**
- `internal/voice` 的一致性测试是**项目第一批单元测试**,共 8 个,全绿

关键实现选择:`internal/voice` **不依赖 `github.com/livekit/protocol`**。
该库会把 grpc、protobuf、prometheus、otel、zap、redis 客户端(约 40 个传递模块)
拖进单体,只为签一枚 HS256 JWT —— 与 PLANNING §2.3「依赖收敛」冲突。
改为用标准库手写约 40 行 HMAC 签名,**用对真实 livekit-server 的一致性测试兜底**
(`voice_conformance_test.go` 钉住 claim 形状,`join_conformance_test.go` 钉住入房行为)。
**这层测试是手写令牌的唯一安全网,不要删。**

尚未接线:REST 端点(`/channels/{id}/voice/join`)、Webhook 入口、`VOICE_STATE_UPDATE`
事件、`type: 2` 语音频道的建立 —— 这些是 M2 的工作,§5 已把形状定下来。

## 三、开发机环境(重要,都是坑)

- macOS arm64,**无 Homebrew、无 Docker、无 Node**;Go 1.27.0 装在 `~/.local/go`,
  不在 PATH —— 每次构建先 `export PATH=$HOME/.local/go/bin:$PATH`
- **LiveKit 官方 Release 没有 darwin 二进制**(v1.13.5 只有 linux ×3 + windows ×2),
  官方 macOS 路径是 Homebrew,本机没有。解法是源码构建(纯 Go,约 40 秒):

  ```sh
  export PATH=$HOME/.local/go/bin:$PATH GOBIN=$HOME/.local/bin
  go install github.com/livekit/livekit-server/cmd/server@v1.13.5
  mv $HOME/.local/bin/server $HOME/.local/bin/livekit-server
  ```

  注意 main 包在 `cmd/server` 子目录,直接 `go install github.com/livekit/livekit-server@v1.13.5`
  会报 "build constraints exclude all Go files"(仓库根目录只有 magefile)。
- 起 LiveKit 与跑语音测试:

  ```sh
  livekit-server --dev --bind 127.0.0.1     # devkey / secret,HTTP 7880
  OD_TEST_LIVEKIT_URL=http://127.0.0.1:7880 \
  OD_TEST_LIVEKIT_KEY=devkey OD_TEST_LIVEKIT_SECRET=secret \
    go test -v -count=1 ./internal/voice
  ```

  不设这些环境变量时语音一致性测试自动 skip,`go test ./...` 在无 SFU 的机器上照样全绿。
- 无 Docker 时验证 PG:用 Maven Central 的 zonky embedded-postgres 独立二进制
  (darwin-arm64v8 jar 解包出 txz),`initdb` 后以
  `-c unix_socket_directories='' -c listen_addresses=127.0.0.1` 纯 TCP 启动
  (scratchpad 路径太长,unix socket 会失败);精简包只有 initdb/pg_ctl/postgres,
  没有 psql/createdb,直接用默认 `postgres` 库即可
- zsh 脚本里 **`GID` 是特殊变量**,赋值会触发 setgid 报错,换名字
- git 已配置(user: KinonNeko / kinonneko@gmail.com),GitHub SSH 认证可用;
  机器上没有 gh CLI,也没有 HTTPS 凭证 —— **能推送已存在的仓库,不能创建仓库**

## 四、本地跑起来

```sh
export PATH=$HOME/.local/go/bin:$PATH
go run ./cmd/opendiscord        # 内存存储,:8080,开两个浏览器注册两个号即可对聊
```

PG 模式:`OD_STORE=postgres OD_POSTGRES_DSN='postgres://…' go run ./cmd/opendiscord`。
环境变量表在 README.md。

## 五、待决策(接手后请先跟维护者过一遍)

上一会话交接时未记录、本次通读文档与代码时发现的**文档与实现不一致**。
按严重度排,前两条建议优先处理:

1. **`internal/gateway` 有 send-on-closed-channel 竞态(会 panic 整个进程)** ——
   `session.close()` 关闭 `s.send`,而 `readLoop` 里回心跳 ACK 的
   `select { case s.send <- ack: default: }` 不在任何锁内。`close()` 可由 writeLoop
   (写失败)或 `Dispatch` 的 `go sess.close(...)`(慢消费者)触发,与该发送并发。
   `Dispatch` 自身那条路径是安全的(持 `h.mu`、先摘表后关 channel),**只有 ACK 这条漏了**。
   修法:让 close 只标记状态、由 writeLoop 唯一负责 `close(s.send)`;或给 send 加读写锁保护。
2. **Gateway 的 `s` 是 hub 级全局计数器,且每个 READY 都消耗它** ——
   于是任一会话看到的 `s` 天然带空洞。协议 §4.1 说 `s` 是「为 M1 断线重放预留」,
   但按当前语义无法用于重放。**动 `RESUME` 之前必须先在协议里拍板**:per-session 序号,
   还是全局序号 + 重放缓冲区?同时 §4.2 定义的 HEARTBEAT `d`(客户端已收到的最大 `s`)
   当前被服务端完全丢弃 —— 它正是 RESUME 的钩子,一并决策。
3. **`GET /guilds` 语义与实现不符** —— 协议 §3 写「当前用户所在 Guild 列表」,
   实现(`api.go` `handleGuilds` 与 `main.go` `readyBuilder`)调 `store.Guilds(ctx)`,
   无成员过滤,`Store.Guilds` 签名里也没有 user 参数。v0 单 Guild + 全员自动加入下
   结果碰巧相同,但**成员关系这个概念在数据模型里根本不存在**,M1 权限系统必然撞上。
4. **实际错误码未登记进协议**(违反「协议先于实现」铁律)—— 代码会返回
   `username_taken`(409)、`invalid_credentials`(401)、`channel_not_found`(404),
   §1.5 一个都没列;反过来 §1.5 登记的 `not_found`、`forbidden` 代码里从未使用。
   建议:补进 §1.5,并约定「新错误码必须先进协议」。
5. **关闭码 4001 被复用** —— §4.3 只定义 4001 = HELLO 后 10 秒未 IDENTIFY;
   实现在「首帧不是 IDENTIFY」和「JSON 解析失败」时也发 4001,未登记。
6. **`limit` 非法值静默回落** —— `?limit=0`、`?limit=abc`、`?limit=999` 都静默变成 50,
   而非 `validation_failed`,§3 未说明该行为。

## 六、下一步(按优先级)

1. **修 §五 第 1 条的 gateway 竞态**,并补 gateway 生命周期的单元测试(现在有测试基建了)
2. **M1 开工**(PLANNING §3.3):权限/角色(allow/deny 位掩码,进协议)、线程、
   Reaction、未读同步、`MESSAGE_UPDATE/DELETE`、`TYPING_START`、`PRESENCE_UPDATE`。
   开工前先定 §五 第 2、3 条(序号语义、成员关系模型),两者都会被 M1 直接依赖
3. **工程化补课**:CI(build/vet/test);store 接口双实现一致性测试
   (`memstore` / `pgstore` 跑同一组用例);gateway 断线重放(`RESUME`)
4. 装 Docker 后跑一次 `docker compose up --build` 验证整体;compose 里加 livekit 服务
5. M2 语音接线:按 PROTOCOL §5 实现 REST/Webhook/`VOICE_STATE_UPDATE`,
   先答 §5.6 的三个待定问题

## 七、约定(接手后请遵守)

- 协议先行:改 API/事件 → 先改 PROTOCOL.md,再改代码,参考客户端同步更新
- 范围纪律:PLANNING §2.3「明确不做」清单未经讨论不推翻;新想法先记进 PLANNING 再排期
- 所有写操作走 REST,Gateway 只下行(PROTOCOL §3 已固化此规则)
- 错误码 snake_case 稳定不改;`message` 字段可改
- 提交信息用英文,PR/文档中文;代码注释只写代码看不出来的约束
