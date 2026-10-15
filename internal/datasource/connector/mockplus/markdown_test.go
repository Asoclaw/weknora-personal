package mockplus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func testDesign(text string) string {
	data, _ := json.Marshal(map[string]any{"layers": map[string]any{
		"basic": map[string]any{"type": "group", "name": "Do not import layer name"},
		"children": []any{map[string]any{
			"basic": map[string]any{"type": "text"},
			"text":  map[string]any{"styles": []any{map[string]any{"value": text}}},
		}},
	}})
	return string(data)
}

func TestDesignMarkdownUsesActualTextAndReadingOrder(t *testing.T) {
	body := `{"layers":{"basic":{"type":"group","name":"忽略分组名称"},"children":[
	 {"basic":{"type":"text","name":"忽略图层名称"},"bounds":{"top":20,"left":10},"text":{"styles":[{"value":"相同"}]}},
	 {"basic":{"type":"text"},"bounds":{"top":10,"left":50},"text":{"styles":[{"value":"右侧"}]}},
	 {"basic":{"type":"text"},"bounds":{"top":10,"left":0},"text":{"styles":[{"value":"左"},{"value":"侧\r\n第二行"}]}},
	 {"basic":{"type":"text","opacity":0},"text":{"styles":[{"value":"不可见"}]}},
	 {"basic":{"type":"shape"},"text":{"styles":[{"value":"非文字图层"}]}},
	 {"basic":{"type":"text"},"bounds":{"top":30},"text":{"styles":[{"value":"相同"}]}},
	 {"basic":{"type":"text"},"bounds":{"top":40},"text":{"styles":[{"value":"<script>*文字*"}]}}
	]}}`
	md, count, err := designMarkdown([]byte(body))
	if err != nil || count != 5 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	want := "左侧\n第二行\n\n右侧\n\n相同\n\n相同\n\n\\<script\\>\\*文字\\*\n"
	if string(md) != want {
		t.Fatalf("got %q want %q", md, want)
	}
	for _, empty := range []string{`{}`, `{"layers":null}`, `{"layers":[]}`, `{"layers":{"basic":{"type":"text","name":"Only a name"}}}`, `{"layers":{"basic":{"type":"text"},"text":{"styles":[{"value":"  "}]}}}`} {
		if _, _, err := designMarkdown([]byte(empty)); err == nil {
			t.Fatalf("empty data must fail: %s", empty)
		}
	}
}

func TestMarkdownFallbackRepeatRenameAndContentChanges(t *testing.T) {
	f := setup(t)
	f.boards = []node{
		{ID: "plain", Name: "有图片", ImageURL: "/one.png", DataURL: "/ignored.json"},
		{ID: "group", IsGroup: true, Children: []node{{ID: "text", Name: "文字画板", DataURL: "/text.json"}}},
		{ID: "broken-image", Name: "图片不可用", ImageURL: "/missing.png", DataURL: "/text.json"},
	}
	f.designs["/text.json"] = testDesign("审核按钮和用户管理")
	ctx := context.Background()
	h := &recordingHandler{}
	cur, err := f.connector.FetchStream(ctx, f.config, nil, h)
	if err != nil || len(h.items) != 3 {
		t.Fatalf("items=%d err=%v", len(h.items), err)
	}
	if h.items[0].ContentType != "image/png" || f.requests["/ignored.json"] != 0 {
		t.Fatal("available image must keep existing PNG path")
	}
	for _, item := range h.items[1:] {
		if item.ContentType != markdownContentType || !strings.HasSuffix(item.FileName, ".md") || item.Metadata["mockplus_md_fallback"] != "true" || item.Metadata["error"] != "" || string(item.Content) != "审核按钮和用户管理\n" || item.SourceResourceID != "app" {
			t.Fatalf("invalid fallback: %+v", item)
		}
		if item.URL != f.server.URL+"/text.json" || item.Metadata["folder_path"] != "财务 / 费用" {
			t.Fatal("fallback needs verified source link and existing project path")
		}
	}
	for _, run := range []string{"repeat", "rename", "late-image", "changed"} {
		f.mu.Lock()
		switch run {
		case "rename":
			f.boards[1].Children[0].Name = "改名后的画板"
		case "late-image":
			f.boards[1].Children[0].ImageURL = "/two.png"
		case "changed":
			f.designs["/text.json"] = testDesign("新的用户管理正文")
		}
		f.mu.Unlock()
		h = &recordingHandler{}
		cur, err = f.connector.FetchStream(ctx, f.config, cur, h)
		if err != nil {
			t.Fatal(err)
		}
		if run == "repeat" || run == "late-image" {
			for _, item := range h.items {
				if len(item.Content) != 0 {
					t.Fatalf("%s reingested unchanged data", run)
				}
			}
		}
		if run == "rename" && string(h.items[1].Content) != "审核按钮和用户管理\n" {
			t.Fatal("renames must not change generated body")
		}
		if run == "changed" && string(h.items[1].Content) != "新的用户管理正文\n" {
			t.Fatal("real text changes must synchronize")
		}
		if boardContentType(loadState(cur, cur.LastSchemaHash)["mockplus:team:app:text"]) != markdownContentType {
			t.Fatal("existing MD must not turn into PNG because a URL appeared")
		}
	}
}

func TestMarkdownFallbackNeverReplacesStoredImagesOrAcknowledgesFailure(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	cur, err := f.connector.FetchStream(ctx, f.config, nil, &recordingHandler{})
	if err != nil {
		t.Fatal(err)
	}
	f.boards[0].ImageURL, f.boards[0].DataURL = "/missing.png", "/text.json"
	f.designs["/text.json"] = testDesign("不能覆盖已有图片")
	h := &recordingHandler{retryID: "mockplus:team:app:board1"}
	next, err := f.connector.FetchStream(ctx, f.config, cur, h)
	if err != nil || h.items[0].Metadata["error"] == "" || f.requests["/text.json"] != 0 {
		t.Fatal("existing image must fail safely rather than fall back to MD")
	}
	if loadState(next, next.LastSchemaHash)["mockplus:team:app:board1"].Hash == "" {
		t.Fatal("fetch failure must retain existing image baseline")
	}
	f.boards = []node{{ID: "text", DataURL: "/text.json"}}
	h = &recordingHandler{failID: "mockplus:team:app:text"}
	cur, err = f.connector.FetchStream(ctx, f.config, nil, h)
	if err != nil || len(loadState(cur, cur.LastSchemaHash)) != 0 {
		t.Fatal("failed MD ingest must not checkpoint")
	}
	h = &recordingHandler{}
	cur, err = f.connector.FetchStream(ctx, f.config, cur, h)
	if err != nil || len(h.items[0].Content) == 0 {
		t.Fatal("failed MD ingest must retry")
	}
	h = &recordingHandler{retryID: "mockplus:team:app:text"}
	_, err = f.connector.FetchStream(ctx, f.config, cur, h)
	if err != nil || h.items[0].ContentType != markdownContentType || len(h.items[0].Content) == 0 {
		t.Fatal("failed MD parse must retry as MD")
	}
	for _, data := range []string{`{}`, `not JSON`, `{"layers":{"basic":{"type":"text","name":"No actual text"}}}`} {
		f.designs["/text.json"] = data
		h = &recordingHandler{}
		_, err = f.connector.FetchStream(ctx, f.config, nil, h)
		if err != nil || h.items[0].Metadata["error"] == "" {
			t.Fatal("invalid or textless design must remain a failure")
		}
	}
}
