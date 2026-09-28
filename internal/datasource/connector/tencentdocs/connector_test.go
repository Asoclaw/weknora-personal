package tencentdocs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSaaSFolderSyncDetectsMovedNewAndDeletedDocuments(t *testing.T) {
	phase := 1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var request struct {
			Params struct {
				Name      string                 `json:"name"`
				Arguments map[string]interface{} `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		var result any
		switch request.Params.Name {
		case "manage.query_file_info":
			id, _ := request.Params.Arguments["file_id"].(string)
			result = map[string]any{"file": map[string]any{"name": strings.ToUpper(id)}}
		case "manage.list_file":
			folder, _ := request.Params.Arguments["parent_id"].(string)
			files := []map[string]any{}
			switch folder {
			case "f1":
				if phase == 1 {
					files = append(files, map[string]any{"file_id": "d1", "name": "需求", "ext": "tencentdoc", "modify_time": 1})
				} else {
					files = append(files, map[string]any{"file_id": "d3", "name": "纪要", "ext": "tencentdoc", "modify_time": 2})
				}
			case "f2":
				if phase == 1 {
					files = append(files, map[string]any{"file_id": "d2", "name": "规则", "ext": "tencentdoc", "modify_time": 1})
				} else {
					files = append(files, map[string]any{"file_id": "d1", "name": "需求", "ext": "tencentdoc", "modify_time": 1})
				}
			}
			result = map[string]any{"files": files, "finish": true}
		case "manage.get_content":
			result = map[string]any{"content": "可检索正文"}
		default:
			t.Errorf("unexpected MCP tool: %s", request.Params.Name)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"structuredContent": result},
		})
	}))
	defer server.Close()
	connector := &Connector{newClient: func(site, token string) (*mcpClient, error) {
		return &mcpClient{http: server.Client(), token: token, endpoint: server.URL}, nil
	}}
	config := &types.DataSourceConfig{
		Credentials: map[string]interface{}{"token": "test-token"},
		Settings:    map[string]interface{}{"site": siteSaaS},
		ResourceIDs: []string{"f1", "f2"},
	}
	first, cursor, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("first sync: got %d items, want 2", len(first))
	}
	if first[0].FileName != "F1/需求.md" || first[1].FileName != "F2/规则.md" {
		t.Fatalf("folder mapping: got %q and %q", first[0].FileName, first[1].FileName)
	}
	phase = 2
	second, _, err := connector.FetchIncremental(context.Background(), config, cursor)
	if err != nil {
		t.Fatal(err)
	}
	var created, moved, deleted bool
	for _, item := range second {
		switch item.ExternalID {
		case "saas:d1":
			moved = item.FileName == "F2/需求.md" && !item.IsDeleted
		case "saas:d2":
			deleted = item.IsDeleted
		case "saas:d3":
			created = item.FileName == "F1/纪要.md" && !item.IsDeleted
		}
	}
	if !created || !moved || !deleted {
		t.Fatalf("second sync missing expected events: created=%v moved=%v deleted=%v", created, moved, deleted)
	}
}

func TestMCPFailureDoesNotExposeToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	client := &mcpClient{http: server.Client(), token: "private-token", endpoint: server.URL}
	var result map[string]any
	err := client.call(context.Background(), "manage.list_file", map[string]any{"list_type": 1}, &result)
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("expected redacted HTTP failure, got %v", err)
	}
}

func TestUnsupportedDocumentIsReportedOncePerVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		var result any
		switch request.Params.Name {
		case "manage.list_file":
			result = map[string]any{"files": []map[string]any{{"file_id": "empty", "name": "空文档", "modify_time": 1}}, "finish": true}
		case "manage.get_content":
			result = map[string]any{"content": "{}"}
		default:
			t.Errorf("unexpected tool %q", request.Params.Name)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"structuredContent": result}})
	}))
	defer server.Close()
	connector := &Connector{newClient: func(site, token string) (*mcpClient, error) {
		return &mcpClient{http: server.Client(), token: token, endpoint: server.URL}, nil
	}}
	config := &types.DataSourceConfig{Credentials: map[string]interface{}{"token": "test"},
		Settings: map[string]interface{}{"site": siteSaaS}, ResourceIDs: []string{rootResourceID}}
	first, cursor, err := connector.FetchIncremental(context.Background(), config, nil)
	if err == nil || !strings.Contains(err.Error(), "no indexable content") {
		t.Fatalf("expected visible unsupported-format warning, got %v", err)
	}
	if len(first) != 1 || first[0].Metadata["error_reason_code"] != "tencent_docs_unsupported_format" {
		t.Fatalf("unexpected first sync: %+v", first)
	}
	second, _, err := connector.FetchIncremental(context.Background(), config, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("unchanged unsupported file reported again: %+v", second)
	}
}
