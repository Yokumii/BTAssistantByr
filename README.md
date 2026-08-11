# BTAssistantByr

BTAssistantByr 是对 [BYRBT_RyukawaChihiro](https://github.com/LetMeFly666/BYRBT_RyukawaChihiro) 的 Go 语言二次开发。

当前仓库已完成工程初始化、架构边界定义、严格配置加载、BYR 和 qBittorrent 登录凭证获取、BYR TopFree 种子发现与定期轮询、BYR 下载 URL、qBittorrent 业务适配器、无副作用的 TopFree 容量规划、显式计划执行器、执行批准策略和应用生命周期装配。程序已经可以启动完整流水线，但尚未进行真实环境写操作验证；首次真机运行建议保持 `request_approval`。

## 环境要求

- Go 1.24 或更高版本
- qBittorrent Web API v2（后续实现所需）

## 目录结构

```text
.
├── cmd/btassistantbyr          程序入口
├── config                     配置示例
├── internal
│   ├── adapter
│   │   ├── byr               BYR 站点适配器
│   │   └── qbittorrent       qBittorrent 适配器
│   ├── app                   依赖装配与程序生命周期
│   ├── config                严格 YAML 加载与配置模型
│   ├── domain                核心领域模型
│   ├── port                  外部能力接口
│   └── service/topfree       TopFree 策略编排
├── Makefile
└── go.mod
```

依赖方向保持为 `adapter -> port/domain`、`service -> port/domain`，领域层不依赖站点、BT 客户端或配置文件格式。外部系统细节由适配器实现，应用层只负责装配。

## 开发命令

```bash
make fmt       # 格式化 Go 文件
make fmt-check # 检查 Go 文件格式但不修改
make test      # 运行测试并检查所有包可编译
make vet       # 运行静态检查
make build     # 构建到 bin/btassistantbyr
make check     # 依次执行 test、vet、build
make ci        # 执行格式检查、竞态测试、vet 和构建
```

## 运行

先完成本地配置，然后构建并启动：

```bash
make build
./bin/btassistantbyr -config config/config.yaml
```

也可以直接运行：

```bash
go run ./cmd/btassistantbyr -config config/config.yaml
```

查看当前构建版本：

```bash
./bin/btassistantbyr -version
```

本地构建显示 `dev`，Release 中的二进制显示对应 Git 标签，例如 `v1.2.3` 或 `v1.2.3-alpha.1`。

程序启动时会依次完成配置加载、BYR 会话建立、qBittorrent 登录和依赖装配，随后立即执行第一轮 TopFree 获取与规划，再按 `strategy.refresh_interval` 串行轮询。任何认证、页面解析、客户端请求、策略或执行错误都会终止进程并返回非零退出码；按 `Ctrl+C` 或发送 `SIGTERM` 会正常取消当前请求和轮询。批准提示正在等待输入时也可以直接按 `Ctrl+C` 退出。

本地配置应由 `config/config.example.yaml` 复制为 `config/config.yaml`。真实配置和运行数据均已加入 `.gitignore`，不要提交账号、密码、Cookie 或 passkey。

配置加载不会填充隐式默认值，并且只接受一个不超过 1 MiB 的 YAML 文档。未知字段、缺少必填项、非法 URL、非正数容量、无法解析的轮询间隔、未知执行模式或日志选项都会让启动显式失败。`strategy.max_disk_usage_gib`、`strategy.max_torrent_size_gib` 和 `strategy.min_free_space_gib` 使用 GiB，后者是每轮下载完成后必须保留的真实磁盘可用空间；`strategy.refresh_interval` 使用 Go duration 格式，例如 `121s` 或 `2m`。`tracker.proxy_url`、`tracker.cookie` 和 `client.save_path` 可以留空，`strategy.max_torrent_size_gib` 可以省略。

磁盘安全配置示例：

```yaml
client:
  save_path: ""

strategy:
  max_disk_usage_gib: 500
  max_torrent_size_gib: 100
  min_free_space_gib: 10
```

`strategy.max_torrent_size_gib` 是可选的单种大小上限。省略时不限制；配置后必须是正整数。大小严格超过上限的 TopFree 不会进入待下载队列，等于上限时仍允许下载。该限制只阻止新增，不会因为修改配置而删除仍然 TopFree 的已有任务。

`client.save_path` 留空时，程序从 qBittorrent 读取默认下载路径。每轮严格执行“检测真实可用空间 → 生成策略 → 删除策略允许回收的旧 TopFree 文件 → 再次检测真实可用空间 → 添加队首下载”的流程。下载前要求实际可用空间至少覆盖队首种子的大小和 `min_free_space_gib` 安全余量；检测失败、路径变化、空间不足或删除后空间未按预期释放时都会停止，且不会发送下载请求。

当前网页快照内所有尚未存在于 qBittorrent 的 TopFree 都会进入内存待下载队列。调度器每轮最多添加一个队首任务；只要 qBittorrent 中已有未完成的当前 TopFree，就不会添加下一项。队首按“种子大小 ÷ 剩余 Free 时间”从小到大选择，当前任务完成后，下一轮才会继续。每轮会用最新网页和 qBittorrent 全量快照重建队列，因此程序重启后仍能识别正在下载的任务。升级前若已存在多个仍然 TopFree 的未完成任务，程序只会阻止继续新增，不会擅自删除有效任务。候选不再 TopFree 时会立即出队；若带 `sc` 标签的任务已经开始但尚未完成，则本轮计划会先停止任务，再从 qBittorrent 删除种子及已下载文件。所有这些删除仍受 `execution.mode` 控制。

qBittorrent 管理范围采用严格标签白名单：无标签、仅 `sc`、仅 `toDel`、以及同时带 `sc` 和 `toDel` 的种子可以参与策略。只要包含任何其他标签，即使同时带有 `sc` 或 `toDel`，该种子也完全排除在打标、容量统计、队列并发阻塞、失效清理和删除范围之外。程序仍会识别其 Hash，避免重复添加同一个种子。单独的 `toDel` 被视为用户对删除的明确授权，即使该种子没有 `sc`，也会计入受管容量并在需要空间时成为回收候选；它不会仅因未完成而进入“失去 TopFree”的强制清理。

容量回收延续旧项目的节制删除策略：先按添加时间从早到晚选择种子，直到逻辑容量和真实可用空间都满足下载要求；再从已选列表末尾向前尝试撤销。只有撤销后两项容量约束仍然同时满足，才保留该种子而不删除。例如候选大小依次为 `[1, 2, 7] GiB`、需要释放 `8 GiB` 时，初选三项后会撤销中间的 `2 GiB`，最终只删除 `[1, 7] GiB`。这不是全局组合搜索，但能避免直接删除整个时间前缀造成的非必要损失。

qBittorrent WebAPI 2.15.2 及以上可按任意 `client.save_path` 查询空间。较早的 5.x 版本只公开默认下载盘空间，因此仅在 `save_path` 留空或与 qBittorrent 默认路径一致时安全回退；旧 API 配置其他路径会显式报错，不会使用运行本程序的宿主机空间代替。

`execution.mode` 是必填项，只接受两种模式：

- `request_approval`：每轮存在写操作时，在执行任何 qBittorrent 写请求前展示计划；只有终端输入精确的 `APPLY` 才批准本轮全部写操作，其他输入均取消。
- `yolo`：不请求人工批准，直接执行计划中的打标、删除和下载等写操作，仅适合明确接受自动写入风险的环境。

批准按“单轮完整计划”进行，不会逐条弹出提示；计划摘要会明确列出删除和下载项。无写操作的轮次不会请求批准。

## 日志

日志配置位于 `runtime`：

```yaml
runtime:
  data_dir: "data"
  log_dir: "logs"
  log_level: "info"
  log_color: "auto"
```

`log_level` 是最低输出等级，只接受：

- `debug`：输出源码位置、应用装配、认证方式、轮询轮次、候选与客户端快照、完整策略计划、下载预检以及打标、重新汇报、停止、删除、添加等步骤。
- `info`：输出启动、会话建立、TopFree 快照数量、策略计划汇总、批准结果和执行完成等正常业务事件。
- `warn`：输出 YOLO 无批准写入、用户拒绝执行等需要注意的事件。
- `error`：只输出导致应用失败的错误。

`log_color` 只控制终端输出，只接受：

- `auto`：仅在输出目标是终端时启用颜色，重定向或测试缓冲区不输出 ANSI，推荐使用。
- `always`：始终输出颜色。
- `never`：始终关闭颜色。

颜色映射为 DEBUG 青色、INFO 绿色、WARN 黄色、ERROR 红色。所有日志同时追加写入 `runtime.log_dir/btassistantbyr.log`；文件日志永远不包含颜色控制码，方便 `grep`、采集和后续分析。日志不会记录密码、BYR Cookie、qBittorrent SID、passkey 或带 passkey 的下载 URL。

临时定位问题时可切换为：

```yaml
runtime:
  log_level: "debug"
  log_color: "auto"
```

## 已实现能力

- 使用用户名和密码向 BYR `/api/v2/login.php` 登录，获取 API Access Token、`auth_token` 和 `session_id`
- 严格加载并校验 `config/config.yaml`，完成 GiB 到字节和轮询间隔转换
- 可选通过 `tracker.proxy_url` 为 BYR 请求配置 HTTP、HTTPS 或 SOCKS5 代理
- 使用 Web API 用户名和密码向 qBittorrent `/api/v2/auth/login` 登录，兼容旧版 `SID` 和 v5.2+ `QBT_SID_<端口>` 会话 Cookie
- 校验已有 `auth_token` 或 `SID`，供后续配置加载和会话复用
- 从 BYR 种子列表识别同时处于置顶和 Free/2x Free 状态的 TopFree 种子
- 解析种子 ID、名称、大小、做种/下载人数和 Free 截止时间，并从详情页获取种子 Hash
- 在 BYR 客户端会话内缓存 ID 到 Hash 的映射，减少重复详情页请求
- 从 BYR 用户页面读取并缓存 passkey，按种子 ID 生成 qBittorrent 可用的下载 URL
- 按指定间隔串行轮询 TopFree 列表，支持上下文取消并显式返回获取和处理错误
- 查询指定标签的 qBittorrent 种子，并映射 Hash、名称、总大小、已下载大小、剩余大小、状态、添加时间、保存路径和标签
- 查询 qBittorrent 全量种子快照，确保识别已经存在但尚未打 `sc` 的当前 TopFree
- 通过 qBittorrent 所在端检测实际下载路径可用空间，将安全余量纳入纯策略计算，并在删除完成后复检
- 支持新增 URL 种子、添加标签、重新汇报、停止和删除操作；停止操作使用 qBittorrent 5.x 的 `/api/v2/torrents/stop`
- 支持移除指定 qBittorrent 标签，用于撤销重新成为 TopFree 种子的 `toDel` 状态
- 纯计算 TopFree 标签同步、失效标记、按容量回收、单并发下载队列、大小/剩余 Free 时间优先级和空间不足跳过计划
- 支持可选的单种大小上限，超出限制的候选不会进入下载队列
- 仅管理无标签或只带 `sc`/`toDel` 的种子，包含其他标签的种子不会被修改、计入策略容量或阻塞程序队列
- 将单独的 `toDel` 视为用户授权的容量回收候选；回收初选后反向撤销仍可省略的删除项
- 每轮按最新 TopFree 快照剔除失效候选；已开始但未完成的失效 `sc` 任务会停止并连同已下载内容删除
- 容量规划只允许回收已不再 TopFree 的 `sc` 种子或用户明确标记的纯 `toDel` 种子，当前 TopFree 和带其他标签的种子不会进入删除计划
- 显式执行已校验的计划；任何写入前先预取队首下载 URL 并复核空间，清理完成且空间复检通过后才添加队首任务，并在首个错误处停止
- 支持 `request_approval` 和 `yolo` 两种执行模式；批准模式在整轮写计划执行前要求终端输入 `APPLY`，拒绝时不会触达具体执行器
- 使用标准库结构化日志，支持终端分级颜色、无 ANSI 文件日志和不泄露凭证的 DEBUG 全流程记录
- 单轮协调器串联全量客户端快照、纯策略规划和显式执行，并提供可直接交给定期轮询器的 Handler
- 对缺少账号密码、无效服务地址、非预期状态码和未返回凭证分别返回显式错误

认证模块不会持久化凭证，也不会把 Cookie 或 SID 写回配置文件。`tracker.cookie` 非空时会将其作为已有 BYR `auth_token` 使用；留空时使用用户名和密码重新登录。qBittorrent 每次启动都会使用用户名和密码获取新的 SID。

BYR 需要代理时，可在本地配置中指定代理地址；留空则不显式设置代理：

```yaml
tracker:
  base_url: "https://byr.pt"
  proxy_url: "http://127.0.0.1:10808"
```

该代理只作用于 BYR 适配器，不会代理本机 qBittorrent 请求。

## CI/CD

仓库包含两个 GitHub Actions 工作流：

- `CI / verify`：任意分支发生 push，以及 PR 创建、重新打开或提交更新时运行 `make ci`，覆盖 `gofmt` 检查、全量竞态测试、`go vet` 和构建。
- `Release / build-and-publish`：推送符合规则的 `v*` 标签时再次执行完整 CI，校验标签所属分支，构建多平台压缩包、生成 `SHA256SUMS` 并发布 GitHub Release。

工作流触发 CI 并不会自动修改仓库的合并权限。要强制 PR 在 CI 成功前不能合并，需要在 GitHub 仓库的 Rulesets 或 Branch protection 中，为 `main` 和 `dev` 启用“Require status checks to pass”，并选择 `CI / verify` 作为必需检查。

发布标签遵循以下规则：

- 正式版本：`vMAJOR.MINOR.PATCH`，例如 `v1.2.3`；标签提交必须包含在 `main` 中，创建普通 Release。
- 测试版本：`vMAJOR.MINOR.PATCH-alpha[.N]`、`beta[.N]` 或 `rc[.N]`，例如 `v1.2.3-alpha.1`；标签提交必须包含在 `dev` 中，创建 GitHub Pre-release。
- 普通 `main` 或 `dev` push 只运行 CI。只有推送版本标签才会打包和发布，避免每次提交都产生 Release。

发布测试版本：

```bash
git switch dev
git pull --ff-only origin dev
git tag -a v1.2.3-alpha.1 -m "v1.2.3-alpha.1"
git push origin dev
git push origin v1.2.3-alpha.1
```

发布正式版本时，先确保目标提交已经进入 `main`：

```bash
git switch main
git pull --ff-only origin main
git tag -a v1.2.3 -m "v1.2.3"
git push origin main
git push origin v1.2.3
```

每个 Release 包含以下平台：

- Linux：`amd64`、`arm64`
- macOS：`amd64`、`arm64`
- Windows：`amd64`、`arm64`

Unix 平台使用 `.tar.gz`，Windows 使用 `.zip`。每个压缩包还包含 `LICENSE`、`README.md` 和 `config.example.yaml`。

## 开发阶段

1. 工程脚手架与领域边界（已完成）
2. BYR 与 qBittorrent 登录凭证获取（已完成）
3. BYR TopFree 种子发现与定期轮询（已完成）
4. BYR 下载 URL 和 qBittorrent 业务适配器（已完成）
5. 配置加载与校验（已完成）
6. TopFree 标签、下载与空间管理规划、显式执行器及执行批准策略（已完成）
7. 应用生命周期装配与命令行入口（已完成）
8. 真实环境受控验证与部署

## 许可

本项目沿用上游项目的 MIT License，并保留原作者版权声明，详见 [LICENSE](LICENSE)。
