package tencentdocs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSourceLinkSyncAndCursorCompatibility(t *testing.T) {
	for _, site := range []string{sitePersonal, siteSaaS} {
		for _, selection := range []string{"folder", "file:d1"} {
			t.Run(site+"/"+selection, func(t *testing.T) {
				directoryURL, infoURL := "https://docs.qq.com/doc/original", "https://docs.qq.com/doc/original"
				modifiedAt := int64(1)
				metadataFailure := false
				contentCalls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if serveNativeDownload(w, r, false) {
						return
					}
					var req struct {
						Params struct {
							Name      string         `json:"name"`
							Arguments map[string]any `json:"arguments"`
						} `json:"params"`
					}
					_ = json.NewDecoder(r.Body).Decode(&req)
					var result any
					if req.Params.Name == "manage.apply_download" {
						contentCalls++
					}
					if serveNativeExport(w, r, req.Params.Name) {
						return
					}
					switch req.Params.Name {
					case "manage.list_file", "manage.folder_list":
						file := map[string]any{"id": "d1", "file_id": "d1", "name": "Document", "title": "Document", "ext": "tencentdoc", "url": directoryURL, "doc_url": directoryURL, "modify_time": modifiedAt}
						result = map[string]any{"list": []any{file}, "files": []any{file}, "finish": true}
					case "manage.query_file_info":
						if metadataFailure && req.Params.Arguments["file_id"] == "d1" {
							w.WriteHeader(http.StatusServiceUnavailable)
							return
						}
						result = map[string]any{"file": map[string]any{"name": "Document", "url": infoURL, "modify_time": modifiedAt}}
					case "get_content", "manage.get_content":
						contentCalls++
						result = map[string]any{"content": "Unchanged body"}
					default:
						t.Errorf("unexpected tool: %s", req.Params.Name)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"structuredContent": result}})
				}))
				defer server.Close()
				connector := &Connector{newClient: func(site, token string) (*mcpClient, error) {
					return &mcpClient{http: server.Client(), downloadHTTP: server.Client(), token: token, endpoint: server.URL}, nil
				}}
				config := &types.DataSourceConfig{Credentials: map[string]any{"token": "test"}, Settings: map[string]any{"site": site}, ResourceIDs: []string{selection}}
				items, cursor, err := connector.FetchIncremental(context.Background(), config, nil)
				if err != nil || len(items) != 1 || items[0].Metadata["source_url"] != infoURL || items[0].URL != "" {
					t.Fatalf("original content ingestion: %+v %v", items, err)
				}
				items, next, err := connector.FetchIncremental(context.Background(), config, cursor)
				if err != nil || len(items) != 0 || contentCalls != 1 {
					t.Fatalf("unchanged: %+v %v", items, err)
				}
				// Simulate the old cursor: body and timestamp unchanged, URL absent.
				old := readCursor(next)
				entry := old[site+":d1"]
				entry.SourceURL = ""
				old[site+":d1"] = entry
				next.ConnectorCursor["items"] = old
				items, cursor, err = connector.FetchIncremental(context.Background(), config, next)
				if err != nil || len(items) != 1 || items[0].Metadata["source_url"] != infoURL {
					t.Fatalf("old cursor backfill: %+v %v", items, err)
				}
				directoryURL = "" // URL only available from file metadata.
				infoURL = "https://docs.qq.com/doc/changed"
				items, cursor, err = connector.FetchIncremental(context.Background(), config, cursor)
				if err != nil || len(items) != 1 || items[0].Metadata["source_url"] != infoURL {
					t.Fatalf("URL-only change with fallback: %+v %v", items, err)
				}
				// A changed body/version with no provider URL must preserve the known link.
				modifiedAt = 2
				infoURL = ""
				items, cursor, err = connector.FetchIncremental(context.Background(), config, cursor)
				if err != nil || len(items) != 0 {
					t.Fatalf("same body/hash and preserved link: %+v %v", items, err)
				}
				if readCursor(cursor)[site+":d1"].SourceURL != "https://docs.qq.com/doc/changed" {
					t.Fatal("known link lost")
				}
				// With no known link, absence does not invent a URL or prevent ingestion.
				items, _, err = connector.FetchIncremental(context.Background(), config, nil)
				if err != nil || len(items) != 1 || items[0].Metadata["source_url"] != "" {
					t.Fatalf("missing URL: %+v %v", items, err)
				}
				if site == siteSaaS && selection == "folder" {
					metadataFailure = true
					items, _, err = connector.FetchIncremental(context.Background(), config, cursor)
					if err != nil || len(items) != 0 {
						t.Fatalf("URL fallback failure should preserve cursor: %+v %v", items, err)
					}
				}
			})
		}
	}
}

func TestSourceURLRejectsUnsafeTargets(t *testing.T) {
	for _, value := range []string{"javascript:alert(1)", "/files/x", "//docs.qq.com/x", "https://user:secret@docs.qq.com/x", "https://", ""} {
		if validSourceURL(value) != "" {
			t.Fatalf("unsafe URL accepted: %q", value)
		}
	}
}
