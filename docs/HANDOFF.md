# 交接文档(→ 下一个会话)

> 目的:让一个全新会话(无本会话上下文)无缝接管 OpenDiscord 开发。
> 最后更新:2026-08-21,M0 完成时。

## 一、项目是什么

开源、可自托管的 Discord 替代品。完整定位、需求优先级、架构决策、里程碑见
[PLANNING.md](PLANNING.md)(必读);API/Gateway 协议见 [PROTOCOL.md](PROTOCOL.md)(必读,
**协议先于实现**:改行为必须先改协议文档)。

## 二、当前进度(M0 已完成)

已交付并验证:

- **协议规范 v0**(PROTOCOL.md):REST + Gateway(HELLO/IDENTIFY/READY/心跳/关闭码),错误码体系,Snowflake ID
- **Go 模块化单体**:`go build ./... && go vet ./...` 通过,gofmt 干净
  - `internal/httpapi` REST;`internal/gateway` WS 扇出;`internal/auth` bcrypt+不透明 Token
  - `internal/store` 接口 + `memstore`(默认)+ `pgstore`(PostgreSQL)
  - `web/` 内嵌参考客户端(纯 HTML/JS,开发工具性质)
- **实时聊天闭环已验证**:两个浏览器(alice/bob)双向实时收发,中文/emoji 正常
- **pgstore 已用真实 PostgreSQL 18 冒烟验证**:schema 自动建表、注册、发消息、建频道、重启后数据与 Token 持久
- docker-compose.yml + Dockerfile 已写好,但**因开发机无 Docker 未整体验证**

## 三、开发机环境(重要,都是坑)

- macOS arm64,**无 Homebrew、无 Docker、无 Node**;Go 1.27.0 装在 `~/.local/go`,
  不在 PATH —— 每次构建先 `export PATH=$HOME/.local/go/bin:$PATH`
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

## 五、下一步(按优先级)

1. **LiveKit 集成验证**(PLANNING"立即下一步"第 4 条,唯一未做的一条):本地起
   livekit-server(无 Docker 可下 GitHub Releases 的 darwin 二进制),验证:后端签发
   Room JWT → 客户端入房。产出:`internal/voice` 模块草图 + PROTOCOL.md 语音章节草案
2. **M1 开工**(PLANNING §3.3):权限/角色(allow/deny 位掩码,进协议)、线程、
   Reaction、未读同步、`MESSAGE_UPDATE/DELETE`、`TYPING_START`、`PRESENCE_UPDATE`
3. **工程化补课**(M0 欠账):单元测试(目前为零,先覆盖 store 接口双实现一致性 +
   gateway 生命周期)、CI(build/vet/test)、给 gateway 加断线重放(`RESUME`,协议已预留 `s` 序号)
4. 装 Docker 后跑一次 `docker compose up --build` 验证整体

## 六、约定(接手后请遵守)

- 协议先行:改 API/事件 → 先改 PROTOCOL.md,再改代码,参考客户端同步更新
- 范围纪律:PLANNING §2.3"明确不做"清单未经讨论不推翻;新想法先记进 PLANNING 再排期
- 所有写操作走 REST,Gateway 只下行(PROTOCOL §3 已固化此规则)
- 错误码 snake_case 稳定不改;`message` 字段可改
- 提交信息用英文,PR/文档中文;代码注释只写代码看不出来的约束
