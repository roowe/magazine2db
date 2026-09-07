# EPUB XML / XHTML 样本

这些样本随源码保存，供 parser 包内的目录解析、来源链接和 Markdown 转换测试使用，不会编译进二进制。

`economist/issue.epub` 是下述 Economist 源文件的完整副本（约 7.3 MB），供 `TestParseEPUBRealIssue` 验证真实 EPUB 的完整解析流程，预期包含 79 篇文章。

每家两篇文章。`.original.xhtml` 是 ZIP 中的原始字节；`.formatted.xml` 是仅用于阅读的 XML 缩进副本，不用于正文解析。图片资源未提取。

建议先看格式化的文章，再对照 `toc.ncx` 和 `package.opf`。文件未改写链接，浏览器无法完整还原页面。

## economist

来源：`/Users/luoliwei/myproject/magazine2db/data/awesome-english-ebooks/01_economist/te_2026.09.05/TheEconomist.2026.09.05.epub`
OPF 在 ZIP 内的位置：`EPUB/content.opf`

- [Nvidia is driving the AI boom. Good](economist/nvidia.formatted.xml)
  - 原始文件：`nvidia.original.xhtml`
  - ZIP 路径：`EPUB/b171c474-00e7-48c0-a595-04e4a679c1c3.html`
- [Donald Trump’s Venezuela deal is bold but dodgy](economist/venezuela.formatted.xml)
  - 原始文件：`venezuela.original.xhtml`
  - ZIP 路径：`EPUB/c0e2a96b-849a-4320-b8fb-d16548b74592.html`

## wired

来源：`/Users/luoliwei/myproject/magazine2db/data/awesome-english-ebooks/05_wired/2026.09.02/wired_2026.09.02.epub`
OPF 在 ZIP 内的位置：`content.opf`

- [I’m a Normie. Can Normies Really Vibe Code?](wired/vibe-code.formatted.xml)
  - 原始文件：`vibe-code.original.xhtml`
  - ZIP 路径：`feed_0/article_2/index_u46.html`
- [Arm’s CEO Insists the Market Needs His New CPU. It Could Piss Everyone Off](wired/arm-cpu.formatted.xml)
  - 原始文件：`arm-cpu.original.xhtml`
  - ZIP 路径：`feed_0/article_3/index_u36.html`
