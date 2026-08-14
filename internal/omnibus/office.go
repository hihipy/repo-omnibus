package omnibus

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// An xlsx or docx is a zip of XML parts, which the standard library can read.
// That matters here: a repository whose whole product is a spreadsheet is
// otherwise represented by nothing but its README.
//
// PDFs are not handled. Their text sits in compressed streams with font-level
// encodings, so reading them needs a real PDF library, and a wrong answer is
// worse than an honest link.

const (
	maxSheetRows = 60 // beyond this a sheet is data, not logic
	maxSheetCols = 24
)

// officeExt are the formats ExtractOffice can read.
var officeExt = map[string]bool{".xlsx": true, ".xlsm": true, ".docx": true}

// maxOfficeBytes bounds what is worth unzipping in memory.
const maxOfficeBytes = 8 << 20

// ExtractOffice turns an Office file into Markdown. The second return reports
// whether the file was handled.
func ExtractOffice(name string, data []byte) (string, bool) {
	var (
		text string
		err  error
	)
	switch strings.ToLower(path.Ext(name)) {
	case ".xlsx", ".xlsm":
		text, err = extractXLSX(data)
	case ".docx":
		text, err = extractDOCX(data)
	default:
		return "", false
	}
	if err != nil || strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

func openZip(data []byte) (*zip.Reader, error) {
	return zip.NewReader(bytes.NewReader(data), int64(len(data)))
}

func readPart(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("part not found: %s", name)
}

// sharedStrings holds every literal string in a workbook. Cells reference them
// by index rather than carrying the text, so this table has to be read first.
func sharedStrings(zr *zip.Reader) []string {
	raw, err := readPart(zr, "xl/sharedStrings.xml")
	if err != nil {
		return nil
	}

	var out []string
	var current strings.Builder
	inItem := false

	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "si" {
				inItem = true
				current.Reset()
			}
		case xml.CharData:
			if inItem {
				current.Write(t)
			}
		case xml.EndElement:
			if t.Name.Local == "si" {
				out = append(out, current.String())
				inItem = false
			}
		}
	}
	return out
}

// sheetNames reads the workbook's sheet names in order. Names pair with
// worksheet parts by position, which holds for files Excel writes.
func sheetNames(zr *zip.Reader) []string {
	raw, err := readPart(zr, "xl/workbook.xml")
	if err != nil {
		return nil
	}
	var names []string
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
			for _, a := range se.Attr {
				if a.Name.Local == "name" {
					names = append(names, a.Value)
				}
			}
		}
	}
	return names
}

// colIndex turns a cell reference like "AB7" into a zero-based column number.
func colIndex(ref string) int {
	n := 0
	for _, ch := range ref {
		if ch < 'A' || ch > 'Z' {
			break
		}
		n = n*26 + int(ch-'A') + 1
	}
	return n - 1
}

func atoiSafe(s string) int {
	if s == "" {
		return -1
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return -1
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// readSheet returns the sheet as rows of cells. A cell carrying a formula is
// rendered as the formula, since that is the logic worth reading; otherwise the
// stored value is used.
func readSheet(raw []byte, shared []string) [][]string {
	var rows [][]string
	var row []string

	var cellRef, cellType, formula, value string
	inV, inF, inIS := false, false, false

	flushCell := func() {
		text := value
		switch {
		case formula != "":
			text = "=" + formula
		case cellType == "s":
			if i := atoiSafe(value); i >= 0 && i < len(shared) {
				text = shared[i]
			}
		}
		col := colIndex(cellRef)
		if col < 0 {
			col = len(row)
		}
		for len(row) <= col {
			row = append(row, "")
		}
		row[col] = strings.TrimSpace(text)
	}

	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row = nil
			case "c":
				cellRef, cellType, formula, value = "", "", "", ""
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "r":
						cellRef = a.Value
					case "t":
						cellType = a.Value
					}
				}
			case "v":
				inV = true
			case "f":
				inF = true
			case "is":
				inIS = true
			}
		case xml.CharData:
			switch {
			case inF:
				formula += string(t)
			case inV, inIS:
				value += string(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "f":
				inF = false
			case "is":
				inIS = false
			case "c":
				flushCell()
			case "row":
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func allEmpty(row []string) bool {
	for _, c := range row {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// extractXLSX renders every sheet as a Markdown table, bounded so a data dump
// cannot swamp the bundle.
func extractXLSX(data []byte) (string, error) {
	zr, err := openZip(data)
	if err != nil {
		return "", err
	}

	var parts []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			parts = append(parts, f.Name)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("no worksheets")
	}
	sort.Strings(parts)

	shared := sharedStrings(zr)
	names := sheetNames(zr)

	var sb strings.Builder
	for i, part := range parts {
		raw, err := readPart(zr, part)
		if err != nil {
			continue
		}
		rows := readSheet(raw, shared)

		name := fmt.Sprintf("Sheet %d", i+1)
		if i < len(names) {
			name = names[i]
		}
		fmt.Fprintf(&sb, "### %s\n\n", name)

		width, used := 0, 0
		for _, r := range rows {
			if len(r) > width {
				width = len(r)
			}
			if !allEmpty(r) {
				used++
			}
		}
		if used == 0 {
			sb.WriteString("Empty sheet.\n\n")
			continue
		}
		if width > maxSheetCols {
			width = maxSheetCols
		}

		shown := 0
		for _, r := range rows {
			if allEmpty(r) {
				continue
			}
			if shown >= maxSheetRows {
				fmt.Fprintf(&sb, "\n%d further rows not shown.\n", used-shown)
				break
			}
			cells := make([]string, width)
			for c := 0; c < width; c++ {
				if c < len(r) {
					cells[c] = escapePipe(r[c])
				}
			}
			fmt.Fprintf(&sb, "| %s |\n", strings.Join(cells, " | "))
			if shown == 0 {
				fmt.Fprintf(&sb, "|%s\n", strings.Repeat(" --- |", width))
			}
			shown++
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n") + "\n", nil
}

// extractDOCX returns the document's paragraphs as plain text.
func extractDOCX(data []byte) (string, error) {
	zr, err := openZip(data)
	if err != nil {
		return "", err
	}
	raw, err := readPart(zr, "word/document.xml")
	if err != nil {
		return "", err
	}

	var out []string
	var para strings.Builder
	inText := false

	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "p":
				para.Reset()
			case "tab":
				para.WriteString("\t")
			case "br":
				para.WriteString("\n")
			}
		case xml.CharData:
			if inText {
				para.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if line := strings.TrimSpace(para.String()); line != "" {
					out = append(out, line)
				}
			}
		}
	}
	if len(out) == 0 {
		return "", fmt.Errorf("no text found")
	}
	return strings.Join(out, "\n\n") + "\n", nil
}
