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
		if serveNativeDownload(w, r, false) {
			return
		}
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
		if serveNativeExport(w, r, request.Params.Name) {
			return
		}
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
		return &mcpClient{http: server.Client(), downloadHTTP: server.Client(), token: token, endpoint: server.URL}, nil
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
	if first[0].FileName != "F1/需求.docx" || first[1].FileName != "F2/规则.docx" {
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
			moved = item.FileName == "F2/需求.docx" && !item.IsDeleted
		case "saas:d2":
			deleted = item.IsDeleted
		case "saas:d3":
			created = item.FileName == "F1/纪要.docx" && !item.IsDeleted
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

func TestFileSelectionListingAncestorsAndIncrementalSync(t *testing.T) {
	for _, site := range []string{siteSaaS, sitePersonal} {
		t.Run(site, func(t *testing.T) {
			listingCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if serveNativeDownload(w, r, false) {
					return
				}
				var request struct {
					Params struct {
						Name      string         `json:"name"`
						Arguments map[string]any `json:"arguments"`
					} `json:"params"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				var result any
				args := request.Params.Arguments
				if serveNativeExport(w, r, request.Params.Name) {
					return
				}
				switch request.Params.Name {
				case "manage.list_file", "manage.folder_list":
					listingCalls++
					folder, _ := args["parent_id"].(string)
					if site == sitePersonal {
						folder, _ = args["folder_id"].(string)
					}
					files := []map[string]any{}
					if folder == "" && args["list_type"] != float64(5) {
						files = append(files, map[string]any{"file_id": "f1", "id": "f1", "name": "Folder", "title": "Folder", "is_folder": true})
					} else if folder == "f1" {
						files = append(files, map[string]any{"file_id": "d1", "id": "d1", "name": "Document", "title": "Document", "modify_time": 1})
					}
					result = map[string]any{"files": files, "list": files, "finish": true}
				case "manage.query_file_info":
					id, _ := args["file_id"].(string)
					result = map[string]any{"file": map[string]any{"name": map[string]string{"d1": "Document", "f1": "Folder"}[id], "modify_time": 1, "entry_parent_id": map[string]string{"d1": "f1", "f1": ""}[id]}}
				case "manage.get_content", "get_content":
					result = map[string]any{"content": "Selected document content"}
				default:
					t.Errorf("unexpected tool %s", request.Params.Name)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"structuredContent": result}})
			}))
			defer server.Close()
			connector := &Connector{newClient: func(site, token string) (*mcpClient, error) {
				return &mcpClient{http: server.Client(), downloadHTTP: server.Client(), token: token, endpoint: server.URL}, nil
			}}
			config := &types.DataSourceConfig{Credentials: map[string]any{"token": "test"}, Settings: map[string]any{"site": site}, ResourceIDs: []string{"file:d1"}}
			resources, err := connector.ListResources(context.Background(), config, "f1")
			if err != nil || len(resources) != 1 || resources[0].ExternalID != "file:d1" || resources[0].HasChildren || resources[0].ParentID != "f1" || resources[0].Type != "document" {
				t.Fatalf("file must be selectable leaf: %+v, %v", resources, err)
			}
			ancestors, err := connector.ResolveResourceAncestors(context.Background(), config, config.ResourceIDs)
			if err != nil || strings.Join(ancestors, ",") != "f1,root" {
				t.Fatalf("file ancestors: %v, %v", ancestors, err)
			}
			listingCalls = 0
			items, cursor, err := connector.FetchIncremental(context.Background(), config, nil)
			if err != nil || len(items) != 1 || items[0].ExternalID != site+":d1" || listingCalls != 0 {
				t.Fatalf("single file sync: %+v, %v, listing calls %d", items, err, listingCalls)
			}
			items, _, err = connector.FetchIncremental(context.Background(), config, cursor)
			if err != nil || len(items) != 0 {
				t.Fatalf("unchanged file sync: %+v, %v", items, err)
			}
			items, err = connector.FetchAll(context.Background(), config, []string{"f1", "file:d1"})
			if err != nil || len(items) != 1 {
				t.Fatalf("overlapping selections must deduplicate: %+v, %v", items, err)
			}
		})
	}
}

func TestSaaSSharedProjectFolderAppearsAtRoot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		listType := request.Params.Arguments["list_type"]
		parent, _ := request.Params.Arguments["parent_id"].(string)
		var files []map[string]any
		switch {
		case parent == "fms":
			if listType != float64(1) {
				t.Errorf("shared folder children must use My Documents listing")
			}
			files = []map[string]any{{"file_id": "phase-1", "name": "项目启动", "is_folder": true}}
		case listType == float64(5):
			files = []map[string]any{{"file_id": "fms", "name": "格力FMS项目", "is_folder": true}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"structuredContent": map[string]any{"files": files, "finish": true}}})
	}))
	defer server.Close()
	connector := &Connector{newClient: func(site, token string) (*mcpClient, error) {
		return &mcpClient{http: server.Client(), token: token, endpoint: server.URL}, nil
	}}
	config := &types.DataSourceConfig{Credentials: map[string]any{"token": "test"}, Settings: map[string]any{"site": siteSaaS}}
	root, err := connector.ListResources(context.Background(), config, rootResourceID)
	if err != nil || len(root) != 1 || root[0].Name != "格力FMS项目" {
		t.Fatalf("shared project folder missing from root: resources=%+v err=%v", root, err)
	}
	children, err := connector.ListResources(context.Background(), config, "fms")
	if err != nil || len(children) != 1 || children[0].Name != "项目启动" {
		t.Fatalf("shared folder children missing: resources=%+v err=%v", children, err)
	}
}

func TestUnsupportedDocumentIsReportedOncePerVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveNativeDownload(w, r, true) {
			return
		}
		var request struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if serveNativeExport(w, r, request.Params.Name) {
			return
		}
		var result any
		switch request.Params.Name {
		case "manage.list_file":
			result = map[string]any{"files": []map[string]any{{"file_id": "empty", "name": "空文档", "modify_time": 1}}, "finish": true}
		case "manage.get_content":
			result = map[string]any{"content": "{}"}
		case "manage.query_file_info":
			result = map[string]any{"file": map[string]any{"name": "空文档"}}
		default:
			t.Errorf("unexpected tool %q", request.Params.Name)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"result": map[string]any{"structuredContent": result}})
	}))
	defer server.Close()
	connector := &Connector{newClient: func(site, token string) (*mcpClient, error) {
		return &mcpClient{http: server.Client(), downloadHTTP: server.Client(), token: token, endpoint: server.URL}, nil
	}}
	config := &types.DataSourceConfig{Credentials: map[string]interface{}{"token": "test"},
		Settings: map[string]interface{}{"site": siteSaaS}, ResourceIDs: []string{rootResourceID}}
	first, cursor, err := connector.FetchIncremental(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("empty document should be a normal skip, got %v", err)
	}
	if len(first) != 1 || first[0].Metadata["skip_reason_code"] != "tencent_docs_empty_document" {
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
