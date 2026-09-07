# magazine2db

面向 `awesome-english-ebooks` 中 Economist 和 Wired 的本地入库工具。它将一期杂志拆成文章后直接写入共享 SQLite，不生成 `articles/*.md`，并提供 期刊浏览、文章读取和原文节选。

杂志内容来源：[hehonghui/awesome-english-ebooks](https://github.com/hehonghui/awesome-english-ebooks.git)。

> [!IMPORTANT]
> **本项目当前仅在 macOS 上开发和验证。** 本文记录的构建、同步、定时任务和 E2E 流程均以 macOS 为准；Linux 与 Windows 尚未测试，不保证可以正常运行。项目脚本还直接依赖 macOS 的 `/usr/bin/trash`。

## 环境要求

- Go 1.25 或更高版本：构建和运行程序。
- Git：执行杂志同步脚本。
- EPUB 原生读取使用 Go ZIP/XML 标准库和 `golang.org/x/net/html`，无需 Calibre。

SQLite 由 Go 依赖内置，无需单独安装 SQLite。

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

`cfg.json` 里的数据库相对路径以工作目录为基准，因此可以从任意目录启动发布后的程序。程序不依赖 LLM、API 密钥或登录状态。

发布目录结构如下：

```text
runtime/
├── magazine2db
├── cfg.json
└── magazines.db
```

`cfg.json` 保存数据库路径和保留期数：

```json
{
  "database": "magazines.db",
  "retention": 4
}
```

`--db` 仍可临时覆盖 `cfg.json` 中的数据库路径。

## 同步杂志

首次执行会在项目内创建 shallow sparse clone，之后只更新 Economist 和 Wired 各自最新 4 期：

```bash
./scripts/sync_magazines.sh
```

杂志保存在 `data/awesome-english-ebooks/`，整个 `data/` 目录已被 Git 忽略。可以通过 `KEEP`、`TARGET_DIR`、`BRANCH` 和 `REPO_URL` 环境变量覆盖默认值。

每天自动执行“同步 → 入库新期刊”时，先构建二进制并创建日志目录：

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
PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin
20 6 * * * cd /path/to/magazine2db && ./scripts/daily.sh >> logs/daily.log 2>&1
```

每天 6:20 运行。脚本带有互斥锁，重复触发会直接退出；已有期刊由 `ingest` 自动跳过。可以通过 `MAGAZINE2DB_BIN` 覆盖二进制路径，并继续使用同步脚本支持的 `KEEP`、`TARGET_DIR`、`BRANCH` 和 `REPO_URL` 环境变量。锁目录退出时通过 macOS `/usr/bin/trash` 移入废纸篓，不永久删除。

## 入库

传入一期杂志目录：

```bash
go run . ingest ./data/awesome-english-ebooks/01_economist/te_2026.06.13
go run . ingest ./data/awesome-english-ebooks/05_wired/2026.06.02
```

工具只接受包含 EPUB 的目录，原生读取 EPUB：通过 OPF、nav / NCX 和 spine 定位并排序文章，原样保存 XHTML，再使用 `JohannesKaufmann/html-to-markdown/v2` 转换为 Markdown。不再支持 TXT 导入；先按刊物和期号查询数据库，已存在且未指定 `--force` 时直接跳过，无需 EPUB；需要解析时，目录没有 EPUB 会报错，此时数据库可能已创建。EPUB 解析失败会直接报错，不使用 TXT 回退。已有数据库中的历史纯文本文章仍可正常读取。重复的 `publisher + issue_date` 默认跳过。显式使用 `ingest --force <目录>` 可从 EPUB 刷新已有期刊：在同一事务中删除整期期刊记录及其文章，再插入本次解析结果并执行保留期数清理；失败时整体回滚。期刊及文章 ID 可能变化，元数据以本次解析为准，下游应重新获取期刊和文章列表。每个杂志只保留日期最新的 4 期，清理旧期时会级联删除文章。

查看已经入库的期刊及其文章数量：

```bash
go run . issue
go run . issue --json
```

默认按期刊日期倒序输出 plain text；`--json` 返回 `count` 和 `issues`。每期包含 `id`、`publisher`、`issue_date`、`article_count` 和 `imported_at`。

## 文章读取

```bash
go run . read economist:2026-06-13:the-world-cup-paradox
go run . read 42
go run . read --json 42
```

`read --json` 默认返回元数据及 Markdown `body`，供下游直接使用。JSON 字段统一使用 snake_case。

`body_xhtml` 保存 ZIP 中的原始 UTF-8 XHTML，不重新序列化。`source_href` 记录 EPUB 内部文件路径。需要核对原始数据时：

```bash
go run . read --xhtml 42
go run . read --json --xhtml 42
```

前者输出原始 XHTML，后者在 JSON 中包含 `body_xhtml`。历史 TXT 导入或尚未刷新的记录没有 XHTML，显式请求时会报错。

`body` 在 HTML DOM 副本上排除 head、script、style、template、图片及显式隐藏内容，再转换为 Markdown，保留标题、列表、引用、代码和表格结构；不推断出版社专有版式，也不保证排除所有导航、广告或 CSS 隐藏内容。作者和发布时间不从 OPF 的整刊元数据推断；缺失时留空。历史 `body` 不会自动转换，使用 `ingest --force` 从 EPUB 刷新后才变成 Markdown。每篇文章对应独立 XHTML 文件；带锚点的目录链接和重复文章文件会报错，不支持共用 XHTML 的分段文章。

分页获取文章标题和原文节选时使用 `list`：

```bash
go run . list --page 1 --page-size 20
go run . list --issue 7 --page 1 --page-size 20
go run . list --page 1 --page-size 20 --json
```

默认输出便于阅读的 plain text；使用 `--issue ID` 可只查看某一期。传入 `--json` 时返回 `page`、`page_size`、`total` 和 `items`。每项包含 `id`、`title`、`excerpt`；`excerpt` 为正文开头最多 200 个 Unicode 字符，完整正文通过 `read` 获取。

## 数据库重建

当前数据库版本为 `user_version = 4`，不再兼容旧库或执行历史迁移。旧库打开时会明确报错，需要将旧数据库移入废纸篓后，从原 EPUB 重新执行 `ingest`。

重建会重新生成期刊和文章 ID，正文输出为 Markdown；没有对应 EPUB 的历史记录无法重新导入。下游应重新获取期刊和文章列表。

数据库只保存期刊和文章，不再创建全文索引，也不提供 `search`、`summarize` 或 `smoke` 命令。

## 测试

普通测试不访问网络：

```bash
go test ./...
```

完整 E2E 会构建并运行真实二进制，覆盖 `help`、`ingest`、重复入库、两种 ID 的 `read` 和分页 `list`。测试使用合成 EPUB，并验证 TXT-only 导入报错、已有期刊无 EPUB 时跳过、`--force` 强制解析，覆盖刷新、原始 XHTML 往返，不调用模型或访问网络：

```bash
go test -tags=e2e -run TestCLIEndToEnd -v .
```

## 许可证与内容归属

本项目源代码采用 [MIT License](LICENSE)。MIT 仅适用于本项目代码，不包含程序同步或处理的杂志、文章、图片、音频等第三方内容。

杂志内容来源于 [hehonghui/awesome-english-ebooks](https://github.com/hehonghui/awesome-english-ebooks)，相关内容仍受来源仓库说明、原始出版方及版权持有人的权利约束，详见 [NOTICE](NOTICE)。
