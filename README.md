# magazine2db

面向 `awesome-english-ebooks` 中 Economist 和 Wired 的本地入库工具。它将一期杂志拆成文章后直接写入共享 SQLite，不生成 `articles/*.md`，并提供 FTS5 全文检索、文章读取和中文摘要。

杂志内容来源：[hehonghui/awesome-english-ebooks](https://github.com/hehonghui/awesome-english-ebooks.git)。

> [!IMPORTANT]
> **本项目当前仅在 macOS 上开发和验证。** 本文记录的构建、同步、Codex 摘要、定时任务和 E2E 流程均以 macOS 为准；Linux 与 Windows 尚未测试，不保证可以正常运行。项目脚本还直接依赖 macOS 的 `/usr/bin/trash`。

## 环境要求

- Go 1.25 或更高版本：构建和运行程序。
- Git：执行杂志同步脚本。
- Calibre 的 `ebook-convert`：仅当一期杂志没有 TXT、需要从 EPUB 转换时使用。
- 已登录的 Codex CLI：仅在生成中文摘要时需要；模型固定为 `gpt-5.6-luna`，reasoning effort 固定为 `max`。

SQLite 和 FTS5 由 Go 依赖内置，无需单独安装 SQLite。

## 构建

Go 没有单独的 release profile，`go build` 默认会进行优化。开发时直接构建：

```bash
go build -o magazine2db .
```

发布时建议移除本机路径、符号表和调试信息，以减小二进制体积：

```bash
go build -trimpath -ldflags="-s -w" -o magazine2db .
```

需要完全静态的当前平台二进制时：

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o magazine2db .
```

开发阶段也可以不构建，直接使用 `go run .`。

程序按以下顺序推断工作目录：

1. 当前目录中存在 `cfg.json` 时，使用当前目录（开发模式）。
2. 否则查找 `magazine2db` 可执行文件同目录的 `cfg.json`（发布模式）。

`cfg.json` 里的数据库相对路径以工作目录为基准，因此可以从任意目录启动发布后的程序。Codex 使用当前系统用户已有的 CLI 登录状态，不读取项目 `.env`。

发布目录结构如下：

```text
runtime/
├── magazine2db
├── cfg.json
└── magazines.db
```

`cfg.json` 保存数据库、保留期数和 Codex 摘要执行参数：

```json
{
  "database": "magazines.db",
  "retention": 4,
  "summary": {
    "concurrency": 4,
    "timeout_seconds": 1800,
    "codex_bin": "codex"
  }
}
```

`codex_bin` 可以是 PATH 中的命令，也可以是绝对路径。使用 npm/NVM 安装 Codex 时，定时任务的 PATH 还必须包含同目录下的 `node`。

`--db` 仍可临时覆盖 `cfg.json` 中的数据库路径。

## 同步杂志

首次执行会在项目内创建 shallow sparse clone，之后只更新 Economist 和 Wired 各自最新 4 期：

```bash
./scripts/sync_magazines.sh
```

杂志保存在 `data/awesome-english-ebooks/`，整个 `data/` 目录已被 Git 忽略。可以通过 `KEEP`、`TARGET_DIR`、`BRANCH` 和 `REPO_URL` 环境变量覆盖默认值。

每天自动执行“同步 → 入库新期刊 → 生成待处理摘要”时，先构建二进制并创建日志目录：

```bash
go build -o magazine2db .
mkdir -p logs
```

先手动验证一次：

```bash
./scripts/daily.sh
```

然后执行 `crontab -e`，加入：

```cron
PATH=/path/to/codex-and-node/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/Applications/calibre.app/Contents/MacOS
20 6 * * * cd /path/to/magazine2db && ./scripts/daily.sh >> logs/daily.log 2>&1
```

将 `/path/to/codex-and-node/bin` 替换为 `dirname "$(command -v codex)"` 的实际输出。每天 6:20 运行。脚本带有互斥锁，重复触发会直接退出；已有期刊由 `ingest` 自动跳过，只为尚无摘要的文章调用模型。可以通过 `MAGAZINE2DB_BIN` 覆盖二进制路径，并继续使用同步脚本支持的 `KEEP`、`TARGET_DIR`、`BRANCH` 和 `REPO_URL` 环境变量。锁目录退出时通过 macOS `/usr/bin/trash` 移入废纸篓，不永久删除。

## 入库

传入一期杂志目录：

```bash
go run . ingest ./data/awesome-english-ebooks/01_economist/te_2026.06.13
go run . ingest ./data/awesome-english-ebooks/05_wired/2026.06.02
```

工具只接受目录，优先读取其中的 TXT；只有 EPUB 时调用本机 `ebook-convert` 转换，产物 `.txt` 持久保存在 EPUB 同目录，下次直接复用，不再重复转换。重复的 `publisher + issue_date` 会跳过。每个杂志只保留日期最新的 4 期，清理旧期时会级联删除文章和 FTS 索引。

查看已经入库的期刊及其文章数量：

```bash
go run . issue
go run . issue --json
```

默认按期刊日期倒序输出 plain text；`--json` 返回 `count` 和 `issues`。每期包含 `id`、`publisher`、`issue_date`、`article_count` 和 `imported_at`。

## 搜索与读取

```bash
go run . search "interest rates"
go run . search --publisher wired "人工智能"
go run . search --json "interest rates"
go run . read economist:2026-06-13:the-world-cup-paradox
go run . read 42
go run . read --json 42
```

搜索覆盖标题、副标题、英文正文和中文摘要。查询不足 3 个字符时自动使用 `LIKE`，其余使用 FTS5 trigram。

`search` 和 `read` 支持 `--json`，用于 agent 或脚本提取结构化信息。`search` 返回 `count` 和 `results`，`read` 返回完整文章对象；JSON 字段统一使用 snake_case。

分页获取文章标题和摘要时使用 `list`：

```bash
go run . list --page 1 --page-size 20
go run . list --issue 7 --page 1 --page-size 20
go run . list --page 1 --page-size 20 --json
```

默认输出便于阅读的 plain text；使用 `--issue ID` 可只查看某一期。传入 `--json` 时返回 `page`、`page_size`、`total` 和 `items`。每项包含 `id`、`title`、`summary`；`summary` 最多 200 个字符，尚未生成摘要时使用正文内容。

## 中文摘要

摘要是独立步骤，默认并发 10，只处理尚无摘要的文章：

```bash
go run . summarize
go run . summarize --limit 20 --concurrency 10
```

每篇文章启动一次非交互式 `codex exec`，固定使用 `gpt-5.6-luna` + `max`。运行参数包含 `--ephemeral`、`--sandbox read-only`、`--output-schema` 和 `-o`；文章正文只写入隔离 workspace，不放进命令行。应用会再次验证最终 JSON：摘要必须是单段中文、不超过 300 个 Unicode 字符，且不能包含 Markdown 或“摘要：”前缀。

每次调用的输入、命令、stdout/stderr、最终响应和运行记录保存在 `.agent-runs/summary/`，不会自动永久删除。成功摘要及 `codex/gpt-5.6-luna@max` 会写回 SQLite，并由触发器同步更新 FTS 索引。Codex 启动失败、非零退出、超时或结果违规都会保留 artifacts 并写入 `summary_error`；不会切换模型，也不会内部重试。

执行一次合成文章摘要，验证 Codex CLI、登录状态、模型、网络和结构化输出（不写库）：

```bash
go run . smoke
```

## 测试

普通测试不访问网络：

```bash
go test ./...
```

完整 E2E 会构建并运行真实二进制，覆盖 `help`、`ingest`、重复入库、`search`、两种 ID 的 `read` 和 `summarize`。测试要求当前用户已登录 Codex CLI，只处理一篇合成文章并发起一次真实摘要请求：

```bash
go test -tags=e2e -run TestCLIEndToEndWithRealCodexSummary -v .
```

## 许可证与内容归属

本项目源代码采用 [MIT License](LICENSE)。MIT 仅适用于本项目代码，不包含程序同步或处理的杂志、文章、图片、音频等第三方内容。

杂志内容来源于 [hehonghui/awesome-english-ebooks](https://github.com/hehonghui/awesome-english-ebooks)，相关内容仍受来源仓库说明、原始出版方及版权持有人的权利约束，详见 [NOTICE](NOTICE)。
