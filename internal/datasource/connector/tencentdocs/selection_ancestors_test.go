package tencentdocs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSaaSSelectionAncestorsFollowEveryParentWithoutScanningOtherBranches(t *testing.T) {
	parents := map[string]string{"chosen-folder": "deep", "chosen-file": "deep", "deep": "phase", "phase": "project"}
	calls := map[string]int{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		args := request.Params.Arguments
		var result any
		if request.Params.Name == "manage.list_file" {
			require.Empty(t, args["parent_id"], "must never enumerate unrelated descendants")
			result = map[string]any{"files": []any{map[string]any{"file_id": "project", "name": "Project", "is_folder": true}, map[string]any{"file_id": "forbidden", "name": "Forbidden", "is_folder": true}}, "finish": true}
		} else {
			require.Equal(t, "manage.query_file_info", request.Params.Name)
			require.Equal(t, true, args["need_entry"])
			id := args["file_id"].(string)
			parent, ok := parents[id]
			require.True(t, ok, "only selected chains may be queried: %s", id)
			mu.Lock()
			calls[id]++
			mu.Unlock()
			result = map[string]any{"file": map[string]any{"entry_parent_id": parent}}
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"structuredContent": result}}))
	}))
	defer server.Close()
	connector := &Connector{newClient: func(_, _ string) (*mcpClient, error) {
		return &mcpClient{http: server.Client(), endpoint: server.URL}, nil
	}}
	config := &types.DataSourceConfig{Settings: map[string]any{"site": siteSaaS}, Credentials: map[string]any{"token": "fixture"}}
	ancestors, err := connector.ResolveResourceAncestors(context.Background(), config, []string{"chosen-folder", "file:chosen-file"})
	require.NoError(t, err)
	require.Equal(t, []string{"deep", "phase", "project", "root"}, ancestors)
	require.Equal(t, map[string]int{"chosen-folder": 1, "chosen-file": 1, "deep": 1, "phase": 1}, calls, "shared parent chains queried once")
}
