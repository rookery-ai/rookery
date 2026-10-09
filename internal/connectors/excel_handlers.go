package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// graphDriveItems is the Graph drive-items endpoint. A var so tests can point it at a fake.
var graphDriveItems = "https://graph.microsoft.com/v1.0/me/drive/items/"

func init() {
	handlers["excel_write_cells"] = excelWriteCells
}

var cellRE = regexp.MustCompile(`^\$?([A-Za-z]{1,3})\$?([0-9]{1,7})$`)

// colNumber converts column letters to a 1-based number (A=1, Z=26, AA=27).
func colNumber(letters string) int {
	n := 0
	for _, c := range strings.ToUpper(letters) {
		n = n*26 + int(c-'A'+1)
	}
	return n
}

// colLetters is the inverse of colNumber.
func colLetters(n int) string {
	var b []byte
	for n > 0 {
		n--
		b = append([]byte{byte('A' + n%26)}, b...)
		n /= 26
	}
	return string(b)
}

// a1Range computes the address a block of rows×cols occupies from its top-left cell,
// which is the arithmetic excel_update_range asked the model to do — and Excel rejects
// any address whose shape differs from the values by even one cell.
func a1Range(start string, rows, cols int) (string, error) {
	if i := strings.LastIndex(start, "!"); i >= 0 {
		start = start[i+1:] // "Sheet1!B3" — the sheet is its own argument
	}
	m := cellRE.FindStringSubmatch(strings.TrimSpace(start))
	if m == nil {
		return "", failf("start_cell %q is not a cell like B3", start)
	}
	if rows < 1 || cols < 1 {
		return "", failf("values is empty; pass rows like [[\"a\",1],[\"b\",2]]")
	}
	c0 := colNumber(m[1])
	r0, _ := strconv.Atoi(m[2])
	if c0+cols-1 > 16384 {
		return "", failf("the block would run past column XFD, Excel's last")
	}
	first := colLetters(c0) + strconv.Itoa(r0)
	if rows == 1 && cols == 1 {
		return first, nil
	}
	return first + ":" + colLetters(c0+cols-1) + strconv.Itoa(r0+rows-1), nil
}

func excelWriteCells(ctx context.Context, hc *handlerCall, args map[string]any) (json.RawMessage, error) {
	item, sheet := argString(args, "item_id"), argString(args, "sheet")
	rawRows, _ := args["values"].([]any)
	cols := 0
	rows := make([][]any, 0, len(rawRows))
	for _, r := range rawRows {
		row, ok := r.([]any)
		if !ok {
			row = []any{r} // a bare value is a one-cell row, not an error
		}
		if len(row) > cols {
			cols = len(row)
		}
		rows = append(rows, row)
	}
	// Excel needs a rectangle; pad ragged rows rather than reject them.
	for i := range rows {
		for len(rows[i]) < cols {
			rows[i] = append(rows[i], "")
		}
	}
	addr, err := a1Range(argString(args, "start_cell"), len(rows), cols)
	if err != nil {
		return nil, err
	}
	u := graphDriveItems + url.PathEscape(item) + "/workbook/worksheets/" + url.PathEscape(sheet) +
		"/range(address='" + url.PathEscape(addr) + "')"
	raw, err := hc.call(ctx, "PATCH", u, map[string]any{"values": rows})
	if err != nil {
		return nil, toConnectorError(err)
	}
	var resp struct {
		Address string          `json:"address"`
		Values  json.RawMessage `json:"values"`
	}
	json.Unmarshal(raw, &resp)
	if resp.Address == "" {
		resp.Address = sheet + "!" + addr
	}
	// The Graph range object also carries formulas, number formats and text for every
	// cell; the model needs only what landed where.
	return marshalResult(map[string]any{
		"status":  "applied",
		"address": resp.Address,
		"rows":    len(rows),
		"columns": cols,
		"values":  resp.Values,
		"note":    fmt.Sprintf("Wrote %d×%d cells starting at %s.", len(rows), cols, strings.SplitN(addr, ":", 2)[0]),
	})
}
