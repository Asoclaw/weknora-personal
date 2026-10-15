package tencentdocs

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaaSNativeReadOnlyIntegration(t *testing.T) {
	if os.Getenv("TENCENT_NATIVE_VERIFY") != "1" {
		t.Skip("explicit read-only native verification required")
	}
	token := os.Getenv("TENCENT_DOCS_TOKEN")
	ids := strings.Fields(os.Getenv("TENCENT_NATIVE_IDS"))
	dir := os.Getenv("TENCENT_NATIVE_OUTPUT")
	if token == "" || len(ids) == 0 || dir == "" {
		t.Fatal("verification environment missing")
	}
	config := &types.DataSourceConfig{Credentials: map[string]interface{}{"token": token}, Settings: map[string]interface{}{"site": siteSaaS}}
	c := NewConnector()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	for _, id := range ids {
		items, err := c.FetchAll(ctx, config, []string{"file:" + id})
		if err != nil {
			t.Fatal("native export failed", err)
		}
		for _, item := range items {
			if reason := item.Metadata["error"]; reason != "" {
				t.Fatalf("%s: %s", item.Title, reason)
			}
			if len(item.Content) == 0 {
				t.Logf("%s: skipped (%s)", item.Title, item.Metadata["skip_reason_code"])
				continue
			}
			if item.URL != "" {
				t.Fatal("signed URL must never be an original link")
			}
			if err := os.WriteFile(filepath.Join(dir, id+filepath.Ext(item.FileName)), item.Content, 0600); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %s, bytes=%d, requires_ocr=%s", item.Title, filepath.Ext(item.FileName), len(item.Content), item.Metadata["tencent_docs_requires_ocr"])
		}
	}
}
