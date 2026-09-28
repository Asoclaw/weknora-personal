package tencentdocs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// This opt-in test reads the authorized account without creating a data source
// or writing any Tencent Docs content to the test database.
func TestSaaSReadOnlyIntegration(t *testing.T) {
	token := os.Getenv("TENCENT_DOCS_TOKEN")
	if token == "" || os.Getenv("TENCENT_DOCS_INTEGRATION") != "1" {
		t.Skip("requires explicit Tencent Docs integration test environment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	connector := NewConnector()
	config := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"token": token},
		Settings:    map[string]interface{}{"site": siteSaaS},
		ResourceIDs: []string{rootResourceID},
	}
	if err := connector.Validate(ctx, config); err != nil {
		t.Fatal(err)
	}
	items, _, err := connector.FetchIncremental(ctx, config, nil)
	var partial *datasource.PartialFetchError
	if err != nil && !errors.As(err, &partial) {
		t.Fatal(err)
	}
	readable := 0
	for _, item := range items {
		if len(item.Content) > 0 && item.FileName != "" {
			readable++
		}
	}
	if readable == 0 {
		t.Fatalf("no indexable documents among %d discovered items", len(items))
	}
	t.Logf("read-only SaaS sync discovered %d item(s), %d indexable", len(items), readable)
}
