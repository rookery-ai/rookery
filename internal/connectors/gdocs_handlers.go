package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// docsAPI is the Docs v1 documents endpoint. A var so tests can point it at a fake.
var docsAPI = "https://docs.googleapis.com/v1/documents/"

func init() {
	handlers["docs_get_document"] = docsGetDocument
	handlers["docs_insert_text"] = docsInsertText
	handlers["docs_append_text"] = docsAppendText
	handlers["docs_add_to_section"] = docsAddToSection
	handlers["docs_replace_section"] = docsReplaceSection
	handlers["docs_replace_text"] = docsReplaceText
	handlers["docs_delete_text"] = docsDeleteText
	handlers["docs_format_text"] = docsFormatText
}

func docURL(id string) string { return "https://docs.google.com/document/d/" + id + "/edit" }

func (hc *handlerCall) readDoc(ctx context.Context, id string) (*gdoc, error) {
	if id == "" {
		return nil, failf("document_id is required (the long id in the document's URL, between /d/ and /edit)")
	}
	raw, err := hc.call(ctx, "GET", docsAPI+url.PathEscape(id), nil)
	if err != nil {
		return nil, toConnectorError(err)
	}
	d, err := parseDoc(raw)
	if err != nil {
		return nil, &ConnectorError{KindOther, err.Error()}
	}
	if d.ID == "" {
		d.ID = id
	}
	return d, nil
}

// ---- reading ------------------------------------------------------------------

// readBudget bounds the paragraph listing so the whole result stays under the 8 KiB
// tool-result cap with room for the envelope. Hitting the cap BLIND is the original
// bug: the model saw the first dozen paragraphs and guessed the rest.
const readBudget = 6000

type paraView struct {
	N     int    `json:"n"`
	Style string `json:"style,omitempty"`
	List  bool   `json:"list,omitempty"`
	Table bool   `json:"in_table,omitempty"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

func docsGetDocument(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error) {
	d, err := hc.readDoc(ctx, argString(args, "document_id"))
	if err != nil {
		return nil, err
	}
	from, _ := argInt(args, "from_paragraph")
	if from < 1 {
		from = 1
	}
	find := normString(argString(args, "find"))
	headingsOnly, _ := argBool(args, "headings_only")

	out := map[string]any{
		"title":           d.Title,
		"document_id":     d.ID,
		"revision_id":     d.Revision,
		"url":             docURL(d.ID),
		"paragraph_count": len(d.Paras),
	}
	var views []paraView
	used := 0
	next := 0
	for _, p := range d.Paras {
		if p.N < from {
			continue
		}
		t := p.display()
		if strings.TrimSpace(t) == "" {
			continue // structural blank lines spend budget and say nothing
		}
		if headingsOnly && headingLevel(p.Style) == 0 {
			continue
		}
		if find != "" && !strings.Contains(normString(t), find) {
			continue
		}
		v := paraView{N: p.N, List: p.List, Table: p.InTable, Start: p.Start, End: p.End, Text: clip(t, 300)}
		if p.Style != "" && p.Style != "NORMAL_TEXT" {
			v.Style = p.Style
		}
		b, _ := json.Marshal(v)
		if used+len(b) > readBudget && len(views) > 0 {
			next = p.N
			break
		}
		used += len(b) + 1
		views = append(views, v)
	}
	out["paragraphs"] = views
	if next > 0 {
		out["next_from_paragraph"] = next
		out["note"] = fmt.Sprintf("Listing stopped at paragraph #%d to fit. Call again with from_paragraph=%d for the rest, or pass find=\"some words\" to jump to a passage.", next, next)
	} else if find != "" && len(views) == 0 {
		out["note"] = "No paragraph contains that text."
		if sug := d.suggest(argString(args, "find")); len(sug) > 0 {
			out["closest"] = sug
		}
	} else {
		out["note"] = "To edit, quote text you see here: docs_insert_text (anchor_text), docs_replace_text, docs_add_to_section (heading), docs_delete_text, docs_format_text. No index arithmetic is needed."
	}
	return marshalResult(out)
}

// ---- the edit loop --------------------------------------------------------------

type editPlan struct {
	requests []docReq
	describe string
	// verify inspects the document re-read after the write and returns the paragraph
	// to centre the result's context on, or an error if the edit is not visible.
	verify func(after *gdoc) (int, error)
}

type planFunc func(d *gdoc) (*editPlan, error)

// edit reads the document, plans against that exact revision, writes with
// writeControl.requiredRevisionId so a concurrent change refuses the write rather
// than shifting it onto the wrong text, and re-reads to verify. On a revision
// conflict it re-plans once from a fresh read — anchors are text, so a fresh plan is
// still the edit that was asked for.
func (hc *handlerCall) edit(ctx context.Context, args map[string]any, plan planFunc) (json.RawMessage, error) {
	id := argString(args, "document_id")
	pinned := argString(args, "revision_id")
	for attempt := 0; attempt < 2; attempt++ {
		d, err := hc.readDoc(ctx, id)
		if err != nil {
			return nil, err
		}
		p, err := plan(d)
		if err != nil {
			return nil, err
		}
		rev := d.Revision
		if pinned != "" {
			rev = pinned
		}
		body := map[string]any{"requests": p.requests}
		if rev != "" {
			body["writeControl"] = map[string]any{"requiredRevisionId": rev}
		}
		_, err = hc.call(ctx, "POST", docsAPI+url.PathEscape(d.ID)+":batchUpdate", body)
		if he, ok := err.(*httpError); ok && isRevisionConflict(he) {
			if pinned != "" {
				return nil, failf("the document changed after revision %s, so NOTHING was changed. "+
					"Call docs_get_document again and retry without revision_id (anchored edits do not need it).", pinned)
			}
			continue
		}
		if err != nil {
			return nil, toConnectorError(err)
		}
		after, err := hc.readDoc(ctx, d.ID)
		if err != nil {
			return nil, &ConnectorError{KindOther, "the edit was sent but the document could not be re-read to confirm it: " + err.Error()}
		}
		n, verr := p.verify(after)
		if verr != nil {
			return nil, &ConnectorError{KindOther, fmt.Sprintf(
				"Google accepted the edit (%s) but re-reading the document does not show it where expected: %v. "+
					"Do NOT report this as done. Call docs_get_document to see the current text.", p.describe, verr)}
		}
		return marshalResult(map[string]any{
			"status":      "applied",
			"verified":    true,
			"edit":        p.describe,
			"document_id": after.ID,
			"url":         docURL(after.ID),
			"context":     contextAround(after, n),
		})
	}
	return nil, failf("the document kept changing while this edit was being applied (someone else is editing it), so NOTHING was changed. Try again in a moment.")
}

func isRevisionConflict(he *httpError) bool {
	if he.Status != 400 && he.Status != 409 {
		return false
	}
	b := strings.ToLower(string(he.Body))
	return strings.Contains(b, "revision")
}

// contextAround renders paragraphs n-1 … n+2 the way the model reads them, so the
// result shows where the edit landed rather than asserting it.
func contextAround(d *gdoc, n int) []string {
	var out []string
	for _, p := range d.Paras {
		if p.N < n-1 || p.N > n+2 {
			continue
		}
		mark := ""
		if p.N == n {
			mark = "→ "
		}
		style := ""
		if p.Style != "" && p.Style != "NORMAL_TEXT" {
			style = "[" + p.Style + "] "
		}
		if p.List {
			style += "• "
		}
		out = append(out, fmt.Sprintf("%s#%d %s%s", mark, p.N, style, clip(p.display(), 140)))
	}
	return out
}

// ---- insertion ----------------------------------------------------------------

type insertion struct {
	index    int    // where insertText goes
	text     string // what insertText sends
	base     int    // where the first inserted line's first character lands
	lines    []mdLine
	paraMode bool
	reset    bool
}

// buildLines turns the caller's text into lines, honouring format and an explicit
// style override. anchor is the paragraph the new text is placed beside, if any.
func buildLines(args map[string]any, anchor *gpara) ([]mdLine, error) {
	text := asString(args["text"])
	if strings.TrimSpace(text) == "" {
		return nil, failf("text is empty; pass the text to write")
	}
	override := ""
	if s := argString(args, "style"); s != "" {
		if override = normStyle(s); override == "" {
			return nil, failf("style %q is not a paragraph style; use NORMAL_TEXT, TITLE, SUBTITLE or HEADING_1 … HEADING_6", s)
		}
	}
	format := strings.ToLower(argString(args, "format"))
	var ls []mdLine
	switch format {
	case "", "markdown", "md":
		ls = parseMarkdown(text)
		// A list line beside an existing list item joins that list rather than
		// starting a new one: keep the bullet it inherits.
		if anchor != nil && anchor.List {
			for i := range ls {
				if ls[i].list != "" {
					ls[i].list = "keep"
				}
			}
		}
	case "plain", "text":
		inherit := ""
		if anchor != nil && headingLevel(anchor.Style) > 0 {
			// Text added beside a heading inherits the HEADING style otherwise —
			// a whole paragraph of 20pt bold that the model reports as "added a note".
			inherit = "NORMAL_TEXT"
		}
		ls = plainLines(text, inherit)
		for i := range ls {
			ls[i].list = "keep"
		}
	default:
		return nil, failf("format %q is not supported; use markdown (default) or plain", format)
	}
	if override != "" {
		for i := range ls {
			ls[i].style = override
		}
	}
	return ls, nil
}

func afterParagraph(p *gpara, ls []mdLine) insertion {
	t := joinLines(ls)
	return insertion{index: p.End - 1, text: "\n" + t, base: p.End, lines: ls, paraMode: true, reset: true}
}

func beforeParagraph(p *gpara, ls []mdLine) insertion {
	t := joinLines(ls)
	return insertion{index: p.Start, text: t + "\n", base: p.Start, lines: ls, paraMode: true, reset: true}
}

// fillParagraph writes into an empty paragraph rather than adding one beside it, so
// appending to a brand-new document does not leave a blank first line.
func fillParagraph(p *gpara, ls []mdLine) insertion {
	return insertion{index: p.Start, text: joinLines(ls), base: p.Start, lines: ls, paraMode: true, reset: true}
}

func (ins insertion) requests() []docReq {
	reqs := []docReq{{"insertText": docReq{"location": docReq{"index": ins.index}, "text": ins.text}}}
	return append(reqs, styleRequests(ins.base, ins.lines, ins.paraMode, ins.reset)...)
}

// verifyInsert finds the first non-empty inserted line at its expected offset.
func (ins insertion) verify(after *gdoc) (int, error) {
	for _, l := range ins.lines {
		if strings.TrimSpace(l.text) == "" {
			continue
		}
		want := ins.base + l.offset
		for _, m := range after.findMatches(l.text) {
			if !m.Normalized && m.Start >= want-2 && m.Start <= want+2 {
				return m.P.N, nil
			}
		}
		return 0, fmt.Errorf("text %q is not at position %d", clip(l.text, 60), want)
	}
	return 0, nil
}

func (ins insertion) plan(describe string) *editPlan {
	return &editPlan{requests: ins.requests(), describe: describe, verify: ins.verify}
}

func positionArg(args map[string]any, def string) string {
	p := strings.ToLower(strings.ReplaceAll(argString(args, "position"), " ", "_"))
	switch p {
	case "":
		return def
	case "after", "below", "after_paragraph":
		return "after_paragraph"
	case "before", "above", "before_paragraph":
		return "before_paragraph"
	case "after_text", "inline_after":
		return "after_text"
	case "before_text", "inline_before":
		return "before_text"
	case "end", "end_of_section", "bottom":
		return "end"
	case "start", "start_of_section", "top", "beginning":
		return "start"
	}
	return p
}

func docsInsertText(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error) {
	anchor := asString(args["anchor_text"])
	if strings.TrimSpace(anchor) == "" {
		if idx, ok := argInt(args, "index"); ok {
			return hc.edit(ctx, args, func(d *gdoc) (*editPlan, error) { return planAtIndex(d, idx, asString(args["text"])) })
		}
		return nil, failf("pass anchor_text: a short piece of text from the document to insert next to " +
			"(read it with docs_get_document). To add at the end use docs_append_text; under a heading use docs_add_to_section.")
	}
	occ, _ := argInt(args, "occurrence")
	pos := positionArg(args, "after_paragraph")
	return hc.edit(ctx, args, func(d *gdoc) (*editPlan, error) {
		m, err := d.resolveOne(anchor, occ, "anchor_text")
		if err != nil {
			return nil, err
		}
		switch pos {
		case "after_paragraph", "before_paragraph":
			ls, err := buildLines(args, m.P)
			if err != nil {
				return nil, err
			}
			if pos == "after_paragraph" {
				return afterParagraph(m.P, ls).plan(fmt.Sprintf("inserted %d paragraph(s) after paragraph #%d", len(ls), m.P.N)), nil
			}
			return beforeParagraph(m.P, ls).plan(fmt.Sprintf("inserted %d paragraph(s) before paragraph #%d", len(ls), m.P.N)), nil
		case "after_text", "before_text":
			text := asString(args["text"])
			if text == "" {
				return nil, failf("text is empty; pass the text to write")
			}
			ls := []mdLine{{text: text}}
			if f := strings.ToLower(argString(args, "format")); f == "" || f == "markdown" || f == "md" {
				// Inline text gets **bold** and links only. A leading "- " or "# "
				// inside a sentence is literal, not a list or a heading.
				if !strings.Contains(text, "\n") {
					t, bold, links := parseInline(text)
					ls = []mdLine{{text: t, bold: bold, links: links}}
				}
			}
			at := m.End
			where := "after"
			if pos == "before_text" {
				at, where = m.Start, "before"
			}
			ins := insertion{index: at, text: joinLines(ls), base: at, lines: ls}
			return ins.plan(fmt.Sprintf("inserted text %s %q in paragraph #%d", where, clip(m.quote(), 60), m.P.N)), nil
		}
		return nil, failf("position %q is not one of after_paragraph, before_paragraph, after_text, before_text", pos)
	})
}

// planAtIndex is the legacy index path, kept for agents built against it. It still
// verifies, so an index that was a guess fails loudly instead of quietly.
func planAtIndex(d *gdoc, idx int, text string) (*editPlan, error) {
	if text == "" {
		return nil, failf("text is empty; pass the text to write")
	}
	if idx < 1 || idx >= d.BodyEnd {
		return nil, failf("index %d is outside the document body (1 … %d). Prefer anchor_text: quote text from the document instead of an index.", idx, d.BodyEnd-1)
	}
	ls := []mdLine{{text: text}}
	ins := insertion{index: idx, text: text, base: idx, lines: ls}
	return ins.plan(fmt.Sprintf("inserted text at index %d", idx)), nil
}

func docsAppendText(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error) {
	return hc.edit(ctx, args, func(d *gdoc) (*editPlan, error) {
		last := d.lastTopLevel()
		if last == nil {
			return nil, failf("the document has no body paragraphs to append after")
		}
		ls, err := buildLines(args, last)
		if err != nil {
			return nil, err
		}
		if d.isEmpty() {
			return fillParagraph(last, ls).plan("wrote the first text into the empty document"), nil
		}
		if strings.TrimSpace(last.text()) == "" {
			return fillParagraph(last, ls).plan(fmt.Sprintf("appended %d paragraph(s) at the end", len(ls))), nil
		}
		return afterParagraph(last, ls).plan(fmt.Sprintf("appended %d paragraph(s) at the end", len(ls))), nil
	})
}

func docsAddToSection(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error) {
	occ, _ := argInt(args, "occurrence")
	pos := positionArg(args, "end")
	return hc.edit(ctx, args, func(d *gdoc) (*editPlan, error) {
		h, err := d.findHeading(argString(args, "heading"), occ)
		if err != nil {
			return nil, err
		}
		body := d.section(h)
		after := h
		if pos == "end" {
			for i := len(body) - 1; i >= 0; i-- {
				if body[i].topLevel() {
					after = body[i]
					break
				}
			}
		} else if pos != "start" {
			return nil, failf("position %q must be end or start", pos)
		}
		// Style follows the section's own body text when there is some, not the
		// heading: a line added under "## Notes" is a note, not another heading.
		ls, err := buildLines(args, after)
		if err != nil {
			return nil, err
		}
		where := "end"
		if pos == "start" {
			where = "start"
		}
		if after != h && strings.TrimSpace(after.text()) == "" {
			return fillParagraph(after, ls).plan(fmt.Sprintf("added %d paragraph(s) at the %s of section %q", len(ls), where, clip(h.display(), 60))), nil
		}
		return afterParagraph(after, ls).plan(fmt.Sprintf("added %d paragraph(s) at the %s of section %q", len(ls), where, clip(h.display(), 60))), nil
	})
}

func docsReplaceSection(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error) {
	occ, _ := argInt(args, "occurrence")
	return hc.edit(ctx, args, func(d *gdoc) (*editPlan, error) {
		h, err := d.findHeading(argString(args, "heading"), occ)
		if err != nil {
			return nil, err
		}
		ls, err := buildLines(args, h)
		if err != nil {
			return nil, err
		}
		body := d.section(h)
		describe := fmt.Sprintf("replaced the %d paragraph(s) under %q with %d new paragraph(s)", len(body), clip(h.display(), 60), len(ls))
		var reqs []docReq
		var ins insertion
		switch {
		case len(body) == 0:
			ins = afterParagraph(h, ls)
		case body[len(body)-1].End >= d.BodyEnd:
			// The section runs to the end of the document, and the body's final
			// newline cannot be deleted: keep one emptied paragraph and write into it.
			s, _ := sectionRange(body)
			if d.BodyEnd-1 > s {
				reqs = append(reqs, docReq{"deleteContentRange": docReq{"range": rng(s, d.BodyEnd-1)}})
			}
			ins = insertion{index: s, text: joinLines(ls), base: s, lines: ls, paraMode: true, reset: true}
		default:
			s, e := sectionRange(body)
			reqs = append(reqs, docReq{"deleteContentRange": docReq{"range": rng(s, e)}})
			ins = afterParagraph(h, ls)
		}
		reqs = append(reqs, ins.requests()...)
		want := joinLines(ls)
		htext := h.text()
		return &editPlan{requests: reqs, describe: describe, verify: func(after *gdoc) (int, error) {
			for _, p := range after.Paras {
				if p.N == h.N && p.text() == htext {
					var got []string
					for _, q := range after.section(p) {
						got = append(got, q.text())
					}
					if strings.Join(got, "\n") != want {
						return 0, fmt.Errorf("section %q now reads %q", clip(htext, 40), clip(strings.Join(got, " / "), 120))
					}
					return p.N + 1, nil
				}
			}
			return 0, fmt.Errorf("heading %q is no longer at paragraph #%d", clip(htext, 40), h.N)
		}}, nil
	})
}

// ---- replace / delete / format --------------------------------------------------

func docsReplaceText(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error) {
	find := asString(args["find"])
	repl, hasRepl := args["replace"]
	if !hasRepl {
		repl, hasRepl = args["replacement"]
	}
	if find == "" || !hasRepl {
		return nil, failf("pass find (text exactly as it appears in the document) and replace (the new text; empty string deletes)")
	}
	replacement := asString(repl)
	occ, _ := argInt(args, "occurrence")
	all, _ := argBool(args, "replace_all")
	return hc.edit(ctx, args, func(d *gdoc) (*editPlan, error) {
		var ms []gmatch
		if all {
			ms = d.findMatches(find)
			if len(ms) == 0 {
				_, err := d.resolveOne(find, 0, "find")
				return nil, err
			}
		} else {
			m, err := d.resolveOne(find, occ, "find")
			if err != nil {
				return nil, err
			}
			ms = []gmatch{m}
		}
		// Apply from the LAST match backwards so earlier offsets stay valid within
		// the one batch, and compute where each replacement ends up for verifying.
		var reqs []docReq
		for i := len(ms) - 1; i >= 0; i-- {
			m := ms[i]
			reqs = append(reqs, docReq{"deleteContentRange": docReq{"range": rng(m.Start, m.End)}})
			if replacement != "" {
				reqs = append(reqs, docReq{"insertText": docReq{"location": docReq{"index": m.Start}, "text": replacement}})
			}
		}
		type spot struct {
			at, para int
		}
		var spots []spot
		shift := 0
		for _, m := range ms {
			spots = append(spots, spot{m.Start + shift, m.P.N})
			shift += utf16LenString(replacement) - (m.End - m.Start)
		}
		describe := fmt.Sprintf("replaced %q with %q in paragraph #%d", clip(ms[0].quote(), 60), clip(replacement, 60), ms[0].P.N)
		if len(ms) > 1 {
			describe = fmt.Sprintf("replaced %d occurrences of %q with %q", len(ms), clip(find, 60), clip(replacement, 60))
		}
		if ms[0].Normalized {
			describe += " (matched ignoring case/spacing/quote style)"
		}
		before := len(d.findMatches(find))
		return &editPlan{requests: reqs, describe: describe, verify: func(after *gdoc) (int, error) {
			if replacement == "" {
				if n := len(after.findMatches(find)); n > before-len(ms) {
					return 0, fmt.Errorf("%q still appears %d time(s)", clip(find, 40), n)
				}
				return spots[0].para, nil
			}
			found := after.findMatches(replacement)
			for _, s := range spots {
				ok := false
				for _, m := range found {
					if !m.Normalized && m.Start == s.at {
						ok = true
						break
					}
				}
				if !ok {
					return 0, fmt.Errorf("replacement %q is not at position %d", clip(replacement, 40), s.at)
				}
			}
			return spots[0].para, nil
		}}, nil
	})
}

func docsDeleteText(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error) {
	text := asString(args["text"])
	occ, _ := argInt(args, "occurrence")
	whole, _ := argBool(args, "whole_paragraph")
	return hc.edit(ctx, args, func(d *gdoc) (*editPlan, error) {
		m, err := d.resolveOne(text, occ, "text")
		if err != nil {
			return nil, err
		}
		s, e := m.Start, m.End
		what := fmt.Sprintf("deleted %q from paragraph #%d", clip(m.quote(), 60), m.P.N)
		if whole {
			p := m.P
			s, e = p.Start, p.End
			switch {
			case p.InTable:
				// A cell's last newline cannot be deleted; empty the paragraph instead.
				e = p.End - 1
			case p.End >= d.BodyEnd && p.Start > 1:
				// The body's final newline cannot go either: take the one before it.
				s, e = p.Start-1, p.End-1
			case p.End >= d.BodyEnd:
				e = p.End - 1
			}
			what = fmt.Sprintf("deleted paragraph #%d %q", p.N, clip(p.display(), 60))
		}
		if e <= s {
			return nil, failf("there is nothing to delete at that position")
		}
		before := len(d.findMatches(text))
		paras := len(d.Paras)
		return &editPlan{
			requests: []docReq{{"deleteContentRange": docReq{"range": rng(s, e)}}},
			describe: what,
			verify: func(after *gdoc) (int, error) {
				if n := len(after.findMatches(text)); n >= before && !(whole && len(after.Paras) < paras) {
					return 0, fmt.Errorf("%q still appears %d time(s)", clip(text, 40), n)
				}
				n := m.P.N
				if n > len(after.Paras) {
					n = len(after.Paras)
				}
				return n, nil
			},
		}, nil
	})
}

func docsFormatText(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error) {
	text := asString(args["text"])
	occ, _ := argInt(args, "occurrence")
	ts := docReq{}
	var fields []string
	var want []struct {
		flag uint8
		on   bool
	}
	for _, f := range []struct {
		arg, field string
		flag       uint8
	}{{"bold", "bold", tsBold}, {"italic", "italic", tsItalic}, {"underline", "underline", tsUnderline}} {
		if v, ok := argBool(args, f.arg); ok {
			ts[f.field] = v
			fields = append(fields, f.field)
			want = append(want, struct {
				flag uint8
				on   bool
			}{f.flag, v})
		}
	}
	if link := argString(args, "link_url"); link != "" {
		ts["link"] = docReq{"url": link}
		fields = append(fields, "link")
		want = append(want, struct {
			flag uint8
			on   bool
		}{tsLink, true})
	}
	style := ""
	if s := argString(args, "style"); s != "" {
		if style = normStyle(s); style == "" {
			return nil, failf("style %q is not a paragraph style; use NORMAL_TEXT, TITLE, SUBTITLE or HEADING_1 … HEADING_6", s)
		}
	}
	if len(fields) == 0 && style == "" {
		return nil, failf("say what to change: bold, italic, underline (true/false), link_url, or style (e.g. HEADING_2)")
	}
	return hc.edit(ctx, args, func(d *gdoc) (*editPlan, error) {
		m, err := d.resolveOne(text, occ, "text")
		if err != nil {
			return nil, err
		}
		var reqs []docReq
		if len(fields) > 0 {
			reqs = append(reqs, docReq{"updateTextStyle": docReq{"range": rng(m.Start, m.End), "textStyle": ts, "fields": strings.Join(fields, ",")}})
		}
		if style != "" {
			reqs = append(reqs, docReq{"updateParagraphStyle": docReq{
				"range": rng(m.P.Start, m.P.End), "paragraphStyle": docReq{"namedStyleType": style}, "fields": "namedStyleType"}})
		}
		var changes []string
		changes = append(changes, fields...)
		if style != "" {
			changes = append(changes, "paragraph style "+style)
		}
		return &editPlan{requests: reqs,
			describe: fmt.Sprintf("formatted %q in paragraph #%d (%s)", clip(m.quote(), 60), m.P.N, strings.Join(changes, ", ")),
			verify: func(after *gdoc) (int, error) {
				for _, p := range after.Paras {
					if p.Start > m.Start || p.End <= m.Start {
						continue
					}
					if style != "" && p.style() != style {
						return 0, fmt.Errorf("paragraph style is %s, not %s", p.style(), style)
					}
					for i, at := range p.idx {
						if at < m.Start || at >= m.End || p.runes[i] == '\n' {
							continue
						}
						for _, w := range want {
							if (p.ts[i]&w.flag != 0) != w.on {
								return 0, fmt.Errorf("the text style did not change at position %d", at)
							}
						}
					}
					return p.N, nil
				}
				return 0, fmt.Errorf("no paragraph at position %d", m.Start)
			}}, nil
	})
}
