package tencentdocs

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSaaSSelectionParentsReadOnlyIntegration(t *testing.T) {
	if os.Getenv("TENCENT_DOCS_SELECTION_VERIFY") != "1" { t.Skip("explicit read-only selection verification required") }
	var selected []string
	require.NoError(t, json.Unmarshal([]byte(os.Getenv("TENCENT_DOCS_SELECTED_IDS")), &selected))
	require.NotEmpty(t, selected)
	config := &types.DataSourceConfig{Settings: map[string]any{"site":siteSaaS}, Credentials:map[string]any{"token":os.Getenv("TENCENT_DOCS_TOKEN")}}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	connector := NewConnector()
	started := time.Now()
	ancestors, err := connector.ResolveResourceAncestors(ctx, config, selected)
	require.NoError(t, err)
	root, err := connector.ListResources(ctx, config, rootResourceID)
	require.NoError(t, err)
	found := false
	for _, node := range root {
		if node.Name == "格力FMS项目" {
			require.Contains(t, ancestors, node.ExternalID, "selected project's first-level ancestor must be half checked")
			found = true
		}
	}
	require.True(t, found, "expected project must remain visible")
	require.Contains(t, ancestors, rootResourceID)
	if output := os.Getenv("TENCENT_DOCS_SELECTION_FIXTURE_PATH"); output != "" {
		initial, err := connector.ListResources(ctx, config, "")
		require.NoError(t, err)
		children := map[string][]types.Resource{rootResourceID: root}
		ids := map[string]bool{}
		for _, node := range root { if node.HasChildren { ids[node.ExternalID] = true } }
		for _, id := range ancestors { if id != rootResourceID { ids[id] = true } }
		for id := range ids {
			items, err := connector.ListResources(ctx, config, id)
			require.NoError(t, err)
			children[id] = items
		}
		data, err := json.Marshal(map[string]any{"selected":selected,"ancestors":ancestors,"tree":initial,"children":children})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(output, data, 0600))
	}
	t.Logf("read-only verification: %d saved selections, %d resolved ancestors; root and FMS project confirmed, elapsed %s",len(selected),len(ancestors),time.Since(started).Round(time.Millisecond))
}
