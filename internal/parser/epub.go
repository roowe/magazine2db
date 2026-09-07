package parser

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"magazine2db/internal/domain"
)

type packageDocument struct {
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MediaType  string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Spine struct {
		TOC   string `xml:"toc,attr"`
		Items []struct {
			ID string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

type tocEntry struct{ title, section, file string }
type ncxPoint struct {
	Title   string `xml:"navLabel>text"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Children []ncxPoint `xml:"navPoint"`
}

// EPUB paths are URL references relative to their containing document, not OS paths.
func resolveHref(document, href string) (string, error) {
	u, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	if u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "/") || u.RawQuery != "" {
		return "", fmt.Errorf("unsupported EPUB reference %q", href)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("EPUB fragment targets are unsupported: %q", href)
	}
	file := document
	if u.Path != "" {
		file = path.Join(path.Dir(document), u.Path)
	}
	if file == ".." || strings.HasPrefix(file, "../") || strings.Contains(file, "\\") {
		return "", fmt.Errorf("EPUB reference escapes archive: %q", href)
	}
	return file, nil
}

func readMember(z *zip.ReadCloser, name string) ([]byte, error) {
	f, err := z.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open EPUB member %s: %w", name, err)
	}
	defer f.Close()
	// Bound uncompressed XML/XHTML; images and other assets are never expanded.
	const maxDocument = 32 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxDocument+1))
	if err != nil {
		return nil, fmt.Errorf("read EPUB member %s: %w", name, err)
	}
	if len(data) > maxDocument {
		return nil, fmt.Errorf("EPUB member %s exceeds 32 MiB", name)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("EPUB member %s is not UTF-8", name)
	}
	return data, nil
}

func parseEPUB(input Input, filename string) (domain.Issue, error) {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("open EPUB: %w", err)
	}
	defer z.Close()
	container, err := readMember(z, "META-INF/container.xml")
	if err != nil {
		return domain.Issue{}, err
	}
	var root struct {
		Files []struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(container, &root); err != nil {
		return domain.Issue{}, fmt.Errorf("parse container: %w", err)
	}
	if len(root.Files) != 1 {
		return domain.Issue{}, fmt.Errorf("expected one EPUB package, got %d", len(root.Files))
	}
	opf := root.Files[0].Path
	data, err := readMember(z, opf)
	if err != nil {
		return domain.Issue{}, err
	}
	var pkg packageDocument
	if err := xml.Unmarshal(data, &pkg); err != nil {
		return domain.Issue{}, fmt.Errorf("parse OPF: %w", err)
	}
	files := map[string]string{}
	nav, ncx := "", ""
	for _, item := range pkg.Manifest {
		file, err := resolveHref(opf, item.Href)
		if err != nil {
			return domain.Issue{}, err
		}
		files[item.ID] = file
		if slices.Contains(strings.Fields(item.Properties), "nav") {
			nav = file
		}
		if item.ID == pkg.Spine.TOC || (pkg.Spine.TOC == "" && item.MediaType == "application/x-dtbncx+xml") {
			ncx = file
		}
	}
	order := map[string]int{}
	for i, item := range pkg.Spine.Items {
		file, ok := files[item.ID]
		if !ok {
			return domain.Issue{}, fmt.Errorf("spine references missing manifest item %q", item.ID)
		}
		order[file] = i
	}
	var entries []tocEntry
	if nav != "" {
		data, err := readMember(z, nav)
		if err != nil {
			return domain.Issue{}, err
		}
		entries, err = parseNavigation(data, nav)
		if err != nil {
			return domain.Issue{}, err
		}
	} else if ncx != "" {
		data, err := readMember(z, ncx)
		if err != nil {
			return domain.Issue{}, err
		}
		entries, err = parseNCX(data, ncx)
		if err != nil {
			return domain.Issue{}, err
		}
	} else {
		return domain.Issue{}, fmt.Errorf("EPUB has neither navigation document nor NCX")
	}
	if len(entries) == 0 {
		return domain.Issue{}, fmt.Errorf("EPUB TOC contains no articles")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if _, ok := order[e.file]; !ok {
			return domain.Issue{}, fmt.Errorf("TOC document %s is absent from spine", e.file)
		}
		if seen[e.file] {
			return domain.Issue{}, fmt.Errorf("duplicate TOC target %s", e.file)
		}
		seen[e.file] = true
	}
	sort.SliceStable(entries, func(i, j int) bool { return order[entries[i].file] < order[entries[j].file] })
	issue := domain.Issue{Publisher: input.Publisher, IssueDate: input.IssueDate, SourcePath: input.Path}
	slugs := map[string]bool{}
	for _, e := range entries {
		data, err := readMember(z, e.file)
		if err != nil {
			return domain.Issue{}, err
		}
		doc, err := html.Parse(bytes.NewReader(data))
		if err != nil {
			return domain.Issue{}, err
		}
		body, err := markdownFromDocument(doc)
		if err != nil {
			return domain.Issue{}, fmt.Errorf("convert %s to Markdown: %w", e.file, err)
		}
		slug := stableSlug(e.title)
		sourceURL := originalURL(doc)
		if sourceURL != "" {
			u, _ := url.Parse(sourceURL)
			if candidate := stableSlug(path.Base(strings.TrimRight(u.Path, "/"))); candidate != "" {
				slug = candidate
			}
		}
		if slug == "" || slugs[slug] {
			sum := sha256.Sum256([]byte(e.file))
			slug = fmt.Sprintf("%s-%x", slug, sum[:8])
		}
		slugs[slug] = true
		issue.Articles = append(issue.Articles, domain.Article{
			StableID: input.Publisher + ":" + input.IssueDate + ":" + slug, Slug: slug,
			Title: e.title, Section: e.section, Body: body, BodyXHTML: string(data), SourceHref: e.file, SourceURL: sourceURL,
		})
	}
	return issue, nil
}

func parseNCX(data []byte, document string) ([]tocEntry, error) {
	var ncx struct {
		Points []ncxPoint `xml:"navMap>navPoint"`
	}
	if err := xml.Unmarshal(data, &ncx); err != nil {
		return nil, fmt.Errorf("parse NCX: %w", err)
	}
	var entries []tocEntry
	var walk func([]ncxPoint, string) error
	walk = func(points []ncxPoint, section string) error {
		for _, p := range points {
			if len(p.Children) > 0 {
				if err := walk(p.Children, joinSection(section, p.Title)); err != nil {
					return err
				}
				continue
			}
			e, err := makeEntry(document, p.Content.Src, p.Title, section)
			if err != nil {
				return err
			}
			entries = append(entries, e)
		}
		return nil
	}
	err := walk(ncx.Points, "")
	return entries, err
}

func joinSection(parent, title string) string {
	if parent == "" {
		return strings.TrimSpace(title)
	}
	return parent + " / " + strings.TrimSpace(title)
}

func makeEntry(document, href, title, section string) (tocEntry, error) {
	if href == "" || strings.TrimSpace(title) == "" {
		return tocEntry{}, fmt.Errorf("TOC entry has empty title or target")
	}
	file, err := resolveHref(document, href)
	return tocEntry{strings.TrimSpace(title), section, file}, err
}

// parseNavigation 解析 EPUB 的 HTML 目录，递归保留栏目层级，并将叶子目录项转换为文章条目。
// document 是目录文件在 EPUB 中的路径，用于解析文章的相对链接。
//
// 示例目录：
//
//	<ol>
//	  <li>
//	    <span>The world this week</span>
//	    <ol>
//	      <li><a href="politics.xhtml">Politics</a></li>
//	    </ol>
//	  </li>
//	</ol>
//
// 外层 li 的 label 是栏目名，递归到内层 li 后，label 才是文章链接。
// 最终文章标题为 Politics，栏目为 The world this week；链接相对于 document 解析。
func parseNavigation(data []byte, document string) ([]tocEntry, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	toc := doc.Find(`nav[epub\:type~="toc"], nav[role="doc-toc"]`).First()
	if toc.Length() == 0 {
		return nil, fmt.Errorf("navigation document has no TOC")
	}
	var entries []tocEntry
	var walk func(*goquery.Selection, string) error
	walk = func(list *goquery.Selection, section string) error {
		for _, item := range list.ChildrenFiltered("li").EachIter() {
			// item 是当前 <li>；只选择直接子节点，避免 Find 把嵌套 <ol> 中的文章标题混进来。
			// 多个 a/span 时取最后一个，沿用原遍历规则；这是一种约定，不保证最后一个一定是真正标题。
			label := item.ChildrenFiltered("a, span").Last()
			nested := item.ChildrenFiltered("ol").Last()
			if label.Length() == 0 {
				return fmt.Errorf("TOC list item has no label")
			}
			title := strings.Join(strings.Fields(label.Text()), " ")
			// 例如外层 <span>栏目名</span> + <ol> 继承栏目向下递归；叶子 <a> 提供文章标题和链接。
			if nested.Length() > 0 {
				if err := walk(nested, joinSection(section, title)); err != nil {
					return err
				}
			} else {
				e, err := makeEntry(document, label.AttrOr("href", ""), title, section)
				if err != nil {
					return err
				}
				entries = append(entries, e)
			}
		}
		return nil
	}
	for _, list := range toc.ChildrenFiltered("ol").EachIter() {
		if err := walk(list, ""); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

// originalURL 从 XHTML 的来源标记中提取文章原始网址，找不到时返回空字符串。
// 例如 <link rel="canonical" href="https://www.wired.com/story/example/">，
// 或 Calibre 添加的 <a rel="calibre-downloaded-from" href="https://...">。
func originalURL(doc *html.Node) string {
	// 只匹配 a/link 上的来源标记；~= 按空白分隔的词匹配，兼容 rel="canonical alternate"。
	links := goquery.NewDocumentFromNode(doc).
		Find(`a[rel~="canonical"], link[rel~="canonical"], a[rel~="calibre-downloaded-from"], link[rel~="calibre-downloaded-from"]`)
	// 按文档顺序取第一个有效的 HTTP/HTTPS 地址，跳过相对路径和其他协议。
	for _, link := range links.EachIter() {
		value := link.AttrOr("href", "")
		u, err := url.Parse(value)
		if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			return value
		}
	}
	return ""
}

// markdownFromDocument 在 DOM 副本上清理图片和显式隐藏内容，原始 XHTML 保持不变。
// 段落、标题、列表、引用、代码和表格由转换库处理，不自行维护 HTML 排版规则。
func markdownFromDocument(doc *html.Node) (string, error) {
	cleaned := goquery.NewDocumentFromNode(doc).Clone()
	cleaned.Find(`head, script, style, template, img, svg, [hidden], [aria-hidden="true"]`).Remove()
	conv := converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
		table.NewTablePlugin(),
	))
	markdown, err := conv.ConvertNode(cleaned.Get(0))
	return string(markdown), err
}
