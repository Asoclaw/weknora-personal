package mockplus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const markdownContentType = "text/markdown"
const maxDesignBytes = 32 << 20

type designLayer struct {
	Bounds struct {
		Left float64 `json:"left"`
		Top  float64 `json:"top"`
	} `json:"bounds"`
	Basic struct {
		Type    string   `json:"type"`
		Opacity *float64 `json:"opacity"`
	} `json:"basic"`
	Text struct {
		Styles []struct {
			Value string `json:"value"`
		} `json:"styles"`
	} `json:"text"`
	Children []designLayer `json:"children"`
}

// Use actual text runs, never layer names or design annotations. A Markdown
// body contains no generated title/styles so metadata-only changes stay stable.
func designMarkdown(data []byte) ([]byte, int, error) {
	var document struct {
		Layers json.RawMessage `json:"layers"`
	}
	if json.Unmarshal(data, &document) != nil || len(document.Layers) == 0 {
		return nil, 0, errors.New("摹客设计数据缺少有效图层")
	}
	var layers []designLayer
	if strings.HasPrefix(strings.TrimSpace(string(document.Layers)), "[") {
		if json.Unmarshal(document.Layers, &layers) != nil {
			return nil, 0, errors.New("摹客设计图层格式不受支持")
		}
	} else {
		var root designLayer
		if json.Unmarshal(document.Layers, &root) != nil {
			return nil, 0, errors.New("摹客设计图层格式不受支持")
		}
		layers = []designLayer{root}
	}
	type block struct {
		text      string
		top, left float64
	}
	blocks := []block{}
	visited := 0
	var walk func([]designLayer, int) error
	walk = func(nodes []designLayer, depth int) error {
		if depth > 100 {
			return errors.New("摹客设计图层超过读取限制")
		}
		for _, layer := range nodes {
			visited++
			if visited > 50000 {
				return errors.New("摹客设计图层超过读取限制")
			}
			if layer.Basic.Opacity != nil && *layer.Basic.Opacity == 0 {
				continue
			}
			if layer.Basic.Type == "text" {
				var text strings.Builder
				for _, run := range layer.Text.Styles {
					text.WriteString(run.Value)
				}
				value := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text.String(), "\r\n", "\n"), "\r", "\n"))
				if value != "" {
					blocks = append(blocks, block{value, layer.Bounds.Top, layer.Bounds.Left})
				}
			}
			if err := walk(layer.Children, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(layers, 0); err != nil {
		return nil, 0, err
	}
	if len(blocks) == 0 {
		return nil, 0, errors.New("摹客设计数据没有可同步的文字")
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].top != blocks[j].top {
			return blocks[i].top < blocks[j].top
		}
		return blocks[i].left < blocks[j].left
	})
	escape := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "\\<", ">", "\\>", "#", "\\#")
	paragraphs := make([]string, len(blocks))
	for i, block := range blocks {
		paragraphs[i] = escape.Replace(block.text)
	}
	return []byte(strings.Join(paragraphs, "\n\n") + "\n"), len(blocks), nil
}

func (c *client) markdown(ctx context.Context, path string) ([]byte, int, error) {
	if path == "" {
		return nil, 0, errors.New("摹客画板缺少设计数据地址")
	}
	for attempt := 0; attempt < 2; attempt++ {
		resp, err := c.request(ctx, http.MethodGet, path, nil, attempt > 0)
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			resp.Body.Close()
			if attempt == 0 {
				continue
			}
			return nil, 0, errors.New("摹客设计数据读取无权限")
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, 0, fmt.Errorf("摹客设计数据读取失败（HTTP %d）", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxDesignBytes+1))
		resp.Body.Close()
		if err != nil || len(data) > maxDesignBytes {
			return nil, 0, errors.New("摹客设计数据读取失败或超过 32 MiB")
		}
		var auth struct {
			Code int `json:"code"`
		}
		if json.Unmarshal(data, &auth) == nil && auth.Code == 401 && attempt == 0 {
			continue
		}
		return designMarkdown(data)
	}
	return nil, 0, errors.New("摹客设计数据读取失败")
}

func boardContentType(state boardState) string {
	if state.ContentType == markdownContentType {
		return markdownContentType
	}
	return "image/png" // Cursors predating Markdown contain image boards only.
}
