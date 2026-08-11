# AGENTS.md

本文档面向维护者和自动化编码 Agent。面向最终用户的安装、配置、运行和故障排查说明只放在 `README.md`。

## 项目目标

BTAssistantByr 是对 `BYRBT_RyukawaChihiro` 的 Go 语言二次开发。当前应用已经具备完整的 TopFree 发现、规划、人工批准/自动执行、串行下载、容量回收和生命周期轮询能力。

工程优先级依次为：磁盘与数据安全、行为显式、低复杂度、可测试、可运维。不要以静默兜底、吞错或隐式默认值掩盖真实故障。

## 技术栈与入口

- Go 版本：以 `go.mod` 为准，当前为 Go 1.24。
- 程序入口：`cmd/btassistantbyr/main.go`。
- 配置示例：`config/config.example.yaml`。
- 本地真实配置：`config/config.yaml`，已加入 `.gitignore`，不得提交。
- 用户文档：`README.md`。
- Release 构建：`scripts/build-release.sh`。
- Release 标签校验：`scripts/release-metadata.sh`。

## 目录与职责

```text
.
├── cmd/btassistantbyr          CLI 入口、信号和顶层生命周期
├── config                     配置示例
├── internal
│   ├── adapter
│   │   ├── byr               BYR 登录、页面访问和解析
│   │   └── qbittorrent       qBittorrent Web API 适配器
│   ├── app                   依赖装配与应用生命周期
│   ├── config                严格 YAML 加载、校验和运行时配置模型
│   ├── domain                与外部系统无关的领域模型
│   ├── logging               结构化日志与终端颜色
│   ├── netretry              瞬时网络错误分类和逐操作重试装饰器
│   ├── port                  Tracker 和 TorrentClient 等外部能力接口
│   └── service/topfree       队列、策略、批准、执行与轮询
├── scripts                    Release 元数据和多平台打包脚本
├── Makefile
└── go.mod
```

依赖方向保持为：

- `domain` 不依赖配置、站点或 BT 客户端。
- `port` 只描述外部能力边界。
- `service/topfree` 依赖 `domain`/`port`，不直接依赖具体 HTTP 适配器。
- `adapter` 实现外部协议与 `port`。
- `app` 只负责创建依赖、应用配置并装配生命周期。

不要为了单个功能增加额外分层、容器或通用框架。优先扩展现有小接口和明确数据结构。

## 必须保持的业务不变量

### 配置

- YAML 使用 `KnownFields` 严格解析，只接受一个不超过 1 MiB 的文档。
- 不添加未记录的隐式默认值；可选字段必须在配置模型和 README 中明确标注。
- 容量配置使用 GiB，并在加载阶段检查正数和字节换算溢出。
- 时间间隔使用正数 Go duration。
- 任何配置变更必须同步更新 `config/config.example.yaml`、加载器测试和 README。

### 管理范围

- 只管理无标签、仅 `sc`、仅 `toDel`、或同时带 `sc`/`toDel` 的种子。
- 只要存在其他标签，该种子必须完全排除在修改、容量统计、队列阻塞和删除范围之外；仍需识别 Hash，避免重复添加。
- `sc` 表示程序管理的种子。
- 纯 `toDel` 表示用户明确授权的容量回收候选，但不因未完成而自动进入失去 TopFree 的强制清理。
- 当前仍 TopFree 的种子不得作为容量回收候选。

### 队列与规划

- 一次最多下载一个种子；已有未完成的当前 TopFree 时不得添加下一项。
- 队列使用实际 BYR/qBittorrent 快照重建，不能只依赖进程内状态。
- 候选优先级保持为“种子大小 ÷ 剩余 Free 时间”从小到大。
- 单种大小限制只阻止新增，不因配置变化删除仍然 TopFree 的已有任务。
- 容量回收先按添加时间从早到晚选择，再从末尾反向撤销不必要项；不要退化为删除整个前缀或所有候选。

### 磁盘安全

- 下载链路顺序必须保持为：真实空间检测 → 策略 → 获准删除 → 真实空间复检 → 下载。
- 所有空间数据必须来自 qBittorrent 所在下载路径，不能使用运行本程序的宿主机空间代替。
- 下载前真实可用空间必须覆盖候选大小和 `min_free_space_gib`。
- qBittorrent 删除文件可能异步完成；执行器每秒复检，最多等待 30 秒，空间实际到账前不得调用 Add。
- 删除后仍不足属于可延后的队列状态：停止本轮并在下个 `queue_check_interval` 重算，不终止进程。
- 已经完成的外部写操作不做伪回滚；错误和部分执行状态必须显式记录。

### 写操作授权

- `request_approval` 必须在任何具体 qBittorrent 写请求前展示完整计划，并要求精确输入 `APPLY`。
- `yolo` 才允许无人工确认执行写操作。
- 批准粒度是一轮完整计划，不要在计划执行过程中追加未经批准的新动作。
- 任何真实环境写测试都必须由用户明确授权；默认只做纯策略、本地模拟和只读验证。

### 网络与会话

- 网络重试以单个外部调用为单位，不得重放完整策略轮次。
- 仅连接错误、超时、HTTP 408/425/429/5xx 等瞬时故障进入重试；认证拒绝、解析、配置、策略和批准错误保持显式。
- BYR 刷新重试耗尽后必须使旧快照失效，并等待下个刷新周期，不能基于旧数据写入。
- qBittorrent 队列调用重试耗尽后停止当前链路，等待下个队列周期。
- BYR 会话过期后使用配置用户名密码续期，原子替换内存 Cookie，只重试触发过期的读取请求。
- 并发读取同时发现 BYR 会话过期时只能执行一次续期。
- 不得在日志中记录密码、BYR Cookie、qBittorrent SID、passkey 或私有下载 URL。

## 开发命令

```bash
make fmt       # 格式化 Go 文件
make fmt-check # 检查格式但不修改
make test      # 运行全部测试
make test-race # 运行竞态测试
make vet       # 运行 go vet
make build     # 构建 bin/btassistantbyr
make check     # test + vet + build
make ci        # fmt-check + test-race + vet + build
```

验证顺序：先运行改动对应的包测试，再运行 `make ci`。涉及发布、入口或平台行为时，还要执行：

```bash
release_check_dir=$(mktemp -d /tmp/btassistantbyr-release-check.XXXXXX)
./scripts/build-release.sh v0.0.0-alpha.0 "$release_check_dir"
```

发布脚本应生成 Linux、macOS、Windows 的 amd64/arm64 六个平台压缩包，以及 `SHA256SUMS`。

## 测试约定

- 纯策略优先使用表驱动单元测试，覆盖边界、冲突和反向回滚。
- 外部协议使用 `httptest.Server`，不得依赖真实 BYR/qBittorrent 才能完成 CI。
- 涉及时间的测试必须注入或固定时钟，不得依赖当前日期、时区边界或真实睡眠。
- 重试/轮询测试应使用可控短间隔，并验证上下文取消。
- 写链路测试必须断言调用顺序，并证明错误发生后没有继续执行 Add/Delete 等后续动作。
- 凭证和日志测试应主动断言敏感值未出现在输出中。

## CI/CD 与发布

`.github/workflows/ci.yml` 在任意分支 push 和所有 pull request 上运行 `make ci`。GitHub 仓库 Rulesets/Branch protection 应将 `CI / verify` 配置为 `main` 和 `dev` 的必需检查。

`.github/workflows/release.yml` 只在推送 `v*` 标签时运行，重新执行 CI、校验标签所属分支、构建多平台产物并创建 GitHub Release：

- 正式版：`vMAJOR.MINOR.PATCH`，标签提交必须包含在 `main`，创建普通 Release。
- 预发布版：`vMAJOR.MINOR.PATCH-alpha[.N]`、`beta[.N]` 或 `rc[.N]`，标签提交必须包含在 `dev`，创建 Pre-release。
- 普通 `main`/`dev` push 只运行 CI，不发布 Release。

预发布示例：

```bash
git switch dev
git pull --ff-only origin dev
git tag -a v1.2.3-alpha.1 -m "v1.2.3-alpha.1"
git push origin dev
git push origin v1.2.3-alpha.1
```

正式发布示例：

```bash
git switch main
git pull --ff-only origin main
git tag -a v1.2.3 -m "v1.2.3"
git push origin main
git push origin v1.2.3
```

## 当前开发状态

已完成：

1. 工程脚手架和领域边界。
2. BYR 与 qBittorrent 登录、会话和代理。
3. TopFree 页面发现、Hash/passkey 获取与双节拍轮询。
4. qBittorrent 5.x 适配和磁盘空间查询。
5. 严格配置、结构化日志和网络重试。
6. TopFree 标签同步、串行队列、容量规划和反向回滚。
7. 显式执行器、磁盘安全复检、人工批准/YOLO 模式。
8. 应用生命周期、CI 和多平台 Release。

后续工作以真实环境受控验证、问题修复和部署完善为主。不要把静态阅读或模拟测试描述成真实环境验证。

## 文档维护

- `README.md` 面向使用者：只写安装、配置、运行、行为、安全边界和故障排查。
- `AGENTS.md` 面向开发者/Agent：写架构、工程不变量、开发命令、测试、CI/CD 和开发状态。
- 用户可观察行为改变时更新 README；内部边界、验证要求或发布流程改变时更新 AGENTS.md；两者同时受影响时都更新。
