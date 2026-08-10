# BTAssistantByr

BTAssistantByr 是对 [BYRBT_RyukawaChihiro](https://github.com/LetMeFly666/BYRBT_RyukawaChihiro) 的 Go 语言二次开发。

当前仓库已完成工程初始化、架构边界定义，以及 BYR 和 qBittorrent 登录凭证获取。配置加载、BYR passkey/种子获取、qBittorrent 操作和下载策略尚未实现，暂不可用于实际刷流。

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
│   ├── config                配置模型与后续加载入口
│   ├── domain                核心领域模型
│   ├── port                  外部能力接口
│   └── service/topfree       TopFree 策略编排
├── Makefile
└── go.mod
```

依赖方向保持为 `adapter -> port/domain`、`service -> port/domain`，领域层不依赖站点、BT 客户端或配置文件格式。外部系统细节由适配器实现，应用层只负责装配。

## 开发命令

```bash
make fmt   # 格式化 Go 文件
make test  # 运行测试并检查所有包可编译
make vet   # 运行静态检查
make build # 构建到 bin/btassistantbyr
make check # 依次执行 test、vet、build
```

本地配置应由 `config/config.example.yaml` 复制为 `config/config.yaml`。真实配置和运行数据均已加入 `.gitignore`，不要提交账号、密码、Cookie 或 passkey。

## 已实现能力

- 使用用户名和密码向 BYR `/api/v2/login.php` 登录，获取 API Access Token、`auth_token` 和 `session_id`
- 可选通过 `tracker.proxy_url` 为 BYR 请求配置 HTTP、HTTPS 或 SOCKS5 代理
- 使用 Web API 用户名和密码向 qBittorrent `/api/v2/auth/login` 登录并获取 `SID`
- 校验已有 `auth_token` 或 `SID`，供后续配置加载和会话复用
- 对缺少账号密码、无效服务地址、非预期状态码和未返回凭证分别返回显式错误

认证模块不会持久化凭证，也不会把 Cookie 或 SID 写回配置文件。持久化策略将在配置加载功能中单独设计。

BYR 需要代理时，可在本地配置中指定代理地址；留空则不显式设置代理：

```yaml
tracker:
  base_url: "https://byr.pt"
  proxy_url: "http://127.0.0.1:10808"
```

该代理只作用于 BYR 适配器，不会代理本机 qBittorrent 请求。

## 开发阶段

1. 工程脚手架与领域边界（已完成）
2. BYR 与 qBittorrent 登录凭证获取（已完成）
3. 配置加载与校验
4. BYR 和 qBittorrent 业务适配器
5. TopFree 策略编排
6. 端到端测试与部署

## 许可

本项目沿用上游项目的 MIT License，并保留原作者版权声明，详见 [LICENSE](LICENSE)。
