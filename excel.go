package main

// Export Excel:
// - Kolom: No | Date | URL | Category | Keyword | Title
// - Date    : tanggal publish artikel (YYYY-MM-DD, kosong bila tidak ketemu)
// - Category: sub-path pertama URL artikel, simbol jadi spasi
// - Keyword : segmen terakhir path URL, simbol jadi spasi
// - Ditulis ke io.Writer: dipakai untuk download HTTP langsung maupun
//   sebagai entry di dalam file .zip (multi website).

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/xuri/excelize/v2"
)

func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	h = strings.TrimPrefix(h, "www.")
	var b strings.Builder
	for _, r := range h {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func urlSegments(raw string) []string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	var segs []string
	for _, s := range strings.Split(u.Path, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	return segs
}

// categoryOf: sub-path pertama URL sebagai kategori yang mudah dibaca.
// contoh: https://site.com/food-stamps/post-title/ -> "food stamps".
// "" bila URL hanya punya satu segmen.
func categoryOf(raw string) string {
	segs := urlSegments(raw)
	if len(segs) >= 2 {
		return strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(segs[0]))
	}
	return ""
}

// keywordOf: segmen terakhir path URL sebagai keyword.
// contoh: https://energytransitions.uk/condolence-messages-for-grandfather/
// -> "condolence messages for grandfather"
func keywordOf(raw string) string {
	segs := urlSegments(raw)
	if len(segs) == 0 {
		return ""
	}
	last := segs[len(segs)-1]
	return strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(last))
}

// writeExcel menulis workbook ke w (bukan ke file path).
func writeExcel(w io.Writer, articles []ArticleResult, errs []ErrorResult) error {
	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Results"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return err
	}

	headers := []interface{}{"No", "Date", "URL", "Category", "Keyword", "Title"}
	if err := f.SetSheetRow(sheet, "A1", &headers); err != nil {
		return err
	}
	headStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"2F5597"}},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	if err == nil {
		_ = f.SetCellStyle(sheet, "A1", "F1", headStyle)
	}

	for i, a := range articles {
		vals := []interface{}{i + 1, a.Date, a.ArticleURL, categoryOf(a.ArticleURL), keywordOf(a.ArticleURL), a.Title}
		if err := f.SetSheetRow(sheet, fmt.Sprintf("A%d", i+2), &vals); err != nil {
			return err
		}
	}

	_ = f.SetColWidth(sheet, "A", "A", 6)
	_ = f.SetColWidth(sheet, "B", "B", 12)
	_ = f.SetColWidth(sheet, "C", "C", 70)
	_ = f.SetColWidth(sheet, "D", "D", 16)
	_ = f.SetColWidth(sheet, "E", "E", 45)
	_ = f.SetColWidth(sheet, "F", "F", 60)
	_ = f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	_ = f.AutoFilter(sheet, fmt.Sprintf("A1:F%d", len(articles)+1), nil)

	if len(errs) > 0 {
		const es = "Errors"
		if _, err := f.NewSheet(es); err == nil {
			eh := []interface{}{"No", "Main URL", "Article URL", "Error"}
			_ = f.SetSheetRow(es, "A1", &eh)
			_ = f.SetCellStyle(es, "A1", "D1", headStyle)
			for i, e := range errs {
				vals := []interface{}{i + 1, e.MainURL, e.ArticleURL, e.Error}
				_ = f.SetSheetRow(es, fmt.Sprintf("A%d", i+2), &vals)
			}
			_ = f.SetColWidth(es, "A", "A", 6)
			_ = f.SetColWidth(es, "B", "B", 38)
			_ = f.SetColWidth(es, "C", "C", 70)
			_ = f.SetColWidth(es, "D", "D", 45)
		}
	}

	return f.Write(w)
}
