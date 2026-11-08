# BTAssistantByr

BTAssistantByr 是一个面向 BYRBT 的 TopFree 自动管理工具。它定期读取当前置顶且 Free/2x Free 的种子，通过 qBittorrent Web API 维护串行下载队列，并在容量不足时按策略回收程序管理的种子。

> [!WARNING]
> 程序会打标签、停止任务、删除种子及文件并添加下载。首次使用请保持 `execution.mode: "request_approval"`，核对完整计划后再输入 `APPLY`。不要在未确认删除范围时启用 `yolo`。

## 功能

- 自动发现 BYR TopFree 种子，并同步 `sc`/`toDel` 标签。
- 一次只下载一个种子；当前 TopFree 任务完成后才推进队列。
- 默认按“种子大小 ÷ 剩余 Free 时间”从小到大选择。
- 可选按预计魔力净收益选择候选，并计入回收旧种的魔力损失。
- 可选通过 Resend 邮件汇总写操作、真实空间变化和魔力收益快照。
- 支持单种大小上限、代理、会话自动续期和网络重试。
- 下载前检查 qBittorrent 下载盘的真实可用空间，并保留安全余量。
- 支持人工批准和全自动两种写入模式。
- 提供终端彩色日志和结构化文件日志，支持按大小轮转。

## 运行要求

- 可正常登录的 BYRBT 账号。
- 已启用 Web UI 的 qBittorrent，建议使用 5.x。
- Linux、macOS 或 Windows，支持 `amd64` 和 `arm64`。
- 无法直连 BYR 时，需要可用的 HTTP、HTTPS 或 SOCKS5 代理。

程序通过 qBittorrent Web API 查询下载路径所在磁盘的空间。WebAPI 2.15.2 及以上支持任意路径；更早的版本只能可靠查询默认下载盘，若配置了非默认路径会直接报错，不会退回宿主机的空间数据。

## 快速开始

### 使用 Release

从 [GitHub Releases](https://github.com/Yokumii/BTAssistantByr/releases) 下载对应系统的压缩包，文件名为 `BTAssistantByr_<版本>_<os>_<arch>.tar.gz` 或 `.zip`，版本号不含前导 `v`。Release 提供 `SHA256SUMS` 和构建来源证明；带 `alpha`、`beta` 或 `rc` 后缀的版本为预发布版，标签格式见 [RELEASING.md](RELEASING.md)。

压缩包包含可执行文件、`config.example.yaml`、`README.md`、`CONTRIBUTING.md`、`RELEASING.md` 和 `LICENSE`。Linux 下载全部附件后可在目录内运行 `sha256sum --check --ignore-missing SHA256SUMS`，macOS 使用 `shasum -a 256 -c SHA256SUMS`；安装 GitHub CLI 后可用 `gh attestation verify <压缩包> --repo Yokumii/BTAssistantByr` 验证来源。

复制配置并填写：

```bash
cp config.example.yaml config.yaml
```

Linux/macOS：

```bash
./btassistantbyr -config config.yaml
```

Windows PowerShell：

```powershell
Copy-Item .\config.example.yaml .\config.yaml
.\btassistantbyr.exe -config .\config.yaml
```

`-config` 的相对路径以启动时的工作目录为基准。省略该参数时固定读取 `config/config.yaml`，因此 Release 中复制出的 `config.yaml` 必须显式指定。使用进程守护或 Windows Terminal 启动时，请固定工作目录和参数。

### 从源码运行

需要 Go 1.24 或更高版本：

```bash
cp config/config.example.yaml config/config.yaml
go run ./cmd/btassistantbyr -config config/config.yaml
```

本地构建的版本号为 `dev`；可用 `go run ./cmd/btassistantbyr -version` 或 `make build` 后的 `./bin/btassistantbyr -version` 查看。Release 二进制显示对应标签，例如 `v1.2.3`。

## 配置

完整配置项、注释和默认值见 `config/config.example.yaml`（Release 压缩包中为根目录的 `config.example.yaml`）。复制该文件后填写；`max_torrent_size_gib` 默认被注释，省略时不限制单种大小。

配置使用严格 YAML 校验：未知字段、缺少必填项、无效 URL、非法容量、无法解析的时间间隔或未知枚举值都会阻止启动。真实配置和运行数据已被 Git 忽略，不要把用户名、密码、Cookie、SID 或 passkey 提交到仓库或 Issue。

### tracker

| 配置项 | 说明 |
| --- | --- |
| `tracker.base_url` | BYR 地址，通常为 `https://byr.pt`；必填。 |
| `tracker.proxy_url` | 可留空；支持 `http`、`https`、`socks5` 和 `socks5h`。代理只作用于 BYR。 |
| `tracker.username` | BYR 用户名，必填。 |
| `tracker.password` | BYR 密码，必填。 |
| `tracker.cookie` | 可选的已有 `auth_token`。没有可用会话缓存时使用。 |

BYR 凭据按 `runtime.data_dir/byr_session.json` → `tracker.cookie` → 用户名密码的顺序获取。Cookie 失效时依次尝试下一级；登录或续期成功后，程序会以受限权限更新缓存。缓存绑定站点和用户名，除绑定字段外只保存会话 Cookie（`auth_token`、`session_id`），不保存密码、passkey 或 API Token；缓存属于敏感凭据，应限制 `runtime.data_dir` 的访问权限。请求被判定为会话过期时，程序会重试刚才失败的读取请求；缓存损坏或读写失败会显式报错。

### client

| 配置项 | 说明 |
| --- | --- |
| `client.base_url` | qBittorrent Web UI 地址，例如 `http://127.0.0.1:8080`；必填。 |
| `client.username` | Web UI 用户名；必填。 |
| `client.password` | Web UI 密码；必填。 |
| `client.save_path` | 下载路径；留空时使用 qBittorrent 默认下载路径。Windows YAML 可使用单引号，例如 `'F:\种子'`。 |

### strategy

| 配置项 | 说明 |
| --- | --- |
| `strategy.priority` | 可选：`free_time`（默认）或 `bonus`。 |
| `strategy.max_disk_usage_gib` | 程序管理种子的逻辑容量上限，单位 GiB，必须大于 0；必填。 |
| `strategy.max_torrent_size_gib` | 可选的单种大小上限，单位 GiB；等于上限仍允许下载。 |
| `strategy.min_free_space_gib` | 下载完成后必须保留的真实可用空间，单位 GiB，必须大于 0；必填。 |
| `strategy.refresh_interval` | 重新读取 BYR 的间隔，使用 Go duration，例如 `121s` 或 `10m`；必填。 |
| `strategy.queue_check_interval` | 使用最近一次有效快照检查队列的间隔，例如 `10s`；必填。 |

下载链路固定为：

```text
检查真实可用空间 → 生成策略 → 删除获准回收的种子及文件
→ 再次检查真实可用空间 → 添加一个队首种子
```

可用空间必须覆盖“队首种子大小 + `min_free_space_gib`”。qBittorrent 删除文件可能异步完成，程序会等待空间到账（最多 30 秒）后再添加；仍不足时结束本轮，下个检查周期重新规划，进程不会退出。BYR 刷新成功后立即检查一次队列；刷新重试耗尽会使旧快照失效，在下一次刷新成功前不使用旧数据。

### execution

| 配置项 | 说明 |
| --- | --- |
| `execution.mode` | `request_approval`（推荐）或 `yolo`；必填。 |

- `request_approval`：存在写操作时展示完整计划，只有输入 `APPLY` 才执行；其他输入取消。
- `yolo`：不请求确认，直接执行标签、停止、删除和下载操作。

批准以一轮完整计划为单位，不会逐条询问。无写操作的轮次不会出现批准提示。`Ctrl+C` 或 `SIGTERM` 可取消当前请求、等待或批准。

### resend（可选邮件通知）

省略整个 `resend` 块时关闭邮件；启用时以下字段均必填。

```yaml
resend:
  api_key: "re_your_api_key"
  from: "BTAssistantByr <bt@example.com>"
  to:
    - "you@example.com"
  timeout: "15s"
```

| 配置项 | 说明 |
| --- | --- |
| `resend.api_key` | Resend API Key，需具有发送权限；按凭据保管，不提交到仓库。 |
| `resend.from` | 发件邮箱，可包含显示名；使用已在 Resend 验证的发件域名。 |
| `resend.to` | 收件邮箱列表，1–50 个纯邮箱地址。 |
| `resend.timeout` | 正数 Go duration；分别限制写操作前的通知数据采集、执行后的数据采集和邮件发送（含重试）的总耗时。 |

每轮实际尝试过 qBittorrent 写请求后发送一封汇总邮件，覆盖添加/移除标签、重新汇报、暂停、删除种子及文件、添加下载；中途失败或取消时也尝试发送，并区分已确认成功和未确认成功的请求。未执行写请求的空轮次、拒绝批准、预检失败不会发邮件。邮件包含涉及的种子名称、Hash、大小以及执行阶段；完整错误保留在本地日志。

空间数据来自 qBittorrent 的实际下载路径，展示写操作前、清理后添加前的最后一次安全复检（有下载计划时）及执行结束后的读数。同一路径才计算差值。删除成功仅表示请求返回成功，文件可能尚未释放；新增成功也不代表已下载完成。空间变化还可能受到其他任务影响。

`free_time` 和 `bonus` 两种策略均在写操作前后额外读取 BYR 网页 A 和完整做种清单，按公布公式换算当前魔力/小时并展示变化。此处不读取账户魔力余额；站点更新可能滞后，不能把差值认定为本轮操作的真实到账收益。`bonus` 模式另外展示原批准计划的 A、删除损失、新增贡献、预计每小时净收益及快照有效期；这是假设新增任务全部下载完成的估算，执行中止时也不会将完整计划预测冒充已完成部分的收益。任何采集失败均显示数据不可用，并记录原因，不用零值替代。

通知数据采集和发送失败会明确记录日志，不改变已有写操作结果，也不会重放增删种子；发送失败只记录状态码或安全传输摘要，不记录响应正文。通知采集可能增加执行等待时间；`bonus` 计划在等待后仍按原有有效期检查。邮件发送使用 [Resend Email API](https://resend.com/docs/api-reference/emails/send-email)，仅瞬时故障按 `runtime.network_retry_count` 重试，并在同一封邮件的重试中复用 [Idempotency-Key](https://resend.com/docs/dashboard/emails/idempotency-keys)。Resend 接受请求不等于邮件已送达收件箱；没有持久化邮件队列，最终失败或进程被强制终止后不补发。正常取消执行后，仍以独立的超时预算尝试采集结果和发送，因此退出可能等待最多两个 `resend.timeout`。

邮件会将种子名称、Hash、下载路径和收益数据发送给 Resend 与所配置的收件人；不包含 BYR Cookie、qBittorrent SID、密码、Resend API Key、passkey 或私有下载 URL。

### runtime

| 配置项 | 说明 |
| --- | --- |
| `runtime.data_dir` | 运行数据目录，包含敏感的 BYR 会话缓存；必填。 |
| `runtime.log_dir` | 文件日志目录，文件名为 `btassistantbyr.log`；必填。 |
| `runtime.log_max_size_mib` | 可选，单个日志文件上限，单位 MiB；默认 `20`。 |
| `runtime.log_max_backups` | 可选，轮转备份数量，范围 `1..100`；默认 `5`。 |
| `runtime.log_level` | `debug`、`info`、`warn` 或 `error`；必填。 |
| `runtime.log_color` | `auto`、`always` 或 `never`；必填。 |
| `runtime.network_retry_count` | 额外网络重试次数，范围 `0..100`，必填；`3` 表示最多尝试 4 次。 |

相邻网络重试固定等待 1 秒。连接错误、超时、HTTP `408`、`425`、`429` 和 `5xx` 会重试；认证拒绝、解析失败、配置错误、策略错误和用户取消不按网络故障重试。重试以单个 BYR/qBittorrent 调用为单位，不会重放整轮写计划。

文件日志按大小轮转，默认保留当前文件和 5 份备份，`.1` 是最新备份。单条日志超过上限或轮转失败会显式报错，不会截断记录；排除故障后重启即可恢复。每个日志目录只供一个程序实例使用。

终端日志包含时间、等级、模块、消息和 `key=value` 字段；`debug` 下附带源码位置。日志不会记录密码、Cookie、SID、passkey 或含 passkey 的下载 URL。

## 队列、标签和回收

### 串行队列

- 当前 TopFree 中尚未存在于 qBittorrent 的候选进入内存队列。
- 每轮最多添加一个种子。
- 存在未完成的当前 TopFree 任务时，不会添加下一项。
- 重启后从 BYR 和 qBittorrent 的实际状态重建队列。
- 候选不再 TopFree 时立即出队。
- 已开始但未完成、随后失去 TopFree 的 `sc` 任务会被停止并删除种子及文件。

### 管理范围

程序只管理以下标签组合：

- 无标签；
- 仅 `sc`；
- 仅 `toDel`；
- 同时带 `sc` 和 `toDel`。

只要带有其他标签，种子就会完全排除在打标、容量统计、队列阻塞和删除范围之外；程序仍会识别其 Hash，避免重复添加。

`sc` 表示程序管理的种子。仅 `toDel`（或同时带 `sc`/`toDel`）表示用户明确授权其成为容量回收候选，不会仅因未完成而触发“失去 TopFree”强制清理。

### 容量回收

程序只回收不再 TopFree 的 `sc` 种子，或用户标记的 `toDel` 种子。当前 TopFree 和带其他标签的种子不会进入回收计划。

`free_time` 模式按添加时间从早到晚选择回收项，再从末尾撤销不必要的删除，直到逻辑容量和真实空间都满足下载要求。这能避免直接删除整个时间前缀。

### 魔力优先

设置 `strategy.priority: "bonus"` 后，程序使用 [BYR 做种魔力规则](https://byr.pt/mybonus.php?show=seed)估算收益：

```text
单种贡献 a = (1 - 10^(-发布时间距今的周数 / 4))
             × 大小GiB × (1 + √2 × 10^(-(做种人数 - 1) / 6))
A = 所有当前做种的单种贡献之和
魔力/小时 = min(做种数, 7) + 100 × 2/π × atan(A/300)
```

种子年龄取站点发布时间，不使用本机添加时间或个人做种时长。程序通过当前账户的做种清单与详情页按 ID/Hash 关联元数据，并缓存不可变的 Hash 和发布时间。做种人数和账户数据最多缓存一个 `refresh_interval`；写操作后快照失效。完整的做种清单、公式或规则解析失败时，本轮不执行写操作，也不会切回默认策略。

程序为每个未过期候选分别规划回收，比较“新增完成后的收益 − 被回收任务的收益”，每轮只执行净增益最高且为正的一项。收益估算基于当前快照和下载完成假设，不保证全局最优。已在本账户做种的候选不会重复计收益（`reason=no_bonus_gain`）；不在账户做种清单中的旧任务会以 `bonus_metadata_unavailable` 排除在回收之外；未完成且失去 TopFree 的 `sc` 任务仍按原安全规则清理。

批准计划和日志会展示快照时间、网页 A、按完整做种清单重算的 A、新增贡献、删除损失和预计魔力/小时变化。计划有效期为“快照读取时间 + `refresh_interval`”；计划过期（包括批准等待过久）、空间等待后仍不足或候选 Free 到期时，停止后续写入并重新规划，已完成的删除不会回滚。

## 故障排查

- **队列有候选但没有下载**：查看 `TopFree queue reconciled` 和 `TopFree plan built`。`download_blocked=true` 表示串行队列受阻，`reason=unfinished_current_topfree` 会给出阻塞任务，`reason=awaiting_client_visibility` 表示新增请求已确认但客户端快照尚未出现该任务。
- **候选被跳过**：查看 `TopFree candidate skipped` 的 `reason`，常见值为 `insufficient_space`、`free_period_expired`、`bonus_metadata_unavailable` 和 `no_bonus_gain`。
- **BYR 页面无法解析**：程序会记录 WARN 并使快照失效，等待下一个 `refresh_interval`。若持续出现 `invalid BYR torrent page: torrents table missing`，检查站点维护、代理返回内容以及账号是否能打开种子页。
- **执行失败**：`TopFree execution stopped` 的 `phase` 表示失败阶段，`confirmed_write_requests` 表示此前成功返回的写请求数。请求超时仍可能有未确认的远端结果，后续轮次会重新读取客户端状态。
- **启动时找不到配置**：默认路径是 `config/config.yaml`。Release 用户需要使用 `-config` 指定同目录下的 `config.yaml`，并固定启动工作目录。

## 参与开发

开发命令、测试约定和发布流程见 [CONTRIBUTING.md](CONTRIBUTING.md) 与 [RELEASING.md](RELEASING.md)。提交 Bug 请使用 [Issue 模板](https://github.com/Yokumii/BTAssistantByr/issues/new/choose)，并先移除配置和日志中的敏感信息。

## 许可

本项目参考了 [BYRBT_RyukawaChihiro](https://github.com/LetMeFly666/BYRBT_RyukawaChihiro)，许可沿用 MIT License，详见 [LICENSE](LICENSE)。
