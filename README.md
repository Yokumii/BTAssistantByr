# BTAssistantByr

BTAssistantByr 定期读取 BYRBT 当前置顶且 Free/2x Free 的种子，通过 qBittorrent Web API 维护串行下载队列，并在容量不足时回收程序管理的种子。

> [!WARNING]
> 程序会打标签、停止任务、删除种子及文件并添加下载。首次使用请保持 `execution.mode: "request_approval"`，核对计划后再输入 `APPLY`。不要在未确认删除范围时启用 `yolo`。

## 快速开始

需要 BYRBT 账号、已启用 Web UI 的 qBittorrent（建议 5.x），以及 Linux、macOS 或 Windows。无法直连 BYR 时需要 HTTP/SOCKS5 代理。

Release 压缩包从 [GitHub Releases](https://github.com/Yokumii/BTAssistantByr/releases) 下载。带 `alpha`、`beta`、`rc` 后缀的是预发布版；`SHA256SUMS` 可用于校验附件。发布流程见 [RELEASING.md](RELEASING.md)。

```bash
cp config.example.yaml config.yaml
./btassistantbyr -config config.yaml
```

Windows：

```powershell
Copy-Item .\config.example.yaml .\config.yaml
.\btassistantbyr.exe -config .\config.yaml
```

`-config` 的相对路径以启动目录为基准；省略时读取 `config/config.yaml`，所以 Release 解压出的配置需要显式指定。

从源码运行需要 Go 1.24 或更高版本：

```bash
cp config/config.example.yaml config/config.yaml
go run ./cmd/btassistantbyr -config config/config.yaml
```

源码构建的版本为 `dev`，Release 显示标签；用 `-version` 查看。

## 配置

完整字段、注释和默认值见 `config/config.example.yaml`（Release 中为根目录的 `config.example.yaml`）。配置会严格校验，未知字段、缺失必填项和非法值会阻止启动。真实配置和运行数据已被 Git 忽略，不要提交用户名、密码、Cookie、SID 或 passkey。

必填项包括 `tracker`/`client` 的地址和账号、`strategy` 的容量与间隔、`execution.mode`，以及 `runtime.data_dir`、`log_dir`、`log_level`、`log_color`、`network_retry_count`。其余字段可选；`resend` 整块可选，配置后其字段均必填。

| 配置项 | 说明 |
| --- | --- |
| `tracker.proxy_url` | 可留空；支持 `http`、`https`、`socks5`、`socks5h`，只代理 BYR。 |
| `tracker.cookie` | 可选的已有 `auth_token`。 |
| `client.save_path` | 下载路径；留空时使用 qBittorrent 默认下载路径。Windows YAML 可写成 `'F:\种子'`。 |
| `strategy.priority` | `free_time`（默认）或 `bonus`。 |
| `strategy.max_torrent_size_gib` | 可选单种上限；等于上限仍允许下载。 |
| `strategy.refresh_interval` | 读取 BYR 的间隔，例如 `121s`。 |
| `strategy.queue_check_interval` | 检查队列的间隔，例如 `10s`。 |
| `runtime.log_max_size_mib` | 单个日志文件上限，默认 `20`。 |
| `runtime.log_max_backups` | 轮转备份数量，范围 `1..100`，默认 `5`。 |
| `runtime.log_level` | `debug`、`info`、`warn` 或 `error`。 |
| `runtime.log_color` | `auto`、`always` 或 `never`。 |
| `runtime.network_retry_count` | 额外重试次数，`0..100`；`3` 表示最多尝试 4 次。 |

BYR 凭据按 `runtime.data_dir/byr_session.json` → `tracker.cookie` → 用户名密码获取。登录或续期成功后更新受限权限的会话缓存，只保存 `auth_token` / `session_id`。该文件是敏感凭据。

WebAPI 2.15.2 及以上可以查询任意下载路径；更早版本只能查询默认下载盘，配置非默认路径会直接报错。

## 行为

- 队列从 BYR 和 qBittorrent 的实际状态重建，每轮最多添加一个种子；当前 TopFree 未完成时不会添加下一个。
- 只管理无标签、仅 `sc`、仅 `toDel` 或同时带 `sc`/`toDel` 的种子；带其他标签的种子完全排除。候选失去 TopFree 时出队。
- 下载顺序固定为：检查真实空间 → 生成计划 → 删除获准回收的种子 → 再次检查空间 → 添加一个种子。qBittorrent 删除可能异步完成，程序最多等待 30 秒；空间仍不足时结束本轮，下个周期重试。
- 容量不足时只回收不再 TopFree 的 `sc` 种子或用户标记的 `toDel` 种子。`free_time` 模式按添加时间从早到晚选择，再从末尾撤销不必要的删除。

`execution.mode` 为 `request_approval` 时，写操作前展示完整计划，只有输入 `APPLY` 才执行；`yolo` 不确认。批准以整轮计划为单位，`Ctrl+C` 或 `SIGTERM` 可取消。

### 魔力优先

`strategy.priority: "bonus"` 时按 BYR 做种魔力规则估算：

```text
a = (1 - 10^(-发布时间周数 / 4)) × 大小GiB × (1 + √2 × 10^(-(做种人数 - 1) / 6))
魔力/小时 = min(做种数, 7) + 100 × 2/π × atan(A/300)
```

程序读取账户的完整做种清单和详情页，只缓存不可变信息。每个候选单独规划回收，比较新增收益和删除损失，每轮最多执行一个正收益候选；无法确认收益的旧任务不参与魔力回收。这是基于当前快照和下载完成假设的启发式估算，不保证全局最优。计划过期、空间不足或候选 Free 到期时会停止后续写入并重新规划，已完成的删除不回滚。

### Resend 邮件

`resend` 可选，用于在每轮实际尝试过 qBittorrent 写请求后发送一封汇总邮件。邮件包含写请求结果、写前/清理后/写后的空间读数、BYR 魔力快照和涉及的种子信息；空轮次、拒绝批准、预检失败不会发送。采集或发送失败只记录日志，不改变执行结果，也不补发。

空间和魔力是读取时点的值：删除或添加请求成功不等于空间已到账或下载已完成，站点统计也可能延迟。邮件会发送种子名称、Hash、下载路径和收益数据，但不包含密码、Cookie、SID、passkey、API Key 或私有下载 URL。`resend.timeout` 分别限制采集和发送，取消后仍会尝试发送结果。

| 配置项 | 说明 |
| --- | --- |
| `resend.api_key` | Resend API Key。 |
| `resend.from` | 发件地址，需使用已在 Resend 验证的域名。 |
| `resend.to` | 1–50 个收件地址。 |
| `resend.timeout` | 正数 Go duration，例如 `15s`。 |

## 日志和排查

日志按大小轮转，`.1` 是最新备份：默认单文件 20 MiB、5 份备份。网络重试以单次外部调用为单位，连接错误、超时、HTTP `408`、`425`、`429` 和 `5xx` 会重试；认证、解析、配置和策略错误不会。日志和邮件不记录密码、Cookie、SID、passkey 或私有下载 URL。

- **有候选但没有下载**：查看 `TopFree queue reconciled` 和 `TopFree plan built`；`reason=unfinished_current_topfree` 表示当前任务未完成，`awaiting_client_visibility` 表示新增请求已确认但客户端还没显示。
- **候选被跳过**：查看 `TopFree candidate skipped` 的 `reason`，常见值为 `insufficient_space`、`free_period_expired`、`bonus_metadata_unavailable`、`no_bonus_gain`。
- **BYR 页面解析失败**：程序会记录 WARN 并使快照失效，等待下个刷新周期。持续出现 `torrents table missing` 时检查站点、代理和账号页面。
- **启动找不到配置**：默认路径是 `config/config.yaml`；Release 用户需要用 `-config` 指向解压目录中的 `config.yaml`。

开发命令和发布流程见 [CONTRIBUTING.md](CONTRIBUTING.md) 与 [RELEASING.md](RELEASING.md)。Bug 请使用 [Issue 模板](https://github.com/Yokumii/BTAssistantByr/issues/new/choose) 提交，并先移除敏感信息。

本项目参考 [BYRBT_RyukawaChihiro](https://github.com/LetMeFly666/BYRBT_RyukawaChihiro)，使用 MIT License，详见 [LICENSE](LICENSE)。
