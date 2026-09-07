# magazine2db

面向 [awesome-english-ebooks](https://github.com/hehonghui/awesome-english-ebooks) 中 *The Economist* 与 *WIRED* 的本地入库工具：把一期杂志按文章拆分后写入共享 SQLite 数据库（不生成 `articles/*.md`），并提供期刊浏览、文章列表、全文读取与原始 XHTML 取回。

- 原生解析 EPUB，无需 Calibre；不依赖 LLM、API 密钥或登录状态。
- 每篇文章同时保存 **Markdown 正文**（供阅读和下游 LLM 使用）与 **ZIP 内原始 XHTML**（供核对，默认不返回）。
- SQLite 由纯 Go 驱动内置，单文件数据库，CGO-free，可静态编译。

> [!IMPORTANT]
> **本项目当前仅在 macOS 上开发和验证。** 构建、同步、定时任务与 E2E 流程均以 macOS 为准，Linux / Windows 尚未测试；脚本还直接依赖 macOS 的 `/usr/bin/trash`。Go 程序本身是跨平台的，平台绑定只存在于 `scripts/` 下的 shell 脚本。

## 环境要求

- Go 1.25 或更高版本。
- Git：执行杂志同步脚本。
- EPUB 解析使用标准库 `archive/zip`、`encoding/xml`，以及 `golang.org/x/net/html`、`PuerkitoBio/goquery` 和 `JohannesKaufmann/html-to-markdown/v2`。
- SQLite 由 `modernc.org/sqlite` 内置，无需单独安装，也无需 Calibre。

## 构建

`go build` 默认即带优化。开发时直接构建：

```bash
go build -o magazine2db .
```

发布时移除本机路径、符号表和调试信息以减小体积：

```bash
go build -trimpath -ldflags="-s -w" -o magazine2db .
```

需要完全静态的当前平台二进制时：

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o magazine2db .
```

## 数据库位置与参数

程序**不读取任何配置文件**，规则固定：

- 默认数据库是**可执行文件同目录**下的 `magazines.db`，因此可以从任意工作目录启动，数据始终落在二进制旁边；首次打开时自动建库。
- 每个刊物默认保留日期最新的 4 期。
- 任意命令都可用 `--db PATH` 指定其他数据库（相对路径以当前工作目录为基准）。
- `ingest --keep N` 可覆盖每刊保留期数。

发布目录只需二进制和数据库并排：

```text
runtime/
├── magazine2db
└── magazines.db
```

> `go run .` 的临时二进制位于系统临时目录，默认库也会落在那里，因此开发时请显式指定 `--db ./magazines.db`，或先 `go build` 再运行构建产物。

## 命令速览

```text
magazine2db ingest [--db PATH] [--keep N] [--force] <issue-dir>
magazine2db issue  [--db PATH] [--json]
magazine2db list   [--db PATH] [--issue ID] [--page N] [--page-size N] [--json]
magazine2db read   [--db PATH] [--json] [--xhtml] <stable-id | numeric-id>
magazine2db help
```

`read` 同时接受数字行 ID 和稳定 ID `stable_id`，格式为 `publisher:issue-date:slug`，例如 `economist:2026-09-05:leaders-politics`。所有命令的 `--json` 输出统一使用 snake_case 字段，供脚本或 agent 直接消费。

## 同步杂志

首次执行会在项目内创建 shallow sparse clone，之后只更新 Economist 和 Wired 各自最新的几期：

```bash
./scripts/sync_magazines.sh
```

杂志保存在 `data/awesome-english-ebooks/`，整个 `data/` 目录已被 Git 忽略。可用环境变量 `KEEP`、`TARGET_DIR`、`BRANCH`、`REPO_URL` 覆盖默认值。

要每天自动完成“同步 → 入库新期刊”，先构建二进制并创建日志目录：

```bash
go build -o magazine2db .
mkdir -p logs
./scripts/daily.sh   # 先手动验证一次
```

然后 `crontab -e` 加入：

```cron
PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin
20 6 * * * cd /path/to/magazine2db && ./scripts/daily.sh >> logs/daily.log 2>&1
```

`daily.sh` 带互斥锁，重复触发会直接退出；已入库的期刊由 `ingest` 自动跳过。可用 `MAGAZINE2DB_BIN` 覆盖二进制路径，并沿用同步脚本的 `KEEP`、`TARGET_DIR`、`BRANCH`、`REPO_URL`。锁目录在退出时通过 macOS `/usr/bin/trash` 移入废纸篓，不做永久删除。

## 入库 ingest

传入一期杂志目录，路径中需要能识别出刊物（`economist` / `wired`）和期号（`YYYY.MM.DD`）：

```bash
./magazine2db ingest ./data/awesome-english-ebooks/01_economist/te_2026.09.05
./magazine2db ingest ./data/awesome-english-ebooks/05_wired/2026.09.02
```

行为说明：

- **只接受含 `.epub` 的目录**，不再支持 TXT 导入，也没有 TXT 回退；目录缺 EPUB 或解析失败都会直接报错。
- **解析流程**：`META-INF/container.xml` → OPF 的 manifest/spine → EPUB3 nav 或 EPUB2 NCX 目录 → 按 spine 顺序排列文章；正文转换为 Markdown，原始 XHTML 逐字节保存。
- **默认幂等**：`publisher + issue_date` 已存在且未加 `--force` 时直接跳过，此时不会去读取 EPUB。
- **`--force` 整期重导**：在同一事务中删除该期记录（文章经外键级联删除），再插入本次解析结果并执行保留清理，任一步失败则整体回滚。重导会改变期刊与文章 ID，元数据以本次解析为准，下游应重新获取列表。
- **保留策略**：每个刊物只留日期最新的 N 期（默认 4，`--keep N` 覆盖），被清理的旧期会级联删除其文章。

## 期刊列表 issue

```bash
./magazine2db issue
./magazine2db issue --json
```

按期刊日期倒序输出，plain 文本形如 `[id] publisher  date  N articles  imported-at`；`--json` 返回 `count` 和 `issues`，每项含 `id`、`publisher`、`issue_date`、`article_count`、`imported_at`。

## 文章列表 list

```bash
./magazine2db list --page 1 --page-size 20
./magazine2db list --issue 7 --page 1 --page-size 100
./magazine2db list --issue 7 --json
```

分页返回文章标题和正文节选：`--issue ID` 只看某一期，`--page` / `--page-size` 控制分页。`--json` 返回 `page`、`page_size`、`total`、`items`；每项含 `id`、`title`、`excerpt`。`excerpt` 是正文开头最多 1000 个 Unicode 字符（Markdown 源文，可能包含标题、列表等标记），完整正文用 `read` 获取。

## 文章读取 read

```bash
./magazine2db read economist:2026-09-05:leaders-politics
./magazine2db read 42
./magazine2db read --json 42
```

`read --json` 默认返回元数据和 Markdown `body`，但**不包含** `body_xhtml`。

### 原始 XHTML

`body_xhtml` 保存 EPUB 内该文件的原始 UTF-8 XHTML（不重新序列化），`source_href` 记录其在 ZIP 内的路径。需要核对原始数据时：

```bash
./magazine2db read --xhtml 42        # 直接输出原始 XHTML
./magazine2db read --json --xhtml 42 # JSON 中额外包含 body_xhtml
```

某篇文章没有 XHTML 时，显式 `--xhtml` 会报错，可用 `ingest --force` 从 EPUB 重新导入。

### 正文是如何得到的

`body` 在 HTML DOM 的**副本**上移除 `head`、`script`、`style`、`template`、`img`、`svg` 及显式隐藏节点后转换为 Markdown，保留标题、列表、引用、代码和表格结构；原始 DOM 与 `body_xhtml` 不受影响。

需要注意的边界：

- 不推断任何出版社专有版式，也**不保证**剔除全部导航、广告或 CSS 隐藏内容。
- 作者、描述、发布时间不从 OPF 整刊元数据推断，因此这些字段留空。
- 每篇文章必须对应一个独立 XHTML 文件；带锚点（fragment）的目录链接或重复目标文件会直接报错，不支持多个文章共用一个 XHTML 分段。

## 数据库版本与重建

当前数据库版本为 `user_version = 5`。程序**不兼容旧库、也不执行任何历史迁移**：打开非当前版本的数据库会明确报错。需要升级时，删除（或移走）旧的 `magazines.db`，再从原始 EPUB 重新执行 `ingest` 即可。

重建会重新生成期刊和文章 ID、正文统一为 Markdown；没有对应 EPUB 的历史记录无法重新导入，下游应重新获取期刊与文章列表。

数据库只保存期刊与文章两类数据：不创建全文索引，也不提供 `search`、`summarize`、`smoke` 命令。

## 测试

普通测试不访问网络，使用 `internal/parser/testdata` 下随源码保存的真实 Economist / WIRED 样本：

```bash
go test ./...
```

完整 E2E 会构建真实二进制，并复用 `internal/parser/testdata/economist/issue.epub`，覆盖 `ingest`（含 `--force`）→ `issue` → `list` → `read` 的 JSON 数据访问链路，同时验证默认数据库落在可执行文件旁、`--db` 覆盖以及从任意工作目录启动：

```bash
go test -tags=e2e -run TestCLIEndToEnd -v .
```

测试样本（含约 7.3 MB 的完整 Economist EPUB）的来源与结构见 `internal/parser/testdata/README.md`，它们不会编译进二进制。

## 许可证与内容归属

本项目源代码采用 [MIT License](LICENSE)。MIT 仅覆盖项目代码，不包含程序同步或处理的杂志、文章、图片、音频等第三方内容。

杂志内容来源于 [hehonghui/awesome-english-ebooks](https://github.com/hehonghui/awesome-english-ebooks)，相关内容仍受来源仓库说明、原始出版方及版权持有人的权利约束，详见 [NOTICE](NOTICE)。
