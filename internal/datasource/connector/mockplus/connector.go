// Package mockplus reads current PNG boards from the private Mockplus
// collaboration API, with a text-only Markdown fallback for unavailable images.
package mockplus

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type Connector struct {
	mu         sync.Mutex
	clients    map[string]*client
	retryDelay time.Duration
}

func NewConnector() *Connector {
	return &Connector{clients: make(map[string]*client), retryDelay: time.Second}
}
func (c *Connector) Type() string { return types.ConnectorTypeMockplus }

type node struct {
	ID          string `json:"_id"`
	AlternateID string `json:"id"`
	Name        string `json:"name"`
	IsAppSet    bool   `json:"isAppSet"`
	IsGroup     bool   `json:"isGroup"`
	Children    []node `json:"children"`
	ImageURL    string `json:"imageURL"`
	DataURL     string `json:"dataURL"`
	URL         string `json:"url"`
	IsArchived  bool   `json:"isArchived"`
	Archived    bool   `json:"archived"`
	IsDeleted   bool   `json:"isDeleted"`
	Deleted     bool   `json:"deleted"`
}

func (n node) id() string {
	if n.ID != "" {
		return n.ID
	}
	return n.AlternateID
}
func (n node) inactive() bool { return n.IsArchived || n.Archived || n.IsDeleted || n.Deleted }

type treeNode struct {
	node
	Path     string
	Parent   string
	Inactive bool
}
type boardState struct {
	AppID        string `json:"app_id"`
	BoardID      string `json:"board_id"`
	Title        string `json:"title"`
	Path         string `json:"path"`
	Hash         string `json:"hash"`
	ImageURL     string `json:"image_url"`
	ContentType  string `json:"content_type,omitempty"`
	DataURL      string `json:"data_url,omitempty"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	cl, err := c.clientWithTeam(config, false)
	if err != nil {
		return err
	}
	var info map[string]json.RawMessage
	return cl.api(ctx, "/api/v1/user/info", &info)
}

// The server ignores appSetID and sometimes returns a shallow tree after
// login. Always filter locally and retry for a stable richest snapshot.
func (c *Connector) tree(ctx context.Context, cl *client) (map[string]treeNode, error) {
	var best map[string]treeNode
	lastFingerprint := ""
	stable := 0
	for attempt := 0; attempt < 6; attempt++ {
		var response struct {
			Apps *[]node `json:"apps"`
		}
		if err := cl.api(ctx, "/api/v1/app/allAppAndAppSet/"+url.PathEscape(cl.team)+"?source=all&needAppSet=true&needArchivedApp=true", &response); err != nil {
			return nil, err
		}
		if response.Apps == nil {
			return nil, errors.New("摹客项目树响应缺少 apps，未同步")
		}
		current := map[string]treeNode{}
		var walk func(node, string, string, bool, int) error
		walk = func(n node, parent, path string, inactive bool, depth int) error {
			if depth > 100 || len(current) > 50000 {
				return errors.New("摹客项目树超过遍历限制")
			}
			if n.id() == "" {
				return errors.New("摹客项目缺少稳定 ID")
			}
			if _, ok := current[n.id()]; ok {
				return errors.New("摹客项目树包含重复 ID")
			}
			name := n.Name
			if name == "" {
				name = n.id()
			}
			if path != "" {
				path += " / "
			}
			path += name
			inactive = inactive || n.inactive()
			current[n.id()] = treeNode{node: n, Path: path, Parent: parent, Inactive: inactive}
			for _, child := range n.Children {
				if err := walk(child, n.id(), path, inactive, depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		for _, root := range *response.Apps {
			if err := walk(root, "", "", false, 0); err != nil {
				return nil, err
			}
		}
		serialized, _ := json.Marshal(current)
		fingerprint := fmt.Sprintf("%x", sha256.Sum256(serialized))
		if fingerprint == lastFingerprint {
			stable++
		} else {
			stable = 0
		}
		lastFingerprint = fingerprint
		if len(current) >= len(best) {
			best = current
		}
		if stable >= 2 {
			break
		}
		if attempt < 5 && c.retryDelay > 0 {
			timer := time.NewTimer(c.retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return best, nil
}

func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	if parentID != "" {
		return []types.Resource{}, nil
	} // Return the full provider tree once.
	cl, err := c.client(config)
	if err != nil {
		return nil, err
	}
	tree, err := c.tree(ctx, cl)
	if err != nil {
		return nil, err
	}
	result := []types.Resource{}
	for id, n := range tree {
		if n.Inactive {
			continue
		}
		kind := "document"
		if n.IsAppSet {
			kind = "folder"
		}
		result = append(result, types.Resource{ExternalID: id, Name: n.Name, Type: kind, ParentID: n.Parent, HasChildren: n.IsAppSet, Metadata: map[string]interface{}{"path": n.Path}})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Metadata["path"].(string) < result[j].Metadata["path"].(string) })
	return result, nil
}

func (c *Connector) ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error) {
	return []string{}, nil
}

type collector struct{ items []types.FetchedItem }

func (h *collector) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}
func (*collector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }
func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, ids []string) ([]types.FetchedItem, error) {
	copyConfig := *config
	copyConfig.ResourceIDs = ids
	h := &collector{}
	_, err := c.fetch(ctx, &copyConfig, nil, h, true)
	return h.items, err
}
func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	h := &collector{}
	next, err := c.fetch(ctx, config, cursor, h, false)
	return h.items, next, err
}
func (c *Connector) FetchStream(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler) (*types.SyncCursor, error) {
	return c.fetch(ctx, config, cursor, h, false)
}
func (c *Connector) FetchFullStream(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler) (*types.SyncCursor, error) {
	return c.fetch(ctx, config, cursor, h, true)
}

func loadState(cursor *types.SyncCursor, scope string) map[string]boardState {
	state := map[string]boardState{}
	if cursor == nil || cursor.LastSchemaHash != scope {
		return state
	}
	data, _ := json.Marshal(cursor.ConnectorCursor["boards"])
	_ = json.Unmarshal(data, &state)
	if state == nil {
		state = map[string]boardState{}
	}
	return state
}
func checkpoint(state map[string]boardState, scope string) *types.SyncCursor {
	return &types.SyncCursor{LastSyncTime: time.Now().UTC(), LastSchemaHash: scope, ConnectorCursor: map[string]interface{}{"boards": state}}
}

// A successful Emit is not necessarily a successful ingest in WeKnora's legacy
// streaming handler. Use explicit acknowledgments so failed items retry.
func emit(ctx context.Context, h datasource.StreamHandler, item types.FetchedItem) (bool, error) {
	if ack, ok := h.(datasource.AcknowledgingStreamHandler); ok {
		return ack.EmitAcknowledged(ctx, item)
	}
	err := h.Emit(ctx, item)
	return err == nil, err
}
func failure(id, title, reason string) types.FetchedItem {
	metadata := map[string]string{"channel": "mockplus", "error": reason, "retry_kind": "project", "project_id": id, "retry_phase": "fetch"}
	if parts := strings.Split(id, ":"); len(parts) == 4 && parts[0] == "mockplus" {
		metadata["retry_kind"], metadata["project_id"], metadata["board_id"] = "board", parts[2], parts[3]
	}
	return types.FetchedItem{ExternalID: id, Title: title, Metadata: metadata}
}

// Project children can be groups instead of boards. Groups are containers,
// including empty ones; only their leaf boards enter the existing sync flow.
// Keep board IDs and project paths independent of group membership.
func projectBoards(children []node) ([]node, error) {
	boards := []node{}
	visited := 0
	var walk func([]node, bool, int) error
	walk = func(nodes []node, inactive bool, depth int) error {
		if depth > 100 {
			return errors.New("摹客画板分组超过遍历限制，保留原资料")
		}
		for _, n := range nodes {
			visited++
			if visited > 50000 {
				return errors.New("摹客画板分组超过遍历限制，保留原资料")
			}
			if n.IsGroup {
				if err := walk(n.Children, inactive || n.inactive(), depth+1); err != nil {
					return err
				}
				continue
			}
			n.IsArchived = n.IsArchived || inactive
			boards = append(boards, n)
		}
		return nil
	}
	if err := walk(children, false, 0); err != nil {
		return nil, err
	}
	return boards, nil
}

func (c *Connector) fetch(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler, full bool) (*types.SyncCursor, error) {
	if config == nil || len(config.ResourceIDs) == 0 {
		return nil, errors.New("请选择至少一个摹客项目集或项目")
	}
	if !config.MultimodalEnabled {
		return nil, errors.New("请在知识库多模态配置中启用图片解析并选择模型")
	}
	cl, err := c.client(config)
	if err != nil {
		return nil, err
	}
	tree, err := c.tree(ctx, cl)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, id := range config.ResourceIDs {
		if _, ok := tree[id]; !ok {
			return nil, errors.New("所选摹客项目未出现在项目树中，请刷新范围后重试；未扩大范围或删除资料")
		}
		selected[id] = true
	}
	scope := fmt.Sprintf("%x", sha256.Sum256([]byte(cl.base.String()+"\x00"+cl.team)))
	state := loadState(cursor, scope)
	projects := []treeNode{}
	for id, n := range tree {
		if n.IsAppSet {
			continue
		}
		covered := false
		for current := id; current != ""; current = tree[current].Parent {
			if selected[current] {
				covered = true
				break
			}
		}
		if covered {
			projects = append(projects, n)
		}
	}
	var retryGroups map[string][]types.SyncRetryTarget
	if config.RetryOnly {
		projects, retryGroups, err = retryProjects(ctx, config, projects, state, h)
		if err != nil {
			return nil, err
		}
	}
	projectFailure := func(project treeNode, reason string) error {
		if config.RetryOnly {
			for _, target := range retryGroups[project.id()] {
				if _, e := emit(ctx, h, retryFailure(target, reason)); e != nil {
					return e
				}
			}
			return nil
		}
		_, e := emit(ctx, h, failure(project.id(), project.Path, reason))
		return e
	}
	// Archived folders may have no children in the provider snapshot. Only an
	// explicit inactive flag, never absence from a tree, authorizes tombstones.
	inactiveApps := map[string]bool{}
	for _, n := range projects {
		if n.Inactive && !config.RetryOnly {
			inactiveApps[n.id()] = true
		}
	}
	for _, old := range state {
		if config.RetryOnly {
			break
		}
		for id := range selected {
			if n := tree[id]; n.Inactive && (old.Path == n.Path || strings.HasPrefix(old.Path, n.Path+" / ")) {
				inactiveApps[old.AppID] = true
			}
		}
	}
	for id, old := range state {
		if inactiveApps[old.AppID] {
			_, err := emit(ctx, h, types.FetchedItem{ExternalID: id, Title: old.Title, IsDeleted: true})
			if err != nil {
				return nil, err
			}
			// Retain the tombstone baseline so disabled/failed deletion retries.
			if err := h.Checkpoint(ctx, checkpoint(state, scope)); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Path < projects[j].Path })
	for _, project := range projects {
		if project.Inactive {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var detail struct {
			Children   *[]node `json:"children"`
			IsArchived bool    `json:"isArchived"`
			Archived   bool    `json:"archived"`
			IsDeleted  bool    `json:"isDeleted"`
		}
		if err := cl.api(ctx, "/api/v1/app/"+url.PathEscape(project.id()), &detail); err != nil {
			if e := projectFailure(project, err.Error()); e != nil {
				return nil, e
			}
			continue
		}
		if detail.IsArchived || detail.Archived || detail.IsDeleted {
			if config.RetryOnly {
				if e := projectFailure(project, "失败项目已停用，保留已有文件"); e != nil {
					return nil, e
				}
				continue
			}
			for id, old := range state {
				if old.AppID == project.id() {
					if _, e := emit(ctx, h, types.FetchedItem{ExternalID: id, Title: old.Title, IsDeleted: true}); e != nil {
						return nil, e
					}
				}
			}
			continue
		}
		if detail.Children == nil {
			if e := projectFailure(project, "摹客项目响应缺少画板列表，保留原资料"); e != nil {
				return nil, e
			}
			continue
		}
		boards, err := projectBoards(*detail.Children)
		if err != nil {
			if e := projectFailure(project, err.Error()); e != nil {
				return nil, e
			}
			continue
		}
		if config.RetryOnly {
			boards, err = retryBoards(ctx, cl, project, boards, retryGroups[project.id()], h)
			if err != nil {
				return nil, err
			}
		}
		seen := map[string]bool{}
		for start := 0; start < len(boards); start += 4 {
			end := start + 4
			if end > len(boards) {
				end = len(boards)
			}
			type fetched struct {
				board       node
				id          string
				state       boardState
				data        []byte
				unchanged   bool
				textBlocks  int
				retainImage bool
				forceRetry  bool
				err         error
			}
			batch := make([]fetched, end-start)
			var workers sync.WaitGroup
			for offset, board := range boards[start:end] {
				if board.id() == "" || seen[board.id()] {
					batch[offset] = fetched{board: board, err: errors.New("摹客画板缺少稳定 ID 或 ID 重复")}
					continue
				}
				seen[board.id()] = true
				id := "mockplus:" + cl.team + ":" + project.id() + ":" + board.id()
				old := state[id]
				priorContentType := boardContentType(old)
				retainImage := old.Hash != "" && boardContentType(old) == "image/png"
				forceRetry := false
				if retry, ok := h.(datasource.RetryAwareStreamHandler); ok && old.Hash != "" {
					needed, err := retry.ShouldRetryItem(ctx, id)
					if err != nil {
						workers.Wait()
						return nil, err
					}
					if needed {
						old = boardState{ContentType: priorContentType}
						forceRetry = true
					}
				}
				if full || old.ImageURL != board.ImageURL {
					old.ETag = ""
					old.LastModified = ""
				}
				batch[offset] = fetched{board: board, id: id, retainImage: retainImage, forceRetry: forceRetry, state: boardState{AppID: project.id(), BoardID: board.id(), Title: board.Name, Path: project.Path, ImageURL: board.ImageURL, ContentType: boardContentType(old)}}
				if board.inactive() {
					continue
				}
				workers.Add(1)
				go func(index int, old boardState) {
					defer workers.Done()
					r := &batch[index]
					var data []byte
					var etag, modified string
					var unchanged bool
					var err error
					if boardContentType(old) == markdownContentType {
						// Keep an existing Markdown representation even if an image
						// URL later appears, avoiding format-only content replacement.
						data, r.textBlocks, err = cl.markdown(ctx, r.board.DataURL)
					} else {
						if r.board.ImageURL == "" {
							err = errors.New("摹客画板缺少图片地址")
						} else {
							data, etag, modified, unchanged, err = cl.image(ctx, r.board.ImageURL, old)
							if err != nil { // One bounded retry for transient/partial downloads.
								data, etag, modified, unchanged, err = cl.image(ctx, r.board.ImageURL, old)
							}
						}
						if err != nil && !r.retainImage && ctx.Err() == nil {
							markdown, blocks, fallbackErr := cl.markdown(ctx, r.board.DataURL)
							if fallbackErr == nil {
								data, r.textBlocks, err = markdown, blocks, nil
								r.state.ContentType = markdownContentType
								etag, modified, unchanged = "", "", false
							} else if r.board.DataURL != "" {
								err = fmt.Errorf("%v；%v", err, fallbackErr)
							}
						}
					}
					r.data = data
					r.unchanged = unchanged
					r.err = err
					r.state.ETag = etag
					r.state.LastModified = modified
					if r.state.ContentType == markdownContentType {
						r.state.DataURL = r.board.DataURL
					}
					if unchanged {
						r.state.Hash = old.Hash
					} else if err == nil {
						r.state.Hash = fmt.Sprintf("%x", sha256.Sum256(data))
					}
				}(offset, old)
			}
			workers.Wait()
			for _, r := range batch {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if r.err != nil {
					if _, e := emit(ctx, h, failure(r.id, project.Path+" / "+r.board.Name, r.err.Error())); e != nil {
						return nil, e
					}
					continue
				}
				if r.board.inactive() {
					if config.RetryOnly {
						continue
					}
					if old, ok := state[r.id]; ok {
						if _, e := emit(ctx, h, types.FetchedItem{ExternalID: r.id, Title: old.Title, IsDeleted: true}); e != nil {
							return nil, e
						}
					}
					continue
				}
				old, exists := state[r.id]
				if !full && !r.forceRetry && exists && old.Hash == r.state.Hash && boardContentType(old) == r.state.ContentType && old.Title == r.state.Title && old.Path == r.state.Path && old.DataURL == r.state.DataURL {
					if _, e := emit(ctx, h, types.FetchedItem{ExternalID: r.id, Title: r.board.Name}); e != nil {
						return nil, e
					}
					state[r.id] = r.state
				} else {
					// A 304 after a rename/move still needs bytes for re-ingestion.
					if r.unchanged {
						r.data, _, _, _, err = cl.image(ctx, r.board.ImageURL, boardState{})
						if err != nil {
							if _, e := emit(ctx, h, failure(r.id, r.board.Name, err.Error())); e != nil {
								return nil, e
							}
							continue
						}
					}
					// Preserve a provider-supplied board/project URL. If absent, the
					// downloaded image URL is the only verified source link; do not
					// invent a private deployment's browser route.
					contentURL := r.board.ImageURL
					extension := ".png"
					if r.state.ContentType == markdownContentType {
						contentURL, extension = r.board.DataURL, ".md"
					}
					imageURL, _ := cl.base.Parse(contentURL)
					source := imageURL.String()
					for _, raw := range []string{project.URL, r.board.URL} {
						if raw != "" {
							if u, e := cl.base.Parse(raw); e == nil && sameOrigin(u, cl.base) && u.User == nil {
								source = u.String()
							}
						}
					}
					item := types.FetchedItem{ExternalID: r.id, Title: r.board.Name, Content: r.data, ContentType: r.state.ContentType, FileName: safePath(project.Path) + "/" + safeName(r.board.Name) + extension, URL: source, SourceResourceID: project.id(), Metadata: map[string]string{"channel": "mockplus", "project_id": project.id(), "board_id": r.board.id(), "folder_path": project.Path, "board_title": r.board.Name, "source_url": source}}
					if r.state.ContentType == markdownContentType {
						item.Metadata["mockplus_md_fallback"] = "true"
						item.Metadata["mockplus_text_blocks"] = fmt.Sprint(r.textBlocks)
					}
					ok, e := emit(ctx, h, item)
					if e != nil {
						return nil, e
					}
					if ok {
						state[r.id] = r.state
					}
				}
				if err := h.Checkpoint(ctx, checkpoint(state, scope)); err != nil {
					return nil, err
				}
			}
		}
	}
	return checkpoint(state, scope), nil
}

func safeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune("\\/:*?\"<>|", r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		name = "画板"
	}
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}
	return name
}

func safePath(path string) string {
	parts := strings.Split(path, " / ")
	for i := range parts {
		parts[i] = safeName(parts[i])
	}
	return strings.Join(parts, "/")
}

var _ datasource.FullStreamingConnector = (*Connector)(nil)
