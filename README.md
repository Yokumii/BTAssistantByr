# BTAssistantByr

BTAssistantByr 是一个面向 BYRBT 用户的 TopFree 自动管理工具。它会定期获取当前 TopFree 种子，通过 qBittorrent Web API 维护串行下载队列，并在磁盘空间不足时按受控策略回收程序管理的旧种子。

> [!WARNING]
> 本程序能够打标签、停止任务、删除种子及文件并添加下载。首次使用请保持 `execution.mode: "request_approval"`，逐项核对计划后再输入 `APPLY`。不要在不了解删除范围时启用 `yolo`。

## 主要能力

- 自动发现 BYR 当前置顶且 Free/2x Free 的 TopFree 种子。
- 一次只下载一个种子，当前任务完成后再推进队列。
- 默认按“种子大小 ÷ 剩余 Free 时间”从小到大选择；同体积时，剩余时间更长的候选优先。
- 可选按预计魔力净收益选择下载，并在回收旧种时计入魔力收益损失。
- 可限制单个种子的最大体积。
- 只管理无标签或仅带 `sc`/`toDel` 标签的种子，不触碰带其他标签的任务。
- 下载前检查 qBittorrent 下载盘的真实可用空间，并保留指定安全余量。
- 支持人工批准和全自动两种写入模式。
- 支持 HTTP、HTTPS、SOCKS5 代理访问 BYR。
- 网络请求自动重试；BYR 会话过期后自动重新登录。
- 提供分级、彩色终端日志和无颜色文件日志。

## 运行要求

- 一个可正常登录的 BYRBT 账号。
- 已启用 Web UI 的 qBittorrent，建议使用 5.x 版本。
- Linux、macOS 或 Windows，支持 `amd64` 和 `arm64`。
- 如果本机无法直连 BYR，需要准备可用的 HTTP、HTTPS 或 SOCKS5 代理。

程序通过 qBittorrent Web API 检查下载路径所在磁盘的空间。WebAPI 2.15.2 及以上可以查询任意路径；较早版本只能可靠查询默认下载盘，因此旧版本应将 `client.save_path` 留空，或确保它与 qBittorrent 默认下载路径一致。

## 快速开始

### 使用 Release

从 [GitHub Releases](https://github.com/Yokumii/BTAssistantByr/releases) 下载与系统和架构匹配的压缩包并解压。Windows 使用 `.zip`，Linux 和 macOS 使用 `.tar.gz`。

Release 同时提供 `SHA256SUMS`，可用来核对下载文件的完整性。带 `alpha`、`beta` 或 `rc` 后缀的版本为预发布版；稳定使用请选择正式版。

Release 压缩包内已经包含可执行文件和 `config.example.yaml`。先复制配置：

Linux/macOS：

```bash
cp config.example.yaml config.yaml
```

Windows PowerShell：

```powershell
Copy-Item .\config.example.yaml .\config.yaml
```

编辑 `config.yaml` 后启动：

Linux/macOS：

```bash
./btassistantbyr -config config.yaml
```

Windows PowerShell：

```powershell
.\btassistantbyr.exe -config .\config.yaml
```

`-config` 的相对路径以启动时的工作目录为基准。省略该参数时固定读取 `config/config.yaml`；Release 中复制得到的 `config.yaml` 需要通过 `-config` 指定。使用 Windows Terminal 配置启动命令或进程守护工具时，请同时固定工作目录和参数，例如：

```powershell
Set-Location 'D:\BTAssistantByr'
.\btassistantbyr.exe -config 'D:\BTAssistantByr\config.yaml'
```

### 从源码运行

源码构建需要 Go 1.24 或更高版本：

```bash
cp config/config.example.yaml config/config.yaml
go run ./cmd/btassistantbyr -config config/config.yaml
```

查看版本：

```bash
./btassistantbyr -version
```

本地源码构建显示 `dev`；Release 二进制显示对应版本标签，例如 `v1.2.3` 或 `v1.2.3-alpha.1`。

## 配置

完整配置示例：

```yaml
tracker:
  base_url: "https://byr.pt"
  # 需要代理时可填写 "http://127.0.0.1:10808"
  proxy_url: ""
  username: "你的 BYR 用户名"
  password: "你的 BYR 密码"
  cookie: ""

client:
  base_url: "http://127.0.0.1:8080"
  username: "qBittorrent Web UI 用户名"
  password: "qBittorrent Web UI 密码"
  save_path: ""

strategy:
  priority: "free_time"
  max_disk_usage_gib: 500
  max_torrent_size_gib: 100
  min_free_space_gib: 10
  refresh_interval: "121s"
  queue_check_interval: "10s"

execution:
  mode: "request_approval"

runtime:
  data_dir: "data"
  log_dir: "logs"
  log_level: "info"
  log_color: "auto"
  log_max_size_mib: 20
  log_max_backups: 5
  network_retry_count: 3
```

配置文件使用严格校验：未知字段、缺少必填项、无效 URL、非法容量、无法解析的时间间隔或未知枚举值都会阻止程序启动。真实配置已经被 Git 忽略，不要把用户名、密码、Cookie、SID 或 passkey 提交到仓库或 Issue。

### BYR

| 配置项 | 说明 |
| --- | --- |
| `tracker.base_url` | BYR 地址，通常为 `https://byr.pt`。 |
| `tracker.proxy_url` | 可留空；支持 `http`、`https`、`socks5` 和 `socks5h`。代理只作用于 BYR，不代理本机 qBittorrent。 |
| `tracker.username` | BYR 用户名，必填。 |
| `tracker.password` | BYR 密码，必填。 |
| `tracker.cookie` | 可选的已有 `auth_token`。仅在没有可用本地会话缓存时使用；过期后程序会用用户名和密码重新登录。 |

BYR 凭据按“`runtime.data_dir/byr_session.json` 会话缓存 → `tracker.cookie` → 用户名密码登录”的优先级获取。使用用户名和密码登录或重新登录成功后，程序会以受限权限更新会话缓存，后续重启直接复用同一个长期会话，避免账户下不断新增会话记录。缓存绑定 `tracker.base_url` 和 `tracker.username`，切换站点或账号时不会误用；它只保存页面请求需要的 `auth_token` 和 `session_id`，不会保存密码、passkey 或 API Access Token，也不会修改配置文件。该文件属于敏感凭据，请限制 `runtime.data_dir` 的访问权限，不要复制到日志、Issue 或公开备份中。

缓存或配置 Cookie 仍可能被用户或服务端撤销。请求被 BYR 判定为会话过期时，程序会继续按上述优先级尝试下一级凭据；最终使用用户名密码建立新的长期会话、覆盖缓存，然后重试刚才失败的读取请求。缓存文件损坏或无法安全读写时会显式报错，不会静默创建新会话。

### qBittorrent

| 配置项 | 说明 |
| --- | --- |
| `client.base_url` | qBittorrent Web UI 地址，例如 `http://127.0.0.1:8080`。 |
| `client.username` | Web UI 用户名。 |
| `client.password` | Web UI 密码。 |
| `client.save_path` | 下载路径；留空时使用 qBittorrent 默认下载路径。Windows YAML 可使用单引号，例如 `'F:\种子'`。 |

程序兼容旧版 `SID` 和 qBittorrent 5.2+ 的 `QBT_SID_<端口>` 会话 Cookie。

### 下载和磁盘策略

| 配置项 | 说明 |
| --- | --- |
| `strategy.priority` | 可选：`free_time` 或 `bonus`；省略时为 `free_time`，保持原有下载排序与回收规则。`bonus` 按完成下载后的预计魔力净收益选择候选。 |
| `strategy.max_disk_usage_gib` | 程序管理范围允许占用的最大容量，单位 GiB，必须大于 0。 |
| `strategy.max_torrent_size_gib` | 可选的单种大小上限，单位 GiB；省略时不限制。等于上限仍允许下载，超过上限不进入队列。 |
| `strategy.min_free_space_gib` | 下载完成后必须保留的真实可用空间，单位 GiB，必须大于 0。 |
| `strategy.refresh_interval` | 重新访问 BYR、更新 TopFree 快照的间隔，使用 Go duration，例如 `121s` 或 `10m`。 |
| `strategy.queue_check_interval` | 使用最近一次有效快照检查 qBittorrent 并推进队列的间隔，例如 `10s`。 |

BYR 刷新成功后会立即检查一次队列。刷新重试仍失败时，旧快照会立即失效；下一次刷新成功前不会基于旧数据执行任何策略。

下载前严格执行：

```text
检查真实可用空间
→ 生成策略
→ 删除获准回收的种子及文件
→ 再次检查真实可用空间
→ 添加一个队首种子
```

实际可用空间必须覆盖“队首种子大小 + `min_free_space_gib`”。qBittorrent 在 Windows 等平台上可能异步删除文件，因此删除后程序会每秒复检一次、最多等待 30 秒。空间没有实际到账时绝不会添加下载；等待后仍不足则结束本轮，下一次队列检查重新规划，主进程不会因此退出。

### 写操作模式

`execution.mode` 必须选择以下一种：

- `request_approval`：推荐。每轮存在写操作时先显示完整计划，只有输入精确的 `APPLY` 才执行；其他输入均取消。
- `yolo`：不请求确认，直接执行打标签、停止、删除和下载等操作。

批准以一轮完整计划为单位，不会逐条询问。无写操作的轮次不会出现批准提示。按 `Ctrl+C` 或发送 `SIGTERM` 可以取消当前请求、等待或批准提示。

### 日志和重试

| 配置项 | 说明 |
| --- | --- |
| `runtime.data_dir` | 运行数据目录；包含敏感的 BYR 会话缓存 `byr_session.json`，不要共享或公开。 |
| `runtime.log_dir` | 文件日志目录，日志文件为 `btassistantbyr.log`。 |
| `runtime.log_max_size_mib` | 可选，单个日志文件的大小上限，单位 MiB，必须为正整数且换算字节不溢出；省略时为 `20`。 |
| `runtime.log_max_backups` | 可选，保留的轮转备份数量，范围 `1..100`；省略时为 `5`。 |
| `runtime.log_level` | `debug`、`info`、`warn` 或 `error`；同时控制终端和文件。日常运行使用 `info`，排查问题时使用 `debug`。 |
| `runtime.log_color` | `auto`、`always` 或 `never`；推荐 `auto`。 |
| `runtime.network_retry_count` | 额外网络重试次数，范围 `0..100`；配置 `3` 表示总共最多尝试 4 次。 |

相邻网络重试固定等待 1 秒。连接错误、超时、HTTP `408`、`425`、`429` 和 `5xx` 会重试；权限拒绝、页面解析失败、配置错误、策略错误和用户拒绝批准不会被当成网络故障。

BYR TopFree 列表页无法解析时，程序会记录包含解析原因的 WARN、使旧快照失效，并等待下一个 `strategy.refresh_interval` 再读取。它不会把异常页面视为空列表，也不会在恢复前使用旧快照推进队列；解析失败本身不触发网络重试。若持续出现 `invalid BYR torrent page: torrents table missing`，表示响应中缺少种子列表表格，应检查站点是否维护、代理是否返回异常页面以及账号能否正常打开种子页；该错误本身不能确定是会话过期。

重试以单个 BYR/qBittorrent 调用为单位，不会重放整轮删除和下载计划。运行期间重试耗尽后，程序会跳过当前刷新或队列轮次并等待下一次调度。已经被远端确认完成的写操作不会自动回滚。

终端使用紧凑格式，依次展示本地日期时间（毫秒精度）、等级、模块、消息和 `key=value` 参数。例如：

```text
2026-11-06 16:59:00.742 DEBUG [topfree_coordinator] qBittorrent management scope applied in_scope=21 out_of_scope=76  (topfree/coordinator.go:132)
2026-11-06 16:59:00.743 DEBUG [topfree_coordinator] qBittorrent disk space checked before planning save_path=/downloads free=128.50GiB reserve=20.00GiB  (topfree/coordinator.go:105)
```

启用颜色时，仅等级使用彩色，时间和源码位置使用弱化样式。`debug` 配置下，所有日志的末尾附带缩短后的源码位置；其他等级配置不展示源码位置。磁盘空间等已知容量字段在终端转换为保留两位小数的 GiB，仅用于展示，精确字节数以文件日志为准。消息和参数中的换行、终端控制字符会转义，避免打断单条日志。

`info` 主要记录启动、停止、批准、实际完成的标签修改、删除和新增下载，以及执行结果；常规 TopFree 快照刷新、空间查询、大小过滤、计划计算和魔力估算明细记录为 `debug`。快照失效、重试和空间不足延后等情况仍保留 WARN。删除成功日志表示 qBittorrent 已确认删除请求，空间是否实际释放由后续空间复检日志说明。

计划摘要只展示计数、容量和队列状态，复杂列表按对象逐条记录，字段包含种子 ID、Hash、名称、大小、做种/下载人数和 Free 截止时间；回收对象还包含已完成字节数、客户端状态和路径。跳过候选记录 `reason`；容量不足时同时记录 `available_bytes`。例如，以下明细省略时间、模块和末尾源码位置：

```text
DEBUG TopFree queued candidate torrent_id=363456 hash=ab75cef9df29e746580afd97b6e46dbc0560d9a4 name="花儿与少年第六季" size=122.32GiB seeders=21 leechers=3 free_until=2026-11-08T12:54:22+08:00 queue_position=1 download_blocked=true
DEBUG unfinished current TopFree torrent blocks next download hash=0123456789abcdef0123456789abcdef01234567 name="Existing download" size=50.00GiB completed=30.00GiB amount_left=20.00GiB state=downloading added_at=2026-11-06T16:00:00+08:00 save_path=/downloads tags=[sc] reason=unfinished_current_topfree
```

排查“队列有候选却没有新增下载”时，先查看 `TopFree queue reconciled` 和 `TopFree plan built`：`download_blocked=true` 表示串行下载受阻；`reason=unfinished_current_topfree` 会给出尚未完成的当前 TopFree 任务，`reason=awaiting_client_visibility` 则表示上次新增请求已经确认，但任务尚未出现在客户端快照中。没有阻塞时，结合逐条 `TopFree candidate skipped` 的原因、大小过滤和容量摘要判断。

执行失败日志 `TopFree execution stopped` 包含 `phase` 和 `confirmed_write_requests`，用于定位失败阶段和此前成功返回的 qBittorrent 写请求数；该计数按请求计，不是种子数量。请求超时等情况仍可能存在未确认的远端结果，程序不会据此回滚；后续轮次重新读取客户端快照。可延后的瞬时网络错误、空间不足和过期计划使用 WARN，永久执行错误使用 ERROR。

网络日志包含具体操作、目标种子或路径、尝试次数和耗时；普通开始/完成记录在 DEBUG，重试后恢复为 INFO，重试耗尽为 WARN。HTTP 错误附带请求方法和接口路径；错误中的 URL 不展示用户信息、查询参数或片段，保留超时等根因。初始化记录配置路径和尝试轮次，BYR 凭据来源日志只表示凭据已加载，首次读取成功前不代表会话已经验证。

文件日志继续使用包含 `time=`、`level=`、`msg=` 和业务字段的结构化文本格式，保留完整时间和时区、原始容量字节数；`debug` 配置下保留完整源码位置。文件日志始终不包含 ANSI 颜色码。日志不会记录密码、BYR Cookie、qBittorrent SID、passkey 或包含 passkey 的下载 URL。

文件日志按大小自动轮转：写入下一条完整日志会超过上限时，先将当前文件归档为 `btassistantbyr.log.1`，原有备份依次后移，超过保留数量的最旧备份自动删除，再创建新的 `btassistantbyr.log`。`.1` 是最新备份。默认保留当前文件和 5 份备份，正常情况下合计不超过 120 MiB；不按天数清理或压缩。旧配置无需补字段即可启用默认轮转。

启动时继续追加已有日志；已有文件超过新上限时立即归档，降低备份数量时会在启动时删除超出数量的备份。历史文件不会被截断，因此升级前已过大或降低上限前生成的备份可能暂时使总量超过上述预算，随后续轮转淘汰。单条日志本身超过上限时，文件输出返回错误，不截断内容，终端仍尝试输出该事件。轮转失败会停止后续文件写入，首次错误由日志系统关闭时返回；排除故障后重启恢复。每个日志目录只供一个程序实例使用，不要同时用外部工具轮转这些文件。

终端与文件共享同一事件的动态字段值。某个输出写入失败时，程序继续尝试另一个输出，并在关闭日志系统时返回首次写入错误（包括短写），因此退出时可能出现 `close logging: write file log: ...` 等错误。

如果终端重启后出现 `open config/config.yaml: The system cannot find the path specified`，说明本次启动在当前工作目录下找不到默认配置路径。检查终端的启动命令是否保留 `-config`，并按上面的示例固定工作目录及配置绝对路径；这与 BYR 请求失败是两个独立问题。

## 队列、标签和删除范围

### 串行队列

- 当前 TopFree 快照中尚未存在于 qBittorrent 的候选会进入内存队列。
- 每轮最多添加一个种子。
- 只要存在尚未完成的当前 TopFree 任务，就不会添加下一项。
- 程序重启后会从 BYR 和 qBittorrent 的实际状态重建队列。
- 候选不再 TopFree 时会立即出队。
- 已开始但尚未完成、随后失去 TopFree 的 `sc` 任务会被停止，并删除种子和已下载内容；删除仍受 `execution.mode` 控制。

### 管理范围

程序只管理以下标签组合：

- 无标签；
- 仅 `sc`；
- 仅 `toDel`；
- 同时带 `sc` 和 `toDel`。

只要种子包含任何其他标签，即使同时带有 `sc` 或 `toDel`，也会完全排除在打标、容量统计、队列阻塞、失效清理和删除范围之外。程序仍会识别其 Hash，避免重复添加。

`sc` 表示由程序管理；单独的 `toDel` 表示用户明确授权它成为容量回收候选。程序不会仅因纯 `toDel` 任务未完成就执行“失去 TopFree”强制清理。

### 容量回收

程序只会回收已经不再 TopFree 的 `sc` 种子，或用户明确标记的纯 `toDel` 种子。当前 TopFree 和带其他标签的种子不会进入回收计划。

`free_time` 模式回收时先按添加时间从早到晚选择，直到逻辑容量和真实可用空间都满足下载要求；随后从已选列表末尾反向撤销仍可省略的删除项，尽量减少删除数量。这不是全局组合搜索，但能避免直接删除整个时间前缀。

### 魔力优先模式

设置 `strategy.priority: "bonus"` 后，程序使用 [BYR 做种魔力规则](https://byr.pt/mybonus.php?show=seed)估算收益：

```text
单种贡献 a = (1 - 10^(-发布时间距今的周数 / 4))
             × 大小GiB × (1 + √2 × 10^(-(做种人数 - 1) / 6))
A = 所有当前做种的单种贡献之和
魔力/小时 = min(做种数, 7) + 100 × 2/π × atan(A/300)
```

种子年龄取站点发布时间；本机添加时间、完成时间和个人累计做种时间不参与年龄计算。预测新增种子的收益时，做种人数计入下载完成后的自己。已经通过同一 BYR 账户在其他客户端做种的候选，不会因再次添加而计为新增收益。

程序为每个未过期候选分别规划空间回收：优先选择单位可释放容量对应的魔力损失较低的旧种，物理空间不足时优先选择能释放目标下载路径空间的任务，再反向撤销不必要的删除。随后比较“新增完成后的收益 − 被回收任务的收益”，每轮只选择净增益最高且为正的一项；净收益相同则沿用默认优先级。评估包含不足 7 个做种时的基础分变化。所有模式都保留串行下载、当前 TopFree 保护、其他标签保护和磁盘复检规则。

元数据通过当前账户的做种清单与详情页按 ID/Hash 关联，重启后重新构建；不按名称猜测种子身份。首次读取需要访问各做种的详情页，之后只在内存缓存不变的 Hash 和发布时间。做种人数和账户数据最多缓存一个 `strategy.refresh_interval`，写操作后立即使魔力快照失效。启用魔力模式会增加 BYR 只读请求。

不在账户当前做种清单中的旧任务、暂停后已不再被站点计入做种的任务、其他站点任务，均不能据此确认回收损失，本轮会明确列为 `bonus_metadata_unavailable` 并排除容量回收。未完成且失去 TopFree 的 `sc` 任务仍按原安全规则清理，独立于收益门槛；容量替换的净增益以这类必需清理后的状态为基线，整轮收益估算则包含清理损失。完整快照解析或刷新失败时，本轮不执行写操作，等待下一次队列检查；不会使用旧魔力快照或默默切回默认策略。网页公式、参数变化也会显式阻止魔力规划。

批准计划和日志展示快照时间、网页报告的 A、按当前完整做种清单重算的 A、新增贡献、删除损失与预计魔力/小时变化。账户其他客户端和带保护标签的做种只影响账户收益估算，不会计入程序的管理容量或删除范围。网页 A 可能来自较早的统计，列表体积也有显示精度限制，因此网页值与估算值可能有差异。

魔力计划有效期截至“快照读取开始时间 + `refresh_interval`”。批准超时、预检或空间等待后过期，会停止后续操作并在下一轮重新规划；已经完成的删除不会回滚。等待期间候选 Free 到期也会阻止继续执行。

这些收益假设候选能在 Free 期内完成下载并持续做种；当前仅检查 Free 尚未到期，不预测下载完成时间。未来做种人数变化、下载期间尚未获得做种收益，以及同一账户其他客户端的动态操作均未建模。删除按整种收益损失保守估算。策略是在当前 TopFree 候选和授权回收范围内进行的启发式优化，不保证长期或全站种子组合的全局最大值；允许的回收项不足时会保留旧种并跳过新增。

## 如何提交问题

请使用仓库的 [Bug 报告模板](https://github.com/Yokumii/BTAssistantByr/issues/new/choose)，附上版本、系统、qBittorrent 版本、脱敏配置和 DEBUG 日志。提交前务必删除用户名、密码、Cookie、SID、passkey 和私有下载 URL。

## 许可

本项目参考了 [BYRBT_RyukawaChihiro](https://github.com/LetMeFly666/BYRBT_RyukawaChihiro) 项目，许可沿用 MIT License，详见 [LICENSE](LICENSE)。
