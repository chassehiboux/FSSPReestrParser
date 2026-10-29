package registry

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
)

// value сохраняет различие числовой и текстовой ячейки при строгом сравнении.
type value struct {
	Text   string
	Number float64
	Kind   byte
}

func (v value) any() any {
	switch v.Kind {
	case 'n':
		return v.Number
	case 'b':
		return v.Text == "1"
	case 0:
		return nil
	}
	return v.Text
}
func (v value) empty() bool { return v.Kind == 0 || v.Kind != 'n' && v.Text == "" }

type richText struct {
	Text string `xml:"t"`
	Runs []struct {
		Text string `xml:"t"`
	} `xml:"r"`
}

func (r richText) text() string {
	s := r.Text
	for _, run := range r.Runs {
		s += run.Text
	}
	return s
}

type xmlCell struct {
	Ref     string   `xml:"r,attr"`
	Type    string   `xml:"t,attr"`
	V       *string  `xml:"v"`
	Formula *string  `xml:"f"`
	Inline  richText `xml:"is"`
}
type xmlRow struct {
	Number int       `xml:"r,attr"`
	Cells  []xmlCell `xml:"c"`
}

// scanBook читает первый лист потоково, не полагаясь на необязательный dimension.
func scanBook(ctx context.Context, filename string, visit func(int, []value, bool) error) error {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer z.Close()
	files := map[string]*zip.File{}
	for _, f := range z.File {
		files[f.Name] = f
	}
	readXML := func(name string, dst any) error {
		f := files[name]
		if f == nil {
			return fmt.Errorf("в XLSX отсутствует %s", name)
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		defer r.Close()
		return xml.NewDecoder(r).Decode(dst)
	}
	var book struct {
		Props struct {
			Date1904 bool `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			ID string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err = readXML("xl/workbook.xml", &book); err != nil {
		return err
	}
	if len(book.Sheets) == 0 {
		return fmt.Errorf("в книге нет листов")
	}
	var rels struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err = readXML("xl/_rels/workbook.xml.rels", &rels); err != nil {
		return err
	}
	sheetPath := ""
	for _, r := range rels.Items {
		if r.ID == book.Sheets[0].ID {
			sheetPath = path.Clean(path.Join("xl", r.Target))
			if strings.HasPrefix(r.Target, "/") {
				sheetPath = strings.TrimPrefix(r.Target, "/")
			}
		}
	}
	stringsTable := []string{}
	if f := files["xl/sharedStrings.xml"]; f != nil {
		r, e := f.Open()
		if e != nil {
			return e
		}
		d := xml.NewDecoder(r)
		for {
			if err = ctx.Err(); err != nil {
				r.Close()
				return err
			}
			t, e := d.Token()
			if e == io.EOF {
				break
			}
			if e != nil {
				r.Close()
				return e
			}
			if start, ok := t.(xml.StartElement); ok && start.Name.Local == "si" {
				var rich richText
				if e = d.DecodeElement(&rich, &start); e != nil {
					r.Close()
					return e
				}
				stringsTable = append(stringsTable, rich.text())
			}
		}
		if err = r.Close(); err != nil {
			return err
		}
	}
	f := files[sheetPath]
	if f == nil {
		return fmt.Errorf("не найден первый лист")
	}
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	d := xml.NewDecoder(r)
	rowNumber := 0
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		t, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		start, ok := t.(xml.StartElement)
		if !ok || start.Name.Local != "row" {
			continue
		}
		var row xmlRow
		if err = d.DecodeElement(&row, &start); err != nil {
			return err
		}
		rowNumber++
		if row.Number > 0 {
			rowNumber = row.Number
		}
		values := []value{}
		for _, c := range row.Cells {
			col := 0
			for _, ch := range c.Ref {
				if ch < 'A' || ch > 'Z' {
					break
				}
				col = col*26 + int(ch-'A'+1)
			}
			if col < 1 || col > 16384 {
				return fmt.Errorf("некорректная ячейка %s", c.Ref)
			}
			for len(values) < col {
				values = append(values, value{})
			}
			if c.Formula != nil && c.V == nil {
				return fmt.Errorf("ячейка %s: формула без сохранённого результата; пересохраните книгу в Excel", c.Ref)
			}
			v := value{}
			if c.V != nil {
				v = value{Text: *c.V, Kind: 's'}
				switch c.Type {
				case "s":
					i, e := strconv.Atoi(*c.V)
					if e != nil || i < 0 || i >= len(stringsTable) {
						return fmt.Errorf("неверная строка в %s", c.Ref)
					}
					v.Text = stringsTable[i]
				case "", "n":
					if *c.V != "" {
						n, e := strconv.ParseFloat(*c.V, 64)
						if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
							return fmt.Errorf("некорректное число в %s", c.Ref)
						}
						v = value{Number: n, Kind: 'n'}
					} else {
						v = value{}
					}
				case "e":
					return fmt.Errorf("ячейка %s содержит ошибку Excel: %s", c.Ref, *c.V)
				case "b":
					v.Kind = 'b'
				}
			}
			if c.Type == "inlineStr" {
				v = value{Text: c.Inline.text(), Kind: 's'}
			}
			values[col-1] = v
		}
		if err = visit(rowNumber, values, book.Props.Date1904); err != nil {
			return err
		}
	}
	return nil
}

func headers(row []value, required []string) (map[string]int, error) {
	cols := map[string]int{}
	for i, v := range row {
		if v.empty() {
			continue
		}
		if _, ok := cols[v.Text]; ok {
			for _, name := range required {
				if name == v.Text {
					return nil, fmt.Errorf("повторяется обязательный столбец «%s»", name)
				}
			}
		}
		cols[v.Text] = i
	}
	for _, name := range required {
		if _, ok := cols[name]; !ok {
			return nil, fmt.Errorf("нет столбца «%s»", name)
		}
	}
	return cols, nil
}
func at(row []value, i int) value {
	if i < len(row) {
		return row[i]
	}
	return value{}
}
