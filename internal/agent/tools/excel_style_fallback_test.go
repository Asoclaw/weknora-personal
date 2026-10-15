package tools

import (
	"archive/zip"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExcelRawCellFallbackPreservesSheetsAndBatchBoundaries(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	name := filepath.Join(t.TempDir(), "styles.xlsx")
	rows := [][]any{{"编号", "金额"}}
	for i := 0; i < 205; i++ {
		rows = append(rows, []any{i, i + 1})
	}
	writeWorkbook(t, name, map[string][][]any{"一": rows, "二": {{"编号", "描述"}, {"x", "保留"}}}, []string{"一", "二"})
	if err := loadExcelWithoutStyles(ctx, db, "fallback", name); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM fallback`).Scan(&count); err != nil || count != 206 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	var value string
	if err := db.QueryRow(`SELECT "描述" FROM fallback WHERE __sheet_name='二'`).Scan(&value); err != nil || value != "保留" {
		t.Fatal("sheet/schema lost")
	}
	// Remove style definitions but leave cell values and cached formulas intact.
	original, err := zip.OpenReader(name)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(t.TempDir(), "bad-styles.xlsx"))
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(out)
	for _, file := range original.File {
		reader, _ := file.Open()
		bytes, _ := io.ReadAll(reader)
		reader.Close()
		if file.Name == "xl/styles.xml" {
			bytes = []byte(strings.ReplaceAll(string(bytes), `count="1"`, `count="0"`))
		}
		entry, _ := w.Create(file.Name)
		entry.Write(bytes)
	}
	w.Close()
	out.Close()
	original.Close()
	if err := loadExcelWithoutStyles(ctx, db, "damaged_styles", out.Name()); err != nil {
		t.Fatal(err)
	}
}
