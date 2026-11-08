# Contributing

## 环境

- Go 1.24 或更高版本。
- GNU Make、Bash、`tar`、`zip`、`sha256sum`（或 macOS 的 `shasum`）。
- 不需要真实的 BYR 或 qBittorrent 环境即可运行测试；外部协议使用 `httptest.Server`。

## 开发命令

```bash
make fmt        # 格式化 Go 文件
make fmt-check  # 检查格式
make tidy-check # 检查 go.mod/go.sum 是否需要整理
make test       # 运行测试
make test-race  # 竞态测试
make vet        # go vet
make build      # 构建 bin/btassistantbyr
make check      # test + vet + build
make ci         # fmt-check + tidy-check + test-race + vet + build
```

提交前至少运行 `make ci`。涉及发布脚本、入口或平台行为时，还应运行六平台打包检查：

```bash
release_check_dir=$(mktemp -d /tmp/btassistantbyr-release-check.XXXXXX)
./scripts/build-release.sh v0.0.0-alpha.0 "$release_check_dir"
```

## 代码约定

- 保持现有依赖方向：`domain` 不依赖外部系统，`service/topfree` 只依赖 `domain`/`port`，适配器实现 `port`，`app` 负责装配。
- 不添加静默兜底、吞错或未文档化的默认值。可恢复的瞬时网络错误按单次外部调用重试；配置、认证、解析、策略和批准错误保持显式。
- 下载链路顺序不可改变：真实空间检测 → 策略 → 获准删除 → 真实空间复检 → 下载。
- 任何真实 BYR/qBittorrent 写操作都必须由用户明确授权。默认只做纯策略、本地模拟和只读验证。
- 配置变更必须同步更新 `config/config.example.yaml`、加载器测试和 `README.md`。
- 不要提交用户名、密码、Cookie、SID、passkey、私有下载链接或真实运行数据。

## 测试

- 纯策略优先使用表驱动测试，覆盖边界、冲突和反向撤销。
- 涉及时间的测试必须注入或固定时钟，不依赖当前日期、时区边界或真实睡眠。
- 重试和轮询测试使用可控短间隔，并验证上下文取消。
- 写链路测试断言调用顺序，并证明错误发生后不会继续执行后续写操作。
- 凭证和日志测试主动断言敏感值未出现在输出中。

## 提交与 Pull Request

- 一个提交只做一件事，提交信息说明动机和结果；避免把格式化、功能和行为修复混在一起。
- Pull Request 描述应包含行为变化、验证命令和风险；用户可观察行为改变时同步更新 `README.md`。
- 发布相关变更请遵循 [RELEASING.md](RELEASING.md)。
