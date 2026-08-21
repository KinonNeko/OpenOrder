# 交接文档(→ 下一个会话)

> 目的:让一个全新会话(无本会话上下文)无缝接管 OpenOrder 开发。
> 最后更新:2026-08-21,改名 OpenOrder 时。

## 〇、项目改过名(2026-08-21)

**`OpenDiscord` → `OpenOrder`**,在公开仓库之前完成。

原因是商标:`Discord` 是 Discord Inc. 的注册商标,拿它命名一个**同品类竞品**属于
风险最高的一档(同类产品 + 近似标识 = 典型的混淆可能性),而 `Open` 前缀反而加重
指向性。同赛道有先例:Fosscord 改名为 Spacebar,普遍认为正是出于这类压力。
(以上不构成法律意见。)

**注意这个区分**:用 Discord 来*描述*本项目是没问题的(「Discord 替代品」
「概念与 Discord 对齐」属于指称性使用),PLANNING 的竞品分析里 27 处 `Discord`
是刻意保留的 —— 不要在后续改名清理中把它们一起替换掉。有问题的只是拿它当**产品名**。

改名同时修掉了一个既有错误:旧 module path 是 `github.com/opendiscord/opendiscord`,
指向一个**并不属于我们的 GitHub 组织**,别人 `go get` 根本拿不到代码。现在是
`github.com/KinonNeko/openorder`,与真实仓库一致。

一并改掉的标识:环境变量前缀 `OD_` → `OO_`,Token 前缀 `odt_` → `oot_`,
二进制与目录 `cmd/openorder/`,PG 库名/用户名,LiveKit 管理身份 `openorder-server`,
本地 PG 二进制目录 `~/.local/share/openorder/pg`。

> 遗留项(不影响功能):本地工作目录仍叫 `~/ClaudeProjects/OpenDiscord`;
> GitHub 会**永久保留**旧仓库 URL 的重定向,想彻底断开关联只能新建仓库并删除旧的。
> git 历史里也仍带旧名(改名前的提交),这是刻意保留的 —— 早期改名很常见,
> 而那些提交信息记录了大量决策理由,压掉可惜。

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

### 工程化(2026-08-21)

- **CI 已建**(见 §四),**但工作流本身未在本地验证过**
- **`./dev` 开发入口**(见 §三):一条命令起服 + 演示账号;`./dev test` 会自动
  拉起 PostgreSQL 与 LiveKit 跑全量测试
- HANDOFF 旧版「待决策」的六项不一致**已全部修复**,决策理由见 §七

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

## 三、开发入口:`./dev`

**先看这个,再看下面的环境坑** —— 下面那些坑,`./dev` 大部分已经替你处理了。

```sh
./dev            # 起服 + 建 alice/bob 演示账号 + 开浏览器,零依赖
./dev check      # actionlint + gofmt + vet + build + test -race(不碰外部服务)
./dev ci         # CI 跑的就是这条:check + 自动拉起 PG/LiveKit + 断言没被 skip
./dev test       # ci 的别名
./dev doctor     # 本机有什么、缺什么、端口占没占
./dev livekit    # 从源码装 livekit-server
./dev actionlint # 装 GitHub Actions 静态检查器
./dev act        # 用 Docker 在本地真跑一遍工作流
./dev clean      # 清掉 .dev/
```

脚本自己找 Go(含 `~/.local/go`)、找 PostgreSQL(`~/.local/share/openorder/pg`,
可用 `OO_PG_BIN` 覆盖)、找 livekit-server。端口冲突用 `OO_PORT=` 换。
本地状态都在 `.dev/`(已 gitignore)。

写脚本时踩到并已修的两个坑,改它时别踩回去:

- **`set -e` 下 trap 会中途夭折** —— `kill` 一个已经死掉的 pid 返回非零,
  trap 就此中断,后面的 `pg_stop` 永远不执行,PostgreSQL 被遗留。
  所以 `cleanup()` 里第一行是 `set +e`。
- **`go run` 不可靠地转发信号** —— 它把服务器起成孙子进程,Ctrl-C 只杀掉
  `go run` 本身,真正占着端口的二进制活得好好的。现在改为先 `go build` 到
  `.dev/openorder` 再直接执行,信号链才是通的(顺带让重启变快)。

## 四、CI

**结构:逻辑在 `./dev`,YAML 只剩样板。** 工作流做三件事 —— checkout、装 Go、
挂一个 PostgreSQL service container —— 然后跑一行 `./dev ci`。

这么拆的起因是「本机没 Docker 没 act,工作流验证不了」:逻辑留在 YAML 里,
推上去之前就是零验证。搬进 shell 脚本后它能在笔记本上逐条跑,剩下的样板交给
`actionlint`。**后来 Docker 装上了,`act` 也就能用了**,于是整个工作流现在可以
在本地真跑:

```sh
./dev act        # 拉镜像(如缺)、必要时装 act、在 Docker 里跑完整工作流
```

### 已验证(2026-08-21,`act` 实跑 Job succeeded)

逐步绿:`actions/checkout@v4` → `actions/setup-go@v5`(`go-version-file: go.mod`
把 `go 1.27` 解析成实际版本)→ PostgreSQL service container(pgstore 套件真的连上
`127.0.0.1:5432`)→ `./dev ci` 在容器里 `go install` 装好 LiveKit 并启动 →
gofmt / vet / build / `test -race` → `assert_suites_ran` 确认两个外部套件都没被 skip。

**这一跑当场抓到一个真问题**:容器里没有 actionlint,workflow lint 那步会降级成
一句 warning —— 也就是说 CI 根本没在校验工作流,正是 `assert_suites_ran` 想防的那种
「静默空洞」。已修:`cmd_ci` 现在像装 LiveKit 一样确保 actionlint 存在,重跑后
该步输出 `ok workflows clean`。

### act 的三个坑(都已在 `.actrc` 里处理,改动前先读那个文件)

1. **不指定镜像会弹交互式选择器**,非交互环境下直接 EOF 退出 → `.actrc` 里钉死
   `catthehacker/ubuntu:act-latest`
2. **Docker Desktop 的 `credsStore: desktop`** 让 act 拉镜像时带上空凭证,报
   `authentication required`,而同一个镜像 `docker pull` 匿名拉完全正常 →
   `.actrc` 里 `--pull=false`,镜像由 `./dev act` 用普通 `docker pull` 预先备好
3. **act ≠ GitHub**:镜像是 `catthehacker/ubuntu:act-latest` 而非 GitHub 真正的
   `ubuntu-latest`,且本机 arm64 / GitHub x86_64。**高保真复现,不是保证一致** ——
   推上去之后仍然值得看一眼 Actions

> 没有 shellcheck(Haskell 二进制,本机装不了),actionlint 不分析 `run:` 里的
> shell;但重构后 YAML 里只剩 `run: ./dev ci` 一行,这个缺口基本被消掉了。

## 五、开发机环境(重要,都是坑)

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
  OO_TEST_LIVEKIT_URL=http://127.0.0.1:7880 \
  OO_TEST_LIVEKIT_KEY=devkey OO_TEST_LIVEKIT_SECRET=secret \
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

## 六、本地跑起来

```sh
export PATH=$HOME/.local/go/bin:$PATH
go run ./cmd/openorder        # 内存存储,:8080,开两个浏览器注册两个号即可对聊
```

PG 模式:`OO_STORE=postgres OO_POSTGRES_DSN='postgres://…' go run ./cmd/openorder`。
环境变量表在 README.md。

## 七、已定的协议决策(2026-08-21,六项一次性补齐)

上一版本 HANDOFF 的「待决策」一节列了六项「文档与实现不一致」待决策。**已全部修复并验证**,
决策与理由记录如下 —— 它们都写进了 PROTOCOL.md,这里只解释「为什么这样选」。

1. **gateway send-on-closed-channel 竞态(会 panic 整个进程)** —— 已修。
   `close()` 不再关闭 `s.send`,改为关闭新增的 `done` channel;`writeLoop` 用
   `select` 在 `done` 与 `send` 之间退出,`readLoop` 的心跳 ACK 也加了 `done` 分支。
   `send` **刻意永不关闭**:readLoop 可能正在往里投 ACK,在别处关它就是那个 panic。
   回归测试 `TestCloseLeavesSendChannelOpen` 直接断言这条不变式 —— 把 `close(s.send)`
   加回去,它每次都 panic;早先写的并发洪泛测试反而抓不到(窗口太窄),已换掉。

2. **`s` 序号语义 → 每连接独立、从 1 起、连续无空洞**(PROTOCOL §4.1)。
   没选「全局序号 + 重放缓冲」,因为 §4.5 规定 M1 起事件按频道权限过滤,届时全局
   序号会让**每个**客户端都看到大量空洞,「序号跳变 = 丢帧」这个判据就废了。
   每连接编号是唯一能在权限过滤下存活的方案,也是 `RESUME` 能按序号重放的前提。
   代价:每个会话单独封帧(载荷只序列化一次)。HEARTBEAT 的 `d` 现在是**接受但不使用**,
   协议已明写 —— 它是 M1 `RESUME` 的重放起点,客户端必须从现在起就正确填写。

3. **成员关系 → 显式存储**(PROTOCOL §2)。新增 `guild_members` 表 /
   `Store.AddGuildMember` / `Store.GuildsByUser`;注册时自动加入当时已存在的全部 Guild。
   `GET /guilds`、READY、Gateway 扇出**都按成员关系过滤**(`Dispatch` 现在带 guild 参数)。
   没有选「改协议措辞去迁就实现」,因为 M1 的角色/权限必须挂在成员关系上 ——
   留到那时再补,等于让权限系统先建在一个不存在的实体上。
   pgstore 的 `schema.sql` 里有一段**一次性回填**,给 M0 时期注册、没有成员行的老账号补上;
   **M1 做「退出 Guild」时必须删掉它**,否则每次启动都会把退出的人加回来(注释已写在 SQL 旁)。

4. **错误码 → §1.5 改为全集注册表**,并写死「服务端不得返回未登记的 `code`,
   新增错误码必须先改协议」。补登了 `username_taken` / `invalid_credentials` /
   `channel_not_found`;`not_found` / `forbidden` 标注为预留。
   `invalid_credentials` 与 `unauthorized` 刻意都用 401 但分开:前者「重填表单」,
   后者「清 Token 回登录页」,合并会让客户端无法区分处置。

5. **关闭码 4002 = 协议错误**,从 4001 拆出(PROTOCOL §4.3)。4001 只表示
   「没来得及发 IDENTIFY」(重连有用),4002 表示「发错了」(重连只会再撞一次)。

6. **`limit` 非法值 → `validation_failed`**,不再静默回落(PROTOCOL §3)。
   静默回落会把分页 Bug 表现成「少了几条消息」,而不是一个明确的失败。

### 本次新增的测试(此前全项目只有 voice 的 8 个)

| 包 | 覆盖 |
|---|---|
| `internal/gateway` | 握手、每连接序号、成员过滤扇出、4001/4002/4004、心跳 ACK、竞态回归 |
| `internal/httpapi` | 成员过滤、错误码注册表(12 例)、`limit` 校验、默认分页 |
| `internal/store/storetest` | **一套用例,memstore 与 pgstore 共跑** —— 两者可互换是设计前提,只有共用测试能保住 |

`go test ./...` 在无 PostgreSQL / 无 LiveKit 的机器上全绿(相关用例自动 skip)。
本次已用真实 PostgreSQL 跑通 store 一致性测试,并用一个端到端程序驱动真实服务器
验证了全部六项(含回填:删空 `guild_members` → 重启 → 成员关系恢复)。

## 八、下一步(按优先级)

1. **对一眼 GitHub 上的 Actions 结果**:本地 `./dev act` 已跑绿,但 act 用的镜像与
   架构和 GitHub 不同(见 §四),真跑一次仍值得确认
2. **M1 开工**(PLANNING §3.3):权限/角色(allow/deny 位掩码,先进协议)、线程、
   Reaction、未读同步、`MESSAGE_UPDATE/DELETE`、`TYPING_START`、`PRESENCE_UPDATE`。
   §七 第 2、3 条已为它铺好路(序号语义、成员关系),可直接开工
3. **`RESUME`**(PROTOCOL §4.3 已承诺 M1 加入):每连接重放缓冲 + HEARTBEAT `d`
   作为重放起点。序号语义已定,剩下的是缓冲区大小与失效策略
4. 装 Docker 后跑一次 `docker compose up --build` 验证整体;compose 里加 livekit 服务
5. M2 语音接线:按 PROTOCOL §5 实现 REST/Webhook/`VOICE_STATE_UPDATE`,
   先答 §5.6 的三个待定问题

### 参考客户端的已知毛刺(不挡路,但会绊到测试的人)

- 频道尚未选中时(READY 到达前的一瞬)按回车发消息,会被**静默丢弃**:
  `composer` 的 submit 处理器里 `if (!content || !state.current) return;`
  既不发送也不提示,输入框里的字还在。窗口极短,但表现是「打了字按回车没反应」。
  修法:选中频道前禁用输入框,或给出可见反馈。
- 回车发送此前依赖浏览器的隐式表单提交,在自动化环境下完全不触发。现已改为
  显式 `keydown` 处理器(带 `isComposing` 判断,否则中文输入法组词途中按回车会误发)。

## 九、约定(接手后请遵守)

- 协议先行:改 API/事件 → 先改 PROTOCOL.md,再改代码,参考客户端同步更新
- 范围纪律:PLANNING §2.3「明确不做」清单未经讨论不推翻;新想法先记进 PLANNING 再排期
- 所有写操作走 REST,Gateway 只下行(PROTOCOL §3 已固化此规则)
- 错误码 snake_case 稳定不改;`message` 字段可改
- 提交信息用英文,PR/文档中文;代码注释只写代码看不出来的约束
