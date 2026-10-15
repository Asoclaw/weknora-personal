package mockplus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type fixture struct {
	mu                      sync.Mutex
	server                  *httptest.Server
	connector               *Connector
	config                  *types.DataSourceConfig
	roots                   []node
	boards                  []node
	images                  map[string][]byte
	designs                 map[string]string
	requests                map[string]int
	version                 string
	loginCode               int
	logins, trees           int
	expire, shallow, broken bool
}

func pngData(t *testing.T, v byte) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: v, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{version: "one", images: map[string][]byte{"/one.png": pngData(t, 10), "/two.png": pngData(t, 10)}, designs: map[string]string{}, requests: map[string]int{}}
	f.boards = []node{{ID: "board1", Name: "审批", ImageURL: "/one.png"}, {ID: "board2", Name: "审批", ImageURL: "/two.png"}}
	f.roots = []node{{ID: "set", Name: "财务", IsAppSet: true, Children: []node{{ID: "app", Name: "费用"}}}, {ID: "other", Name: "其他"}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests[r.URL.Path]++
		jsonReply := func(value any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(value)
		}
		if r.URL.Path == "/api/v1/user/signin" {
			f.logins++
			http.SetCookie(w, &http.Cookie{Name: "ds.sid", Value: "session", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "ds.sid.sig", Value: "signature", Path: "/"})
			jsonReply(map[string]any{"code": f.loginCode})
			return
		}
		if _, err := r.Cookie("ds.sid"); err != nil {
			jsonReply(map[string]any{"code": 401})
			return
		}
		if f.expire {
			f.expire = false
			jsonReply(map[string]any{"code": 401})
			return
		}
		switch r.URL.Path {
		case "/api/v1/user/info":
			jsonReply(map[string]any{"code": 0, "payload": map[string]any{"user": map[string]string{"name": "tester"}}})
		case "/api/v1/app/allAppAndAppSet/team":
			f.trees++
			if r.URL.Query().Get("needAppSet") != "true" || r.URL.Query().Get("source") != "all" {
				t.Error("wrong tree query")
			}
			roots := f.roots
			if f.shallow && f.trees == 1 {
				roots = []node{{ID: "set", Name: "财务", IsAppSet: true}}
			}
			jsonReply(map[string]any{"code": 0, "payload": map[string]any{"apps": roots}})
		case "/api/v1/app/app":
			if f.broken {
				w.WriteHeader(500)
				return
			}
			jsonReply(map[string]any{"code": 0, "data": map[string]any{"children": f.boards}})
		default:
			if data, ok := f.designs[r.URL.Path]; ok {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(data))
			} else if data, ok := f.images[r.URL.Path]; ok {
				etag := `"` + f.version + `"`
				if r.Header.Get("If-None-Match") == etag {
					w.WriteHeader(http.StatusNotModified)
					return
				}
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("ETag", etag)
				_, _ = w.Write(data)
			} else {
				w.WriteHeader(404)
			}
		}
	}))
	t.Cleanup(f.server.Close)
	t.Setenv("MOCKPLUS_BASE_URL", f.server.URL)
	t.Setenv("MOCKPLUS_USER", "test")
	t.Setenv("MOCKPLUS_PASS", "secret-test")
	f.connector = NewConnector()
	f.connector.retryDelay = 0
	f.config = &types.DataSourceConfig{Type: "mockplus", Settings: map[string]interface{}{"base_url": f.server.URL, "team_id": "team"}, ResourceIDs: []string{"set", "app"}, MultimodalEnabled: true}
	return f
}

type recordingHandler struct {
	collector
	checkpoint *types.SyncCursor
	failID     string
	retryID    string
}

func (h *recordingHandler) EmitAcknowledged(ctx context.Context, item types.FetchedItem) (bool, error) {
	_ = h.Emit(ctx, item)
	return item.ExternalID != h.failID, nil
}
func (h *recordingHandler) ShouldRetryItem(_ context.Context, id string) (bool, error) {
	return id == h.retryID, nil
}
func (h *recordingHandler) Checkpoint(_ context.Context, c *types.SyncCursor) error {
	data, _ := json.Marshal(c)
	h.checkpoint = &types.SyncCursor{}
	return json.Unmarshal(data, h.checkpoint)
}

func TestCurrentBoardsIncrementalAndRename(t *testing.T) {
	f := setup(t)
	h := &recordingHandler{}
	cur, err := f.connector.FetchStream(context.Background(), f.config, nil, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 2 || h.items[0].ExternalID == h.items[1].ExternalID {
		t.Fatalf("same-name boards must remain distinct: %+v", h.items)
	}
	if h.items[0].FileName != "财务/费用/审批.png" || h.items[0].Metadata["folder_path"] != "财务 / 费用" {
		t.Fatal("missing project path")
	}
	if h.items[0].URL != f.server.URL+"/one.png" {
		t.Fatal("must retain a verified source URL")
	}
	h = &recordingHandler{}
	cur, err = f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range h.items {
		if len(item.Content) > 0 || item.IsDeleted {
			t.Fatal("unchanged board reingested")
		}
	}
	f.mu.Lock()
	f.boards[0].Name = "审批详情"
	f.mu.Unlock()
	h = &recordingHandler{}
	cur, err = f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items[0].Content) == 0 || h.items[0].Title != "审批详情" {
		t.Fatal("rename needs bytes even on 304")
	}
	f.mu.Lock()
	f.images["/one.png"] = pngData(t, 20)
	f.version = "two"
	f.mu.Unlock()
	h = &recordingHandler{}
	_, err = f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items[0].Content) == 0 || len(h.items[1].Content) > 0 {
		t.Fatal("content hash change detection incorrect")
	}
}

func TestGroupedBoardsPreserveExistingCursorAndPaths(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	before := &recordingHandler{}
	cur, err := f.connector.FetchStream(ctx, f.config, nil, before)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	first, second := f.boards[0], f.boards[1]
	f.boards = []node{
		{ID: "group", Name: "分组", IsGroup: true, Children: []node{
			first,
			{ID: "nested", Name: "子分组", IsGroup: true, Children: []node{
				second, {ID: "new-board", Name: "新增画板", ImageURL: "/one.png"},
			}},
			{ID: "empty", Name: "空分组", IsGroup: true},
		}},
	}
	f.mu.Unlock()
	after := &recordingHandler{}
	cur, err = f.connector.FetchStream(ctx, f.config, cur, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.items) != 3 || len(loadState(cur, cur.LastSchemaHash)) != 3 {
		t.Fatalf("expected only three boards: %+v", after.items)
	}
	for i := 0; i < 2; i++ {
		if after.items[i].ExternalID != before.items[i].ExternalID || len(after.items[i].Content) != 0 || after.items[i].IsDeleted || after.items[i].Metadata["error"] != "" {
			t.Fatalf("group membership changed existing board: %+v", after.items[i])
		}
	}
	added := after.items[2]
	if added.ExternalID != "mockplus:team:app:new-board" || len(added.Content) == 0 || added.FileName != "财务/费用/新增画板.png" || added.Metadata["folder_path"] != "财务 / 费用" || added.SourceResourceID != "app" {
		t.Fatalf("new grouped board must use existing identity/path rules: %+v", added)
	}
	repeated := &recordingHandler{}
	_, err = f.connector.FetchStream(ctx, f.config, cur, repeated)
	if err != nil || len(repeated.items) != 3 {
		t.Fatalf("repeat: count=%d err=%v", len(repeated.items), err)
	}
	for _, item := range repeated.items {
		if len(item.Content) != 0 || item.IsDeleted || item.Metadata["error"] != "" {
			t.Fatalf("repeat reingested grouped board: %+v", item)
		}
	}
}

func TestGroupsDoNotHideRealMissingImages(t *testing.T) {
	f := setup(t)
	f.boards = []node{
		{ID: "plain", Name: "普通画板", ImageURL: "/one.png"},
		{ID: "empty", Name: "空分组", IsGroup: true},
		{ID: "group", Name: "分组", IsGroup: true, Children: []node{
			{ID: "nested", IsGroup: true, Children: []node{
				{ID: "missing", Name: "缺图画板"},
				{ID: "valid", Name: "组内画板", ImageURL: "/two.png"},
			}},
		}},
	}
	h := &recordingHandler{}
	cur, err := f.connector.FetchStream(context.Background(), f.config, nil, h)
	if err != nil || len(h.items) != 3 {
		t.Fatalf("count=%d err=%v", len(h.items), err)
	}
	if h.items[1].ExternalID != "mockplus:team:app:missing" || h.items[1].Metadata["error"] != "摹客画板缺少图片地址" {
		t.Fatalf("missing board must still fail: %+v", h.items[1])
	}
	if len(h.items[0].Content) == 0 || len(h.items[2].Content) == 0 || len(loadState(cur, cur.LastSchemaHash)) != 2 {
		t.Fatal("valid boards must ingest; failed boards must not checkpoint")
	}
	f.boards = []node{{ID: "empty", IsGroup: true}}
	h = &recordingHandler{}
	_, err = f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil || len(h.items) != 0 {
		t.Fatal("empty/missing groups must not cause failures or deletions")
	}
}

func TestInactiveGroupsOnlyDeleteKnownBoards(t *testing.T) {
	f := setup(t)
	f.boards = []node{{ID: "group", IsGroup: true, Children: f.boards}}
	ctx := context.Background()
	cur, err := f.connector.FetchStream(ctx, f.config, nil, &recordingHandler{})
	if err != nil {
		t.Fatal(err)
	}
	f.boards[0].IsDeleted = true
	f.boards[0].Children = append(f.boards[0].Children, node{ID: "never-synced"})
	h := &recordingHandler{}
	_, err = f.connector.FetchStream(ctx, f.config, cur, h)
	if err != nil || len(h.items) != 2 {
		t.Fatalf("count=%d err=%v", len(h.items), err)
	}
	for _, item := range h.items {
		if !item.IsDeleted || item.Metadata["error"] != "" {
			t.Fatalf("inactive group's known boards must use tombstones: %+v", item)
		}
	}
}

func TestProjectBoardGroupTraversalLimits(t *testing.T) {
	deep := node{ID: "leaf", ImageURL: "/one.png"}
	for i := 0; i < 102; i++ {
		deep = node{IsGroup: true, Children: []node{deep}}
	}
	if boards, err := projectBoards([]node{deep}); err == nil || boards != nil {
		t.Fatal("excessively deep response must not partially synchronize")
	}
	if boards, err := projectBoards(make([]node, 50001)); err == nil || boards != nil {
		t.Fatal("excessively large response must not partially synchronize")
	}
}

func TestLoginBusinessCodesAndExpiredSession(t *testing.T) {
	f := setup(t)
	f.loginCode = 1
	if err := f.connector.Validate(context.Background(), f.config); err == nil || strings.Contains(err.Error(), "secret-test") {
		t.Fatalf("invalid login: %v", err)
	}
	f.loginCode = 0
	if err := f.connector.Validate(context.Background(), f.config); err != nil {
		t.Fatal(err)
	}
	f.expire = true
	if err := f.connector.Validate(context.Background(), f.config); err != nil {
		t.Fatal(err)
	}
	if f.logins != 3 {
		t.Fatalf("expected one re-login, got %d", f.logins)
	}
}

func TestPageCredentialsUseCustomServerAndTeam(t *testing.T) {
	f := setup(t)
	t.Setenv("MOCKPLUS_BASE_URL", "https://legacy-server.example.test")
	f.config.Credentials = map[string]interface{}{"username": "page-account", "password": "page-password"}
	if err := f.connector.Validate(context.Background(), f.config); err != nil {
		t.Fatal(err)
	}
	cl, err := f.connector.client(f.config)
	if err != nil || cl.user != "page-account" || cl.password != "page-password" || cl.team != "team" {
		t.Fatal("page parameters were overridden by environment")
	}
	f.config.Credentials["password"] = ""
	if err := f.connector.Validate(context.Background(), f.config); err == nil {
		t.Fatal("partial page credentials must not use environment password")
	}
	f.config.Credentials["password"] = "page-password"
	delete(f.config.Settings, "team_id")
	if _, err := f.connector.client(f.config); err == nil {
		t.Fatal("missing team must not use the GREE team")
	}
}

func TestTreeRetryScopeAndArchive(t *testing.T) {
	f := setup(t)
	f.shallow = true
	resources, err := f.connector.ListResources(context.Background(), f.config, "")
	if err != nil || len(resources) != 3 {
		t.Fatalf("resources=%v err=%v", resources, err)
	}
	f.config.ResourceIDs = []string{"missing"}
	h := &recordingHandler{}
	if _, err := f.connector.FetchStream(context.Background(), f.config, nil, h); err == nil || len(h.items) != 0 {
		t.Fatal("missing scope must never widen")
	}
	f.config.ResourceIDs = []string{"set"}
	cur, err := f.connector.FetchStream(context.Background(), f.config, nil, h)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.boards = f.boards[:1]
	f.mu.Unlock() // Missing board is not evidence of deletion.
	h = &recordingHandler{}
	cur, err = f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range h.items {
		if item.IsDeleted {
			t.Fatal("incomplete list caused deletion")
		}
	}
	f.mu.Lock()
	f.roots[0].Archived = true
	f.mu.Unlock()
	h = &recordingHandler{}
	_, err = f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 2 || !h.items[0].IsDeleted || !h.items[1].IsDeleted {
		t.Fatal("explicit archive not reconciled")
	}
}

func TestFailuresResumeAndMultimodalGate(t *testing.T) {
	f := setup(t)
	f.config.MultimodalEnabled = false
	if _, err := f.connector.FetchStream(context.Background(), f.config, nil, &recordingHandler{}); err == nil {
		t.Fatal("must require multimodal")
	}
	f.config.MultimodalEnabled = true
	h := &recordingHandler{failID: "mockplus:team:app:board1"}
	cur, err := f.connector.FetchStream(context.Background(), f.config, nil, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadState(cur, cur.LastSchemaHash)) != 1 {
		t.Fatal("failed ingest checkpointed")
	}
	h = &recordingHandler{}
	cur, err = f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items[0].Content) == 0 {
		t.Fatal("failed board not retried")
	}
	h = &recordingHandler{retryID: "mockplus:team:app:board2"}
	cur, err = f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items[1].Content) == 0 {
		t.Fatal("asynchronous parse failure not retried")
	}
	f.mu.Lock()
	f.broken = true
	f.mu.Unlock()
	h = &recordingHandler{}
	next, err := f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.items) != 1 || h.items[0].Metadata["error"] == "" || len(loadState(next, next.LastSchemaHash)) != 2 {
		t.Fatal("fetch failure lost baseline")
	}
	f.mu.Lock()
	f.broken = false
	f.boards[0].ImageURL = "/missing.png"
	f.mu.Unlock()
	h = &recordingHandler{}
	_, err = f.connector.FetchStream(context.Background(), f.config, cur, h)
	if err != nil {
		t.Fatal(err)
	}
	if h.items[0].URL != "" || h.items[0].Metadata["error"] == "" {
		t.Fatal("failed image must not fall through URL parser")
	}
}

func TestExternalImagesDoNotReceiveCookiesAndHostBinding(t *testing.T) {
	f := setup(t)
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("cookie leaked")
		}
		_, _ = w.Write(pngData(t, 30))
	}))
	defer external.Close()
	f.boards[0].ImageURL = external.URL + "/image.png"
	if _, err := f.connector.FetchStream(context.Background(), f.config, nil, &recordingHandler{}); err != nil {
		t.Fatal(err)
	}
	f.config.Settings["base_url"] = external.URL
	if err := f.connector.Validate(context.Background(), f.config); err == nil {
		t.Fatal("environment password must not be sent to arbitrary host")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.config.Settings["base_url"] = f.server.URL
	if err := f.connector.Validate(ctx, f.config); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}
