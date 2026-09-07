package parser

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

type articleExample struct {
	publisher, sample, href, title, section, excerpt, sourceURL string
}

var articleExamples = []articleExample{
	{"economist", "nvidia", "EPUB/b171c474-00e7-48c0-a595-04e4a679c1c3.html", "Nvidia is driving the AI boom. Good", "Leaders", "was named in 1993 after the Latin word for envy", ""},
	{"economist", "venezuela", "EPUB/c0e2a96b-849a-4320-b8fb-d16548b74592.html", "Donald Trump’s Venezuela deal is bold but dodgy", "Leaders", "Oil made Venezuela rich.", ""},
	{"wired", "vibe-code", "feed_0/article_2/index_u46.html", "I’m a Normie. Can Normies Really Vibe Code?", "", "That’s all my mother registered", "https://www.wired.com/story/normie-vibe-code/"},
	{"wired", "arm-cpu", "feed_0/article_3/index_u36.html", "Arm’s CEO Insists the Market Needs His New CPU. It Could Piss Everyone Off", "", "Rene Haas is half-prone on a couch", "https://www.wired.com/story/arms-ceo-insists-the-market-needs-his-new-cpu-it-could-piss-everyone-off/"},
}

func readExample(t *testing.T, publisher, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", publisher, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func exampleDocument(t *testing.T, example articleExample) *html.Node {
	t.Helper()
	doc, err := html.Parse(bytes.NewReader(readExample(t, example.publisher, example.sample+".original.xhtml")))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func assertExampleEntries(t *testing.T, entries []tocEntry, publisher string, count int) {
	t.Helper()
	if len(entries) != count {
		t.Fatalf("articles=%d, want %d", len(entries), count)
	}
	for _, example := range articleExamples {
		if example.publisher != publisher {
			continue
		}
		want := tocEntry{title: example.title, section: example.section, file: example.href}
		found := false
		for _, entry := range entries {
			if entry == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing TOC entry: %+v", want)
		}
	}
}

func TestParseNCXExamples(t *testing.T) {
	for _, tc := range []struct {
		publisher, document string
		count               int
	}{
		{"economist", "EPUB/toc.ncx", 79},
		{"wired", "toc.ncx", 58},
	} {
		t.Run(tc.publisher, func(t *testing.T) {
			entries, err := parseNCX(readExample(t, tc.publisher, "toc.ncx"), tc.document)
			if err != nil {
				t.Fatal(err)
			}
			assertExampleEntries(t, entries, tc.publisher, tc.count)
		})
	}
}

func TestParseNavigationExample(t *testing.T) {
	entries, err := parseNavigation(readExample(t, "economist", "nav.xhtml"), "EPUB/nav.xhtml")
	if err != nil {
		t.Fatal(err)
	}
	assertExampleEntries(t, entries, "economist", 79)
	ncx, err := parseNCX(readExample(t, "economist", "toc.ncx"), "EPUB/toc.ncx")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entries, ncx) {
		t.Fatal("HTML navigation differs from NCX")
	}
}

func TestOriginalURLExamples(t *testing.T) {
	for _, example := range articleExamples {
		t.Run(example.sample, func(t *testing.T) {
			if got := originalURL(exampleDocument(t, example)); got != example.sourceURL {
				t.Fatalf("source URL=%q, want %q", got, example.sourceURL)
			}
		})
	}
}

func TestMarkdownFromDocumentExamples(t *testing.T) {
	for _, example := range articleExamples {
		t.Run(example.sample, func(t *testing.T) {
			doc := exampleDocument(t, example)
			var before, after bytes.Buffer
			if err := html.Render(&before, doc); err != nil {
				t.Fatal(err)
			}
			markdown, err := markdownFromDocument(doc)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(markdown, example.title) || !strings.Contains(markdown, example.excerpt) {
				t.Fatalf("article title or body missing in Markdown:\n%s", markdown)
			}
			if strings.Contains(markdown, "![") || strings.Contains(markdown, "<img") {
				t.Fatal("Markdown contains images")
			}
			if err := html.Render(&after, doc); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("conversion changed original DOM")
			}
		})
	}
}
