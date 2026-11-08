# Releasing

发布由 `.github/workflows/release.yml` 在推送 `v*` 标签时执行。普通 `main`/`dev` push 只运行 CI，不创建 Release。

## 版本与分支

- 正式版：`vMAJOR.MINOR.PATCH`，标签提交必须包含在 `main`。
- 预发布版：`vMAJOR.MINOR.PATCH-alpha[.N]`、`beta[.N]` 或 `rc[.N]`，标签提交必须包含在 `dev`。

标签不符合上述格式、或标签提交不在对应分支时，工作流会失败。

## 仓库设置

- `main` 和 `dev` 的 Ruleset/Branch protection 将 `CI / verify` 设为必需检查。
- 为 `v*` 标签配置规则，禁止更新和删除已推送的标签，避免同版本指向不同提交。

## 流程

1. 确认目标分支的 CI 通过，工作区没有未提交改动。
2. 创建并推送注释标签：

   ```bash
   git switch dev
   git pull --ff-only origin dev
   git tag -a v1.2.3-alpha.1 -m "v1.2.3-alpha.1"
   git push origin dev
   git push origin v1.2.3-alpha.1
   ```

   正式版将分支和标签改为 `main` 与 `v1.2.3`。

3. 等待 Release 工作流完成。它会：
   - 校验标签格式、目标分支和标签提交归属；
   - 运行 `make ci`；
   - 构建 Linux、macOS、Windows 的 `amd64`/`arm64` 六个压缩包和 `SHA256SUMS`；
   - 为构建产物生成 GitHub Artifact Attestation；
   - 下载并校验产物后创建或更新 Release 草稿。
4. 在 GitHub Releases 中核对草稿的版本、更新说明、六个附件和校验和，再手动发布。工作流不会自动公开草稿。

同一标签重跑可以更新草稿附件；更新说明也可能由 GitHub 重新生成。如果 Release 已经发布，工作流会失败，不会修改其说明或附件。

## 手动验证

```bash
release_check_dir=$(mktemp -d /tmp/btassistantbyr-release-check.XXXXXX)
./scripts/build-release.sh v0.0.0-alpha.0 "$release_check_dir"
(cd "$release_check_dir" && sha256sum --check --strict SHA256SUMS)
```

macOS 没有 `sha256sum` 时，在目录内执行 `shasum -a 256 -c SHA256SUMS`。发布构建使用 `-trimpath` 和 `-ldflags "-s -w -X main.version=<tag>"`；工作流在标签对应的干净检出上构建。

下载附件后可以核对发布页的校验和，也可以用 GitHub CLI 验证 Artifact Attestation：

```bash
gh attestation verify BTAssistantByr_<version>_<os>_<arch>.tar.gz --repo Yokumii/BTAssistantByr
```
