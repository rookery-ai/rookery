package connectors

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
)

// fakeDocs is an in-memory Google Docs that APPLIES batchUpdate requests, so the
// handler tests check what a document ends up saying rather than which JSON was sent.
// It models a document the way Docs does: a run of characters, each paragraph ended by
// a newline that carries the paragraph's style and bullet, indexes in UTF-16 code
// units starting at 1 (index 0 is the section break).
type fakeDocs struct {
	mu       sync.Mutex
	chars    []fchar
	rev      int
	id       string
	batches  int
	dropNext bool // accept the next batch but do not apply it
	// beforeWrite runs once, just before the next batch is checked — a concurrent
	// editor changing the document between our read and our write.
	beforeWrite func(f *fakeDocs)
	lastBody    map[string]any
}

type fchar struct {
	r      rune
	bold   bool
	italic bool
	link   string
	style  string // on '\n' only
	bullet bool   // on '\n' only
}

func newFakeDocs(paras ...string) *fakeDocs {
	f := &fakeDocs{id: "DOC1", rev: 1}
	for _, p := range paras {
		style := ""
		bullet := false
		switch {
		case strings.HasPrefix(p, "# "):
			style, p = "HEADING_1", p[2:]
		case strings.HasPrefix(p, "## "):
			style, p = "HEADING_2", p[3:]
		case strings.HasPrefix(p, "TITLE "):
			style, p = "TITLE", p[6:]
		case strings.HasPrefix(p, "- "):
			bullet, p = true, p[2:]
		}
		for _, r := range p {
			f.chars = append(f.chars, fchar{r: r})
		}
		f.chars = append(f.chars, fchar{r: '\n', style: style, bullet: bullet})
	}
	return f
}

func u16(r rune) int { return utf16.RuneLen(r) }

// pos returns the char slot whose UTF-16 start is idx (len(chars) for the end).
func (f *fakeDocs) pos(idx int) (int, error) {
	at := 1
	for i, c := range f.chars {
		if at == idx {
			return i, nil
		}
		at += u16(c.r)
	}
	if at == idx {
		return len(f.chars), nil
	}
	return 0, fmt.Errorf("index %d is not a character boundary", idx)
}

func (f *fakeDocs) bodyEnd() int {
	at := 1
	for _, c := range f.chars {
		at += u16(c.r)
	}
	return at
}

// paragraphs returns the plain text of each paragraph, for assertions.
func (f *fakeDocs) paragraphs() []string {
	var out []string
	var b strings.Builder
	for _, c := range f.chars {
		if c.r == '\n' {
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(c.r)
	}
	return out
}

func (f *fakeDocs) paraStyles() []string {
	var out []string
	for _, c := range f.chars {
		if c.r == '\n' {
			s := c.style
			if s == "" {
				s = "NORMAL_TEXT"
			}
			if c.bullet {
				s += "+bullet"
			}
			out = append(out, s)
		}
	}
	return out
}

func (f *fakeDocs) json() []byte {
	type el = map[string]any
	var content []any
	content = append(content, el{"endIndex": 1, "sectionBreak": el{}})
	at := 1
	start := 0
	for i, c := range f.chars {
		if c.r != '\n' {
			continue
		}
		pStart := at
		var elems []any
		// group runs of identical style
		j := start
		for j <= i {
			k := j
			var b strings.Builder
			runStart := at
			for k <= i && f.chars[k].bold == f.chars[j].bold && f.chars[k].italic == f.chars[j].italic && f.chars[k].link == f.chars[j].link {
				b.WriteRune(f.chars[k].r)
				at += u16(f.chars[k].r)
				k++
			}
			ts := el{}
			if f.chars[j].bold {
				ts["bold"] = true
			}
			if f.chars[j].italic {
				ts["italic"] = true
			}
			if f.chars[j].link != "" {
				ts["link"] = el{"url": f.chars[j].link}
			}
			elems = append(elems, el{"startIndex": runStart, "endIndex": at, "textRun": el{"content": b.String(), "textStyle": ts}})
			j = k
		}
		style := c.style
		if style == "" {
			style = "NORMAL_TEXT"
		}
		para := el{"elements": elems, "paragraphStyle": el{"namedStyleType": style}}
		if c.bullet {
			para["bullet"] = el{"listId": "kix.list1"}
		}
		content = append(content, el{"startIndex": pStart, "endIndex": at, "paragraph": para})
		start = i + 1
	}
	b, _ := json.Marshal(el{"documentId": f.id, "title": "Test doc", "revisionId": fmt.Sprintf("rev%d", f.rev),
		"body": el{"content": content}})
	return b
}

func (f *fakeDocs) apply(reqs []map[string]json.RawMessage) error {
	for _, r := range reqs {
		for kind, raw := range r {
			var v struct {
				Location struct{ Index int } `json:"location"`
				Text     string             `json:"text"`
				Range    struct {
					StartIndex, EndIndex int
				} `json:"range"`
				ParagraphStyle struct{ NamedStyleType string } `json:"paragraphStyle"`
				TextStyle      map[string]json.RawMessage   `json:"textStyle"`
				Fields         string                       `json:"fields"`
				ContainsText   struct {
					Text      string
					MatchCase bool
				} `json:"containsText"`
				ReplaceText string `json:"replaceText"`
			}
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			switch kind {
			case "insertText":
				p, err := f.pos(v.Location.Index)
				if err != nil {
					return err
				}
				if p >= len(f.chars) {
					return fmt.Errorf("insert index must be inside the body")
				}
				// newline attrs come from the paragraph being split; text attrs from
				// the preceding character, as in Docs
				end := p
				for end < len(f.chars) && f.chars[end].r != '\n' {
					end++
				}
				nl := f.chars[end]
				var tmpl fchar
				if p > 0 && f.chars[p-1].r != '\n' {
					tmpl = f.chars[p-1]
				}
				var ins []fchar
				for _, r := range v.Text {
					c := fchar{r: r, bold: tmpl.bold, italic: tmpl.italic, link: tmpl.link}
					if r == '\n' {
						c = fchar{r: '\n', style: nl.style, bullet: nl.bullet}
					}
					ins = append(ins, c)
				}
				f.chars = append(f.chars[:p], append(ins, f.chars[p:]...)...)
			case "deleteContentRange":
				if v.Range.EndIndex >= f.bodyEnd() {
					return fmt.Errorf("cannot delete the final newline of the body")
				}
				a, err := f.pos(v.Range.StartIndex)
				if err != nil {
					return err
				}
				b, err := f.pos(v.Range.EndIndex)
				if err != nil {
					return err
				}
				f.chars = append(f.chars[:a], f.chars[b:]...)
			case "updateParagraphStyle", "createParagraphBullets", "deleteParagraphBullets":
				a, _ := f.pos(v.Range.StartIndex)
				b, _ := f.pos(v.Range.EndIndex)
				for i := a; i < len(f.chars); i++ {
					if f.chars[i].r != '\n' {
						continue
					}
					switch kind {
					case "updateParagraphStyle":
						f.chars[i].style = v.ParagraphStyle.NamedStyleType
					case "createParagraphBullets":
						f.chars[i].bullet = true
					default:
						f.chars[i].bullet = false
					}
					if i >= b-1 {
						break
					}
				}
			case "updateTextStyle":
				a, _ := f.pos(v.Range.StartIndex)
				b, _ := f.pos(v.Range.EndIndex)
				for i := a; i < b; i++ {
					for _, fld := range strings.Split(v.Fields, ",") {
						switch fld {
						case "bold":
							f.chars[i].bold = string(v.TextStyle["bold"]) == "true"
						case "italic":
							f.chars[i].italic = string(v.TextStyle["italic"]) == "true"
						case "link":
							var l struct{ URL string }
							json.Unmarshal(v.TextStyle["link"], &l)
							f.chars[i].link = l.URL
						}
					}
				}
			case "replaceAllText":
				return fmt.Errorf("replaceAllText not modelled")
			default:
				return fmt.Errorf("unsupported request %s", kind)
			}
		}
	}
	return nil
}

func (f *fakeDocs) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "GET":
			w.Write(f.json())
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, ":batchUpdate"):
			f.batches++
			if f.beforeWrite != nil {
				f.beforeWrite(f)
				f.beforeWrite = nil
			}
			body, _ := io.ReadAll(r.Body)
			var req struct {
				Requests     []map[string]json.RawMessage `json:"requests"`
				WriteControl struct {
					RequiredRevisionID string `json:"requiredRevisionId"`
				} `json:"writeControl"`
			}
			json.Unmarshal(body, &req)
			json.Unmarshal(body, &f.lastBody)
			if rid := req.WriteControl.RequiredRevisionID; rid != "" && rid != fmt.Sprintf("rev%d", f.rev) {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"code":400,"message":"The required revision ID does not match the latest revision.","status":"FAILED_PRECONDITION"}}`))
				return
			}
			if f.dropNext {
				f.dropNext = false
				w.Write([]byte(`{"replies":[{}]}`))
				return
			}
			saved := append([]fchar(nil), f.chars...)
			if err := f.apply(req.Requests); err != nil {
				f.chars = saved
				w.WriteHeader(400)
				w.Write([]byte(`{"error":{"code":400,"message":"` + err.Error() + `"}}`))
				return
			}
			f.rev++
			w.Write([]byte(`{"replies":[{}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

// runDocs executes a google_docs action against the fake through the real Execute.
func runDocs(t *testing.T, f *fakeDocs, action string, args map[string]any) (string, error) {
	t.Helper()
	srv := f.server(t)
	t.Cleanup(srv.Close)
	old := docsAPI
	docsAPI = srv.URL + "/v1/documents/"
	t.Cleanup(func() { docsAPI = old })
	if _, ok := args["document_id"]; !ok {
		args["document_id"] = f.id
	}
	res, err := Execute(t.Context(), testRegistry(t), fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{ID: "c", Provider: "google_docs"}, action, args, Policy{})
	return string(res.Data), err
}
