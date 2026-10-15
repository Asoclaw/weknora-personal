package mockplus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// Opt-in read-only integration: secrets arrive through process environment.
func TestMarkdownReadOnlyLive(t *testing.T) {
	if os.Getenv("MOCKPLUS_MD_VERIFY") != "1" {
		t.Skip("explicit live verification required")
	}
	var config types.DataSourceConfig
	if err := json.Unmarshal([]byte(os.Getenv("MOCKPLUS_MD_CONFIG")), &config); err != nil {
		t.Fatal("invalid verification config")
	}
	cl, err := NewConnector().client(&config)
	if err != nil {
		t.Fatal(err)
	}
	var targets []struct{ Project, Board string }
	if json.Unmarshal([]byte(os.Getenv("MOCKPLUS_MD_TARGETS")), &targets) != nil {
		t.Fatal("invalid targets")
	}
	output := os.Getenv("MOCKPLUS_MD_OUTPUT")
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	results := []map[string]any{}
	states := map[string]boardState{}
	for _, target := range targets {
		var detail struct{ Children []node }
		if err := cl.api(context.Background(), "/api/v1/app/"+target.Project, &detail); err != nil {
			t.Fatal(err)
		}
		boards, err := projectBoards(detail.Children)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, board := range boards {
			if board.id() != target.Board {
				continue
			}
			found = true
			data, blocks, err := cl.markdown(context.Background(), board.DataURL)
			result := map[string]any{"board_id": target.Board, "project_id": target.Project, "text_blocks": blocks, "bytes": len(data)}
			if err != nil {
				result["error"] = err.Error()
			} else {
				again, _, repeatErr := cl.markdown(context.Background(), board.DataURL)
				if repeatErr != nil || !bytes.Equal(data, again) {
					t.Fatal("unchanged live design must produce stable Markdown bytes")
				}
				if err := os.WriteFile(filepath.Join(output, target.Board+".md"), data, 0600); err != nil {
					t.Fatal(err)
				}
				states["mockplus:"+cl.team+":"+target.Project+":"+target.Board] = boardState{AppID: target.Project, BoardID: target.Board, Title: board.Name, Hash: fmt.Sprintf("%x", sha256.Sum256(data)), ImageURL: board.ImageURL, DataURL: board.DataURL, ContentType: markdownContentType}
			}
			results = append(results, result)
		}
		if !found {
			t.Fatal("target board not found")
		}
	}
	data, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(filepath.Join(output, "result.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(states)
	if err := os.WriteFile(filepath.Join(output, "cursor-states.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
