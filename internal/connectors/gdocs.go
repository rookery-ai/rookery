package connectors

// Google Docs, modelled as a list of paragraphs a model can address by TEXT.
//
// The Docs API addresses everything by character index, in UTF-16 code units, and
// returns a document as deeply nested JSON carrying every run's full style. Both
// properties defeat a small model: the JSON of a realistic document is far past the
// 8 KiB tool-result cap, so the model never sees most of it, and even a model that
// could see it cannot do UTF-16 arithmetic reliably. Every index in this file is
// therefore computed HERE, from text the model quoted, and never taken from the model
// — except by the explicitly "advanced" index-based actions, which stay for agents
// already built against them.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
)

// objectRune stands in for a non-text paragraph element (inline image, page break,
// footnote reference). Docs counts each as one index unit, so it must occupy one
// position in the text or every later offset in the paragraph drifts.
const objectRune = '￼'

// Text-style flags tracked per rune, so a formatting edit can be verified.
const (
	tsBold = 1 << iota
	tsItalic
	tsUnderline
	tsLink
)

type gpara struct {
	N       int    // 1-based position among all paragraphs, the number shown to the model
	Start   int    // UTF-16 index of the first character
	End     int    // UTF-16 index just past the trailing newline
	Style   string // namedStyleType, e.g. HEADING_2; "" reads as NORMAL_TEXT
	List    bool
	InTable bool
	// BlockStart/BlockEnd span the top-level structural element holding the
	// paragraph: the paragraph itself, or the whole TABLE for a cell paragraph. A
	// range deletion must cover whole tables — Docs refuses to cut one in half.
	BlockStart, BlockEnd int
	runes                []rune // paragraph text INCLUDING the trailing newline
	idx                  []int  // UTF-16 index of each rune
	ts                   []uint8
}

// text is the paragraph's text without its trailing newline.
func (p *gpara) text() string {
	r := p.runes
	if n := len(r); n > 0 && r[n-1] == '\n' {
		r = r[:n-1]
	}
	return string(r)
}

// display is text() with object placeholders spelled out, for the model to read.
func (p *gpara) display() string {
	return strings.ReplaceAll(p.text(), string(objectRune), "[object]")
}

func (p *gpara) style() string {
	if p.Style == "" {
		return "NORMAL_TEXT"
	}
	return p.Style
}

// headingLevel orders heading styles for section bounds: TITLE outranks HEADING_1,
// which outranks HEADING_2. 0 means "not a heading".
func headingLevel(style string) int {
	switch style {
	case "TITLE":
		return 1
	case "SUBTITLE":
		return 2
	}
	if strings.HasPrefix(style, "HEADING_") && len(style) == len("HEADING_")+1 {
		if d := style[len("HEADING_")]; d >= '1' && d <= '6' {
			return int(d-'1') + 3
		}
	}
	return 0
}

type gdoc struct {
	ID       string
	Title    string
	Revision string
	BodyEnd  int
	Paras    []*gpara
}

// Wire shapes — only the fields this file reads.
type docsWire struct {
	DocumentID string `json:"documentId"`
	Title      string `json:"title"`
	RevisionID string `json:"revisionId"`
	Body       struct {
		Content []structWire `json:"content"`
	} `json:"body"`
}

type structWire struct {
	StartIndex int `json:"startIndex"`
	EndIndex   int `json:"endIndex"`
	Paragraph  *struct {
		Elements []struct {
			StartIndex int `json:"startIndex"`
			EndIndex   int `json:"endIndex"`
			TextRun    *struct {
				Content   string `json:"content"`
				TextStyle struct {
					Bold      bool            `json:"bold"`
					Italic    bool            `json:"italic"`
					Underline bool            `json:"underline"`
					Link      json.RawMessage `json:"link"`
				} `json:"textStyle"`
			} `json:"textRun"`
		} `json:"elements"`
		ParagraphStyle struct {
			NamedStyleType string `json:"namedStyleType"`
		} `json:"paragraphStyle"`
		Bullet json.RawMessage `json:"bullet"`
	} `json:"paragraph"`
	Table *struct {
		TableRows []struct {
			TableCells []struct {
				Content []structWire `json:"content"`
			} `json:"tableCells"`
		} `json:"tableRows"`
	} `json:"table"`
	TableOfContents *struct {
		Content []structWire `json:"content"`
	} `json:"tableOfContents"`
}

func parseDoc(raw []byte) (*gdoc, error) {
	var w docsWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("could not read the document: %w", err)
	}
	d := &gdoc{ID: w.DocumentID, Title: w.Title, Revision: w.RevisionID}
	d.walk(w.Body.Content, false, 0, 0)
	for _, se := range w.Body.Content {
		if se.EndIndex > d.BodyEnd {
			d.BodyEnd = se.EndIndex
		}
	}
	return d, nil
}

func (d *gdoc) walk(content []structWire, inTable bool, tblStart, tblEnd int) {
	for _, se := range content {
		switch {
		case se.Paragraph != nil:
			p := &gpara{N: len(d.Paras) + 1, Start: se.StartIndex, End: se.EndIndex,
				Style: se.Paragraph.ParagraphStyle.NamedStyleType, InTable: inTable,
				List:       len(se.Paragraph.Bullet) > 0 && string(se.Paragraph.Bullet) != "null",
				BlockStart: se.StartIndex, BlockEnd: se.EndIndex}
			if inTable {
				p.BlockStart, p.BlockEnd = tblStart, tblEnd
			}
			for _, el := range se.Paragraph.Elements {
				pos := el.StartIndex
				if el.TextRun == nil {
					// One unit per non-text element; trust the element's own span if
					// it reports more, so a later element still lines up.
					p.runes = append(p.runes, objectRune)
					p.idx = append(p.idx, pos)
					p.ts = append(p.ts, 0)
					continue
				}
				var flags uint8
				st := el.TextRun.TextStyle
				if st.Bold {
					flags |= tsBold
				}
				if st.Italic {
					flags |= tsItalic
				}
				if st.Underline {
					flags |= tsUnderline
				}
				if len(st.Link) > 0 && string(st.Link) != "null" && string(st.Link) != "{}" {
					flags |= tsLink
				}
				for _, r := range el.TextRun.Content {
					p.runes = append(p.runes, r)
					p.idx = append(p.idx, pos)
					p.ts = append(p.ts, flags)
					pos += utf16Len(r)
				}
			}
			d.Paras = append(d.Paras, p)
		case se.Table != nil:
			for _, row := range se.Table.TableRows {
				for _, cell := range row.TableCells {
					if inTable {
						d.walk(cell.Content, true, tblStart, tblEnd) // nested table: the outer one bounds it
					} else {
						d.walk(cell.Content, true, se.StartIndex, se.EndIndex)
					}
				}
			}
		case se.TableOfContents != nil:
			// A table of contents is generated text; editing it is pointless and its
			// paragraphs would shadow the real headings in an anchor search.
		}
	}
}

// utf16Len is how many index units Docs counts for r: two for anything outside the
// Basic Multilingual Plane (most emoji), one otherwise. Cyrillic is one, which is why
// a byte- or rune-based offset passes every Latin and Macedonian test and still lands
// in the wrong place after the first emoji.
func utf16Len(r rune) int {
	if n := utf16.RuneLen(r); n > 0 {
		return n
	}
	return 1
}

func utf16LenString(s string) int {
	n := 0
	for _, r := range s {
		n += utf16Len(r)
	}
	return n
}

// topLevel reports whether a paragraph sits in the body rather than a table cell.
func (p *gpara) topLevel() bool { return !p.InTable }

// lastTopLevel is the final body paragraph — the one an append goes after.
func (d *gdoc) lastTopLevel() *gpara {
	for i := len(d.Paras) - 1; i >= 0; i-- {
		if d.Paras[i].topLevel() {
			return d.Paras[i]
		}
	}
	return nil
}

func (d *gdoc) firstTopLevel() *gpara {
	for _, p := range d.Paras {
		if p.topLevel() {
			return p
		}
	}
	return nil
}

// isEmpty reports a document holding nothing but the one empty paragraph every new
// document starts with.
func (d *gdoc) isEmpty() bool {
	for _, p := range d.Paras {
		if strings.TrimSpace(p.text()) != "" {
			return false
		}
	}
	return true
}

// ---- matching ---------------------------------------------------------------

type gmatch struct {
	P          *gpara
	RuneStart  int
	RuneEnd    int // exclusive
	Start, End int // UTF-16, End exclusive
	Normalized bool
}

func (m gmatch) quote() string {
	return clip(string(m.P.runes[m.RuneStart:m.RuneEnd]), 120)
}

// findMatches locates needle in the document's paragraphs. Exact matches win; only
// when there are none does it fall back to a normalised match (case, whitespace runs,
// smart quotes and dashes folded), because a model quoting a document reproduces its
// words far more reliably than its typography.
func (d *gdoc) findMatches(needle string) []gmatch {
	if needle == "" {
		return nil
	}
	var out []gmatch
	nr := []rune(needle)
	for _, p := range d.Paras {
		hay := p.runes
		if n := len(hay); n > 0 && hay[n-1] == '\n' {
			hay = hay[:n-1]
		}
		for i := 0; i+len(nr) <= len(hay); i++ {
			if runesEqual(hay[i:i+len(nr)], nr) {
				out = append(out, d.match(p, i, i+len(nr), false))
				i += len(nr) - 1
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	nn, _ := normalize([]rune(needle))
	nn = trimRunes(nn)
	if len(nn) == 0 {
		return nil
	}
	for _, p := range d.Paras {
		hay := p.runes
		if n := len(hay); n > 0 && hay[n-1] == '\n' {
			hay = hay[:n-1]
		}
		nh, back := normalize(hay)
		for i := 0; i+len(nn) <= len(nh); i++ {
			if runesEqual(nh[i:i+len(nn)], nn) {
				out = append(out, d.match(p, back[i], back[i+len(nn)-1]+1, true))
				i += len(nn) - 1
			}
		}
	}
	return out
}

func (d *gdoc) match(p *gpara, rs, re int, norm bool) gmatch {
	m := gmatch{P: p, RuneStart: rs, RuneEnd: re, Normalized: norm, Start: p.idx[rs]}
	last := p.runes[re-1]
	m.End = p.idx[re-1] + utf16Len(last)
	if last == objectRune {
		m.End = p.idx[re-1] + 1
	}
	return m
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func trimRunes(r []rune) []rune {
	for len(r) > 0 && r[0] == ' ' {
		r = r[1:]
	}
	for len(r) > 0 && r[len(r)-1] == ' ' {
		r = r[:len(r)-1]
	}
	return r
}

// normalize folds case, typographic quotes/dashes and whitespace runs, returning the
// folded runes and, for each, the index of the source rune it came from.
func normalize(in []rune) ([]rune, []int) {
	out := make([]rune, 0, len(in))
	back := make([]int, 0, len(in))
	prevSpace := false
	for i, r := range in {
		switch r {
		case '‘', '’', '‚', '‛', '′':
			r = '\''
		case '“', '”', '„', '‟', '″':
			r = '"'
		case '‐', '‑', '‒', '–', '—', '―', '−':
			r = '-'
		case '…':
			r = '.'
		}
		if unicode.IsSpace(r) || r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
			out = append(out, ' ')
			back = append(back, i)
			continue
		}
		prevSpace = false
		out = append(out, unicode.ToLower(r))
		back = append(back, i)
	}
	return out, back
}

func normString(s string) string {
	r, _ := normalize([]rune(s))
	return string(trimRunes(r))
}

var wordRE = regexp.MustCompile(`[\p{L}\p{N}]{3,}`)

// suggest returns up to three paragraphs sharing the most words with needle, so a
// failed anchor's error message tells the model what it probably meant.
func (d *gdoc) suggest(needle string) []string {
	words := map[string]bool{}
	for _, w := range wordRE.FindAllString(strings.ToLower(needle), -1) {
		words[w] = true
	}
	type scored struct {
		p *gpara
		n int
	}
	var ss []scored
	for _, p := range d.Paras {
		n := 0
		seen := map[string]bool{}
		for _, w := range wordRE.FindAllString(strings.ToLower(p.text()), -1) {
			if words[w] && !seen[w] {
				n++
				seen[w] = true
			}
		}
		if n > 0 {
			ss = append(ss, scored{p, n})
		}
	}
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].n > ss[j].n })
	var out []string
	for i := 0; i < len(ss) && i < 3; i++ {
		out = append(out, fmt.Sprintf("#%d %q", ss[i].p.N, clip(ss[i].p.display(), 100)))
	}
	return out
}

// resolveOne turns an anchor into exactly one match or an error the model can act on.
// occurrence is 1-based; 0 means "the anchor must be unique".
func (d *gdoc) resolveOne(anchor string, occurrence int, what string) (gmatch, error) {
	ms := d.findMatches(anchor)
	if len(ms) == 0 {
		msg := fmt.Sprintf("%s %q was not found in the document, so NOTHING was changed.", what, clip(anchor, 120))
		if sug := d.suggest(anchor); len(sug) > 0 {
			msg += " Closest paragraphs: " + strings.Join(sug, "; ") +
				". Copy the exact words from one of them, or call docs_get_document with find= to look."
		} else {
			msg += " Call docs_get_document to read the document and copy text exactly as it appears."
		}
		return gmatch{}, failf("%s", msg)
	}
	if occurrence > 0 {
		if occurrence > len(ms) {
			return gmatch{}, failf("%s %q occurs %d time(s); occurrence=%d does not exist. NOTHING was changed.",
				what, clip(anchor, 80), len(ms), occurrence)
		}
		return ms[occurrence-1], nil
	}
	if len(ms) > 1 {
		var list []string
		for i, m := range ms {
			if i == 8 {
				list = append(list, fmt.Sprintf("…and %d more", len(ms)-8))
				break
			}
			list = append(list, fmt.Sprintf("occurrence=%d in paragraph #%d %q", i+1, m.P.N, clip(m.P.display(), 90)))
		}
		return gmatch{}, failf("%s %q appears %d times, so it does not say WHERE to edit and NOTHING was changed. "+
			"Pass occurrence=N, or use a longer, unique piece of text. Matches: %s",
			what, clip(anchor, 80), len(ms), strings.Join(list, "; "))
	}
	return ms[0], nil
}

// findHeading resolves a section heading by its text. Real heading paragraphs win;
// failing that, a paragraph whose whole text equals the query (people often make a
// "heading" by typing a bold line), so a document without named styles still works.
func (d *gdoc) findHeading(q string, occurrence int) (*gpara, error) {
	q = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(q), "#"))
	nq := normString(q)
	if nq == "" {
		return nil, failf("heading is empty; pass the heading's text exactly as it appears")
	}
	var exact, contains, plain []*gpara
	for _, p := range d.Paras {
		if !p.topLevel() {
			continue
		}
		nt := normString(p.text())
		if headingLevel(p.Style) > 0 {
			if nt == nq {
				exact = append(exact, p)
			} else if strings.Contains(nt, nq) {
				contains = append(contains, p)
			}
		} else if nt == nq {
			plain = append(plain, p)
		}
	}
	for _, set := range [][]*gpara{exact, contains, plain} {
		if len(set) == 0 {
			continue
		}
		if occurrence > 0 {
			if occurrence > len(set) {
				return nil, failf("heading %q occurs %d time(s); occurrence=%d does not exist. NOTHING was changed.", q, len(set), occurrence)
			}
			return set[occurrence-1], nil
		}
		if len(set) > 1 {
			var list []string
			for i, p := range set {
				list = append(list, fmt.Sprintf("occurrence=%d paragraph #%d %q", i+1, p.N, clip(p.display(), 80)))
			}
			return nil, failf("heading %q matches %d headings, so NOTHING was changed. Pass occurrence=N or the full heading text. Matches: %s",
				q, len(set), strings.Join(list, "; "))
		}
		return set[0], nil
	}
	var heads []string
	for _, p := range d.Paras {
		if headingLevel(p.Style) > 0 && p.topLevel() {
			heads = append(heads, fmt.Sprintf("%q", clip(p.display(), 60)))
			if len(heads) == 15 {
				break
			}
		}
	}
	if len(heads) == 0 {
		return nil, failf("no heading %q found, and this document has no heading-styled paragraphs. NOTHING was changed. "+
			"Use docs_insert_text with anchor_text instead.", q)
	}
	return nil, failf("no heading %q found, so NOTHING was changed. Headings in this document: %s", q, strings.Join(heads, ", "))
}

// sectionRange is the deletable span of a section's body: from the start of its
// first block to the end of its last, so a table inside the section goes whole.
func sectionRange(body []*gpara) (int, int) {
	return body[0].BlockStart, body[len(body)-1].BlockEnd
}

// section returns the paragraphs under heading h, up to (not including) the next
// top-level heading of the same or higher rank.
func (d *gdoc) section(h *gpara) []*gpara {
	lvl := headingLevel(h.Style)
	if lvl == 0 {
		lvl = 99 // a plain-text "heading": the section runs to the next real heading
	}
	var out []*gpara
	for _, p := range d.Paras[h.N:] {
		if p.topLevel() {
			if l := headingLevel(p.Style); l > 0 && l <= lvl {
				break
			}
		}
		out = append(out, p)
	}
	return out
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ---- writing: text and markdown → batchUpdate requests ------------------------

type mdLine struct {
	text   string // plain text of the line (markers stripped)
	style  string // NORMAL_TEXT or HEADING_n; "" = leave as inherited (plain mode)
	list   string // "" (not a list item), "bullet", "number", or "keep" (leave bullets as inherited)
	bold   [][2]int
	links  []mdLink
	offset int // UTF-16 offset of the line within the inserted text
}

type mdLink struct {
	from, to int // UTF-16 offsets within the line
	url      string
}

var (
	mdHeadingRE = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdBulletRE  = regexp.MustCompile(`^\s*[-*+•]\s+(.*)$`)
	mdNumberRE  = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
	mdInlineRE  = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__|\[([^\]]+)\]\((https?://[^)\s]+)\)`)
)

// parseMarkdown turns the small subset of markdown models habitually write — headings,
// bullet and numbered lists, **bold**, [links](https://…) — into lines carrying the
// styling Docs needs. Anything else stays literal: a single *star* is too often real
// text (footnotes, multiplication) to be read as italics.
func parseMarkdown(text string) []mdLine {
	var out []mdLine
	off := 0
	for _, raw := range strings.Split(text, "\n") {
		l := mdLine{style: "NORMAL_TEXT", offset: off}
		body := strings.TrimRight(raw, " \t\r")
		if m := mdHeadingRE.FindStringSubmatch(body); m != nil {
			l.style = fmt.Sprintf("HEADING_%d", len(m[1]))
			body = strings.TrimRight(strings.TrimSpace(m[2]), "#")
			body = strings.TrimSpace(body)
		} else if m := mdBulletRE.FindStringSubmatch(body); m != nil {
			l.list = "bullet"
			body = m[1]
		} else if m := mdNumberRE.FindStringSubmatch(body); m != nil {
			l.list = "number"
			body = m[1]
		}
		var b strings.Builder
		last := 0
		for _, loc := range mdInlineRE.FindAllStringSubmatchIndex(body, -1) {
			b.WriteString(body[last:loc[0]])
			start := utf16LenString(b.String())
			switch {
			case loc[2] >= 0:
				b.WriteString(body[loc[2]:loc[3]])
				l.bold = append(l.bold, [2]int{start, utf16LenString(b.String())})
			case loc[4] >= 0:
				b.WriteString(body[loc[4]:loc[5]])
				l.bold = append(l.bold, [2]int{start, utf16LenString(b.String())})
			default:
				b.WriteString(body[loc[6]:loc[7]])
				l.links = append(l.links, mdLink{start, utf16LenString(b.String()), body[loc[8]:loc[9]]})
			}
			last = loc[1]
		}
		b.WriteString(body[last:])
		l.text = b.String()
		out = append(out, l)
		off += utf16LenString(l.text) + 1
	}
	// A trailing newline in the input is not a request for an empty paragraph.
	for len(out) > 1 && out[len(out)-1].text == "" && out[len(out)-1].list == "" && out[len(out)-1].style == "NORMAL_TEXT" {
		out = out[:len(out)-1]
	}
	return out
}

func plainLines(text string, style string) []mdLine {
	var out []mdLine
	off := 0
	for _, s := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out = append(out, mdLine{text: s, style: style, offset: off})
		off += utf16LenString(s) + 1
	}
	return out
}

func joinLines(ls []mdLine) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = l.text
	}
	return strings.Join(parts, "\n")
}

type docReq = map[string]any

func rng(a, b int) map[string]any { return map[string]any{"startIndex": a, "endIndex": b} }

// textStyleResetFields clears run formatting on newly inserted paragraphs. Text typed
// at the end of a bold, 18pt heading inherits bold and 18pt; resetting returns it to
// the paragraph's named style, which is what "add a paragraph" means.
const textStyleResetFields = "bold,italic,underline,strikethrough,smallCaps,link,fontSize,weightedFontFamily,foregroundColor,backgroundColor,baselineOffset"

// styleRequests returns the formatting requests for lines whose first character lands
// at base. paragraphMode is false for an inline insertion, which must not restyle the
// paragraph it lands in.
func styleRequests(base int, ls []mdLine, paragraphMode, reset bool) []docReq {
	var reqs []docReq
	total := 0
	if n := len(ls); n > 0 {
		total = ls[n-1].offset + utf16LenString(ls[n-1].text)
	}
	if paragraphMode && reset && total > 0 {
		reqs = append(reqs, docReq{"updateTextStyle": docReq{
			"range": rng(base, base+total), "textStyle": docReq{}, "fields": textStyleResetFields}})
	}
	if paragraphMode {
		// Bullets first come off every inserted paragraph that is not a list item —
		// a paragraph added after a list item inherits its bullet otherwise.
		for i := 0; i < len(ls); {
			j := i
			for j < len(ls) && ls[j].list == ls[i].list {
				j++
			}
			from := base + ls[i].offset
			to := base + ls[j-1].offset + utf16LenString(ls[j-1].text) + 1
			switch ls[i].list {
			case "keep":
				// plain text, or a list line joining the list it was placed beside
			case "":
				reqs = append(reqs, docReq{"deleteParagraphBullets": docReq{"range": rng(from, to)}})
			default:
				preset := "BULLET_DISC_CIRCLE_SQUARE"
				if ls[i].list == "number" {
					preset = "NUMBERED_DECIMAL_ALPHA_ROMAN"
				}
				reqs = append(reqs, docReq{"createParagraphBullets": docReq{"range": rng(from, to), "bulletPreset": preset}})
			}
			i = j
		}
		for _, l := range ls {
			if l.style == "" {
				continue
			}
			from := base + l.offset
			reqs = append(reqs, docReq{"updateParagraphStyle": docReq{
				"range":          rng(from, from+utf16LenString(l.text)+1),
				"paragraphStyle": docReq{"namedStyleType": l.style},
				"fields":         "namedStyleType"}})
		}
	}
	for _, l := range ls {
		for _, b := range l.bold {
			reqs = append(reqs, docReq{"updateTextStyle": docReq{
				"range": rng(base+l.offset+b[0], base+l.offset+b[1]), "textStyle": docReq{"bold": true}, "fields": "bold"}})
		}
		for _, k := range l.links {
			reqs = append(reqs, docReq{"updateTextStyle": docReq{
				"range": rng(base+l.offset+k.from, base+l.offset+k.to), "textStyle": docReq{"link": docReq{"url": k.url}}, "fields": "link"}})
		}
	}
	return reqs
}

var namedStyles = map[string]bool{
	"NORMAL_TEXT": true, "TITLE": true, "SUBTITLE": true,
	"HEADING_1": true, "HEADING_2": true, "HEADING_3": true,
	"HEADING_4": true, "HEADING_5": true, "HEADING_6": true,
}

// normStyle accepts the spellings a model reaches for — "heading 2", "h2", "Heading2",
// "normal", "body" — and returns the API's enum, or "" if it is none of them.
func normStyle(s string) string {
	u := strings.ToUpper(strings.TrimSpace(s))
	u = strings.NewReplacer(" ", "_", "-", "_").Replace(u)
	switch u {
	case "":
		return ""
	case "NORMAL", "BODY", "TEXT", "PARAGRAPH", "NORMAL_TEXT":
		return "NORMAL_TEXT"
	}
	if len(u) == 2 && u[0] == 'H' && u[1] >= '1' && u[1] <= '6' {
		return "HEADING_" + u[1:]
	}
	if strings.HasPrefix(u, "HEADING") {
		d := strings.TrimPrefix(strings.TrimPrefix(u, "HEADING"), "_")
		if len(d) == 1 && d[0] >= '1' && d[0] <= '6' {
			return "HEADING_" + d
		}
	}
	if namedStyles[u] {
		return u
	}
	return ""
}
