package tencentdocs

import (
	"context"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/types"
	"net/http"
	"net/http/httptest"
	"testing"
)

type nativeAckRecorder struct {
	reject     bool
	items      []types.FetchedItem
	checkpoint *types.SyncCursor
}

func (h *nativeAckRecorder) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}
func (h *nativeAckRecorder) EmitAcknowledged(ctx context.Context, item types.FetchedItem) (bool, error) {
	h.Emit(ctx, item)
	return !h.reject, nil
}
func (h *nativeAckRecorder) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	raw, _ := json.Marshal(cursor)
	var copy types.SyncCursor
	json.Unmarshal(raw, &copy)
	h.checkpoint = &copy
	return nil
}

func TestNativeStreamingOnlyCheckpointsAcceptedFilesAndMigratesOldCursor(t *testing.T) {
	exports := 0
	brokenListing := false
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
		json.NewDecoder(r.Body).Decode(&req)
		if req.Params.Name == "manage.apply_download" {
			exports++
			if req.Params.Arguments["target_ext"] != "docx" {
				t.Error("smartcanvas must explicitly export docx")
			}
		}
		if serveNativeExport(w, r, req.Params.Name) {
			return
		}
		result := map[string]any{}
		switch req.Params.Name {
		case "manage.query_file_info":
			result = map[string]any{"file": map[string]any{"name": "Native", "ext": "smartcanvas", "modify_time": 1, "doc_url": "https://saas.docs.qq.com/aio/original"}}
		case "manage.list_file":
			if brokenListing {
				w.WriteHeader(503)
				return
			}
			result = map[string]any{"finish": true, "files": []any{map[string]any{"file_id": "d1", "name": "Native", "ext": "smartcanvas", "modify_time": 1, "doc_url": "https://saas.docs.qq.com/aio/original"}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"structuredContent": result}})
	}))
	defer server.Close()
	c := &Connector{newClient: func(string, string) (*mcpClient, error) {
		return &mcpClient{http: server.Client(), downloadHTTP: server.Client(), endpoint: server.URL}, nil
	}}
	config := &types.DataSourceConfig{Credentials: map[string]interface{}{"token": "test"}, Settings: map[string]interface{}{"site": siteSaaS}, ResourceIDs: []string{"file:d1"}}
	old := &types.SyncCursor{ConnectorCursor: map[string]interface{}{"items": map[string]cursorEntry{"saas:d1": {ModifiedAt: 1, SourceURL: "https://saas.docs.qq.com/aio/original", Title: "Native", Path: "Native", Hash: "old-md"}}}}
	recorder := &nativeAckRecorder{reject: true}
	cursor, err := c.FetchStream(context.Background(), config, old, recorder)
	if err != nil || readCursor(cursor)["saas:d1"].FormatVersion != 0 {
		t.Fatalf("rejected ingest advanced cursor: %v", err)
	}
	recorder.reject = false
	recorder.items = nil
	cursor, err = c.FetchStream(context.Background(), config, cursor, recorder)
	if err != nil || exports != 2 || readCursor(cursor)["saas:d1"].FormatVersion != nativeFormatVersion || len(recorder.items) != 1 || recorder.items[0].FileName != "Native.docx" {
		t.Fatalf("retry/migration failed: exports=%d items=%+v err=%v", exports, recorder.items, err)
	}
	recorder.items = nil
	cursor, err = c.FetchStream(context.Background(), config, cursor, recorder)
	if err != nil || exports != 2 || len(recorder.items) != 0 {
		t.Fatal("unchanged native document fetched again")
	}
	config.ResourceIDs = []string{rootResourceID}
	brokenListing = true
	recorder.items = nil
	_, err = c.FetchStream(context.Background(), config, cursor, recorder)
	if err == nil {
		t.Fatal("incomplete listing must fail")
	}
	for _, item := range recorder.items {
		if item.IsDeleted {
			t.Fatal("incomplete listing emitted deletion")
		}
	}
}

func TestNativeTypeValidationAndNormalSkips(t *testing.T) {
	ext, empty, _, err := nativePackageInfo(testDocx(true))
	if err != nil || ext != "docx" || !empty {
		t.Fatal("empty docx classification failed")
	}
	if _, _, _, err := nativePackageInfo([]byte("not an office document")); err != nil {
		t.Fatal(err)
	}
	item := fetchNativeFile(context.Background(), nil, remoteFile{ID: "zip", Name: "Archive", Ext: "zip"}, "")
	if item.Metadata["skip_reason_code"] != "tencent_docs_archive_unsupported" || item.Metadata["error"] != "" {
		t.Fatal("ZIP must skip before export")
	}
}
