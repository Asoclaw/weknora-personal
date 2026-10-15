package tencentdocs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeExportUnavailableFallsBackOnlyToValidOnlineContent(t *testing.T) {
	for _, tc := range []struct {
		ext, body string
		success   bool
	}{
		{"flowchart", "节点一\n节点二", true}, {"tencentdoc", "正文", true},
		{"flowchart", "  ", false}, {"flowchart", "{}", false}, {"pptx", "附件预览", false},
	} {
		t.Run(tc.ext+tc.body, func(t *testing.T) {
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Params struct {
						Name string `json:"name"`
					} `json:"params"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				result := map[string]any{"error_code": 10090, "error_msg": "drivecgi Service Error"}
				if req.Params.Name == "manage.get_content" {
					reads++
					result = map[string]any{"error_code": 0, "content": tc.body}
				}
				json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"structuredContent": result}})
			}))
			defer server.Close()
			file := remoteFile{ID: "one", Name: "流程", Ext: tc.ext, URL: "https://saas.docs.qq.com/flowchart/original"}
			item := fetchNativeFile(context.Background(), &mcpClient{http: server.Client(), endpoint: server.URL}, file, "目录")
			if tc.success {
				if item.FileName != "目录/流程.md" || item.Metadata["error"] != "" || item.Metadata["source_url"] != file.URL || item.Metadata["tencent_docs_md_fallback"] != "true" {
					t.Fatalf("fallback not successful: %+v", item)
				}
			} else if item.Metadata["error"] == "" {
				t.Fatal("missing content must not become success")
			}
			if tc.ext == "pptx" && reads != 0 {
				t.Fatal("uploaded attachment must not be substituted")
			}
		})
	}
}
