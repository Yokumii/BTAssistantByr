# BTAssistantByr

BTAssistantByr 是对 [BYRBT_RyukawaChihiro](https://github.com/LetMeFly666/BYRBT_RyukawaChihiro) 的 Go 语言二次开发。

当前仓库仅完成工程初始化和架构边界定义，尚未实现登录 BYR、获取种子、控制 qBittorrent 或执行下载策略，暂不可用于实际刷流。

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

## 开发阶段

1. 工程脚手架与领域边界（当前阶段）
2. 配置加载与校验
3. BYR 和 qBittorrent 适配器
4. TopFree 策略编排
5. 端到端测试与部署

## 许可

本项目沿用上游项目的 MIT License，并保留原作者版权声明，详见 [LICENSE](LICENSE)。
