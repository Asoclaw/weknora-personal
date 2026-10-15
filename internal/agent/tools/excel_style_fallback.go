package tools

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

func quotedExcelIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// Read raw cell values rather than formatting styles. Rows are streamed twice:
// discover the union schema, then insert in bounded windows without touching
// the original workbook or discarding sheets/columns.
func loadExcelWithoutStyles(ctx context.Context, db *sql.DB, tableName, filename string) error {
	book, err := excelize.OpenFile(filename, excelize.Options{RawCellValue: true, UnzipSizeLimit: 512 << 20, UnzipXMLSizeLimit: 16 << 20})
	if err != nil {
		return err
	}
	defer book.Close()
	type sheetSchema struct {
		name    string
		columns []string
	}
	schemas := []sheetSchema{}
	union := []string{}
	known := map[string]bool{}
	for _, name := range book.GetSheetList() {
		rows, err := book.Rows(name)
		if err != nil {
			return err
		}
		headers := []string{}
		maxColumns := 0
		first := true
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				rows.Close()
				return err
			}
			cells, err := rows.Columns(excelize.Options{RawCellValue: true})
			if err != nil {
				rows.Close()
				return err
			}
			if first {
				headers = cells
				first = false
			}
			maxColumns = max(maxColumns, len(cells))
		}
		readErr := rows.Error()
		rows.Close()
		if readErr != nil {
			return readErr
		}
		if maxColumns == 0 {
			continue
		}
		columns := make([]string, maxColumns)
		local := map[string]bool{excelSheetNameColumn: true}
		for i := range columns {
			base := fmt.Sprintf("column_%d", i+1)
			if i < len(headers) && strings.TrimSpace(headers[i]) != "" {
				base = headers[i]
			}
			value := base
			for suffix := 2; local[strings.ToLower(value)]; suffix++ {
				value = fmt.Sprintf("%s_%d", base, suffix)
			}
			local[strings.ToLower(value)] = true
			columns[i] = value
			if !known[strings.ToLower(value)] {
				known[strings.ToLower(value)] = true
				union = append(union, value)
			}
		}
		schemas = append(schemas, sheetSchema{name, columns})
	}
	if len(union) == 0 {
		return fmt.Errorf("Excel workbook has no readable cells")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	definitions := []string{}
	for _, column := range union {
		definitions = append(definitions, quotedExcelIdentifier(column)+" VARCHAR")
	}
	definitions = append(definitions, quotedExcelIdentifier(excelSheetNameColumn)+" VARCHAR")
	if _, err := tx.ExecContext(ctx, "CREATE TABLE "+quotedExcelIdentifier(tableName)+" ("+strings.Join(definitions, ",")+")"); err != nil {
		return err
	}
	for _, sheet := range schemas {
		rows, err := book.Rows(sheet.name)
		if err != nil {
			return err
		}
		columns := []string{}
		for _, column := range sheet.columns {
			columns = append(columns, quotedExcelIdentifier(column))
		}
		columns = append(columns, quotedExcelIdentifier(excelSheetNameColumn))
		placeholder := "(" + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + ")"
		values := []any{}
		count := 0
		flush := func() error {
			if count == 0 {
				return nil
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO "+quotedExcelIdentifier(tableName)+" ("+strings.Join(columns, ",")+") VALUES "+strings.TrimSuffix(strings.Repeat(placeholder+",", count), ","), values...)
			values = nil
			count = 0
			return err
		}
		first := true
		for rows.Next() {
			if first {
				first = false
				continue
			}
			if err := ctx.Err(); err != nil {
				rows.Close()
				return err
			}
			cells, err := rows.Columns(excelize.Options{RawCellValue: true})
			if err != nil {
				rows.Close()
				return err
			}
			for i := range sheet.columns {
				if i < len(cells) {
					values = append(values, cells[i])
				} else {
					values = append(values, nil)
				}
			}
			values = append(values, sheet.name)
			count++
			if count == 100 {
				if err := flush(); err != nil {
					rows.Close()
					return err
				}
			}
		}
		readErr := rows.Error()
		rows.Close()
		if readErr != nil {
			return readErr
		}
		if err := flush(); err != nil {
			return err
		}
	}
	return tx.Commit()
}
