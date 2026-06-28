# magazine2db

面向 `awesome-english-ebooks` 中 Economist 和 Wired 的本地入库工具。它将一期杂志拆成文章后直接写入共享 SQLite，不生成 `articles/*.md`，并提供 FTS5 全文检索、文章读取和中文摘要。

杂志内容来源：[hehonghui/awesome-english-ebooks](https://github.com/hehonghui/awesome-english-ebooks.git)。

## 环境要求

- Go 1.25 或更高版本：构建和运行程序。
- Git：执行杂志同步脚本。
- Calibre 的 `ebook-convert`：仅当一期杂志没有 TXT、需要从 EPUB 转换时使用。
- Anthropic Messages 协议兼容的模型服务：仅在生成中文摘要时需要，通过 `cfg.json` 和 `.env` 配置。

SQLite 和 FTS5 由 Go 依赖内置，无需单独安装 SQLite。

## 构建

```bash
go build -o magazines2db .
```

也可以不构建，直接使用 `go run .`。

程序按以下顺序推断工作目录：

1. 当前目录中存在 `cfg.json` 时，使用当前目录（开发模式）。
2. 否则查找 `magazines2db` 可执行文件同目录的 `cfg.json`（发布模式）。

确定工作目录后，程序读取同级 `.env` 中的 API Key。`cfg.json` 里的数据库相对路径也以该目录为基准，因此可以从任意目录启动发布后的程序。

发布目录结构如下：

```text
runtime/
├── magazines2db
├── cfg.json
├── .env
└── magazines.db
```

`cfg.json` 保存数据库、保留期数、并发数、最大 token、Provider URL 和模型；`.env` 只保存密钥：

```dotenv
MAGAZINE_PRIMARY_API_KEY=...
MAGAZINE_FALLBACK_API_KEY=...
```

`--db` 仍可临时覆盖 `cfg.json` 中的数据库路径。

## 同步杂志

首次执行会在项目内创建 shallow sparse clone，之后只更新 Economist 和 Wired 各自最新 4 期：

```bash
./scripts/sync_magazines.sh
```

杂志保存在 `data/awesome-english-ebooks/`，整个 `data/` 目录已被 Git 忽略。可以通过 `KEEP`、`TARGET_DIR`、`BRANCH` 和 `REPO_URL` 环境变量覆盖默认值。

需要每天自动更新时，先创建日志目录，再把任务加入 `crontab -e`：

```bash
mkdir -p logs
```

```cron
20 6 * * * cd /path/to/magazine2db && ./scripts/sync_magazines.sh >> logs/sync.log 2>&1
```

## 入库

传入一期杂志目录：

```bash
go run . ingest ./data/awesome-english-ebooks/01_economist/te_2026.06.13
go run . ingest ./data/awesome-english-ebooks/05_wired/2026.06.02
```

工具只接受目录，优先读取其中的 TXT；只有 EPUB 时调用本机 `ebook-convert` 转换，产物 `.txt` 持久保存在 EPUB 同目录，下次直接复用，不再重复转换。重复的 `publisher + issue_date` 会跳过。每个杂志只保留日期最新的 4 期，清理旧期时会级联删除文章和 FTS 索引。

## 搜索与读取

```bash
go run . search "interest rates"
go run . search --publisher wired "人工智能"
go run . read economist:2026-06-13:the-world-cup-paradox
go run . read 42
```

搜索覆盖标题、副标题、英文正文和中文摘要。查询不足 3 个字符时自动使用 `LIKE`，其余使用 FTS5 trigram。

## 中文摘要

摘要是独立步骤，默认并发 4，只处理尚无摘要的文章：

```bash
go run . summarize
go run . summarize --limit 20 --concurrency 4
```

底层使用 Eino 的 Claude ChatModel，通过 Anthropic Messages 协议请求。主 Provider 返回 `input new_sensitive (1026)` 时不会继续重试，而是立即改用 fallback Base URL；其他错误不会触发 fallback。成功摘要写回 SQLite，并由触发器同步更新 FTS 索引。

## 测试

普通测试不访问网络：

```bash
go test ./...
```

完整 E2E 会构建并运行真实二进制，覆盖 `help`、`ingest`、重复入库、`search`、两种 ID 的 `read` 和 `summarize`。测试使用项目目录的 `cfg.json` 与 `.env`，只处理一篇文章并发起一次真实摘要请求：

```bash
go test -tags=e2e -run TestCLIEndToEndWithRealSummaryAPI -v .
```
