# BTAssistantByr

BTAssistantByr 是一个面向 BYRBT 用户的 TopFree 自动管理工具。它会定期获取当前 TopFree 种子，通过 qBittorrent Web API 维护串行下载队列，并在磁盘空间不足时按受控策略回收程序管理的旧种子。

> [!WARNING]
> 本程序能够打标签、停止任务、删除种子及文件并添加下载。首次使用请保持 `execution.mode: "request_approval"`，逐项核对计划后再输入 `APPLY`。不要在不了解删除范围时启用 `yolo`。

## 主要能力

- 自动发现 BYR 当前置顶且 Free/2x Free 的 TopFree 种子。
- 一次只下载一个种子，当前任务完成后再推进队列。
- 按“种子大小 ÷ 剩余 Free 时间”选择优先级，体积更小、剩余时间更短的候选优先。
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
| `runtime.log_level` | `debug`、`info`、`warn` 或 `error`。排查问题时使用 `debug`。 |
| `runtime.log_color` | `auto`、`always` 或 `never`；推荐 `auto`。 |
| `runtime.network_retry_count` | 额外网络重试次数，范围 `0..100`；配置 `3` 表示总共最多尝试 4 次。 |

相邻网络重试固定等待 1 秒。连接错误、超时、HTTP `408`、`425`、`429` 和 `5xx` 会重试；权限拒绝、页面解析失败、配置错误、策略错误和用户拒绝批准不会被当成网络故障。

重试以单个 BYR/qBittorrent 调用为单位，不会重放整轮删除和下载计划。运行期间重试耗尽后，程序会跳过当前刷新或队列轮次并等待下一次调度。已经被远端确认完成的写操作不会自动回滚。

终端日志按等级着色；文件日志始终不包含 ANSI 颜色码。日志不会记录密码、BYR Cookie、qBittorrent SID、passkey 或包含 passkey 的下载 URL。

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

回收时先按添加时间从早到晚选择，直到逻辑容量和真实可用空间都满足下载要求；随后从已选列表末尾反向撤销仍可省略的删除项，尽量减少删除数量。这不是全局组合搜索，但能避免直接删除整个时间前缀。

## 如何提交问题

请使用仓库的 [Bug 报告模板](https://github.com/Yokumii/BTAssistantByr/issues/new/choose)，附上版本、系统、qBittorrent 版本、脱敏配置和 DEBUG 日志。提交前务必删除用户名、密码、Cookie、SID、passkey 和私有下载 URL。

## 许可

本项目参考了 [BYRBT_RyukawaChihiro](https://github.com/LetMeFly666/BYRBT_RyukawaChihiro) 项目，许可沿用 MIT License，详见 [LICENSE](LICENSE)。
