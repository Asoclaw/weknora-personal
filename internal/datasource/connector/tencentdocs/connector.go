package tencentdocs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connector reads selected Tencent Docs folders through the provider's MCP API.
// The published WeKnora data-source scheduler, ingestion, logs and deletion
// switch remain responsible for the rest of the sync lifecycle.
type Connector struct {
	newClient func(site, token string) (*mcpClient, error)
}

func NewConnector() *Connector    { return &Connector{newClient: newMCPClient} }
func (c *Connector) Type() string { return types.ConnectorTypeTencentDocs }

func (c *Connector) client(config *types.DataSourceConfig) (*mcpClient, string, error) {
	if config == nil {
		return nil, "", errors.New("Tencent Docs configuration is required")
	}
	site := ""
	if config.Settings != nil {
		site, _ = config.Settings["site"].(string)
	}
	if site == "" && config.Credentials != nil {
		site, _ = config.Credentials["site"].(string)
	}
	token, _ := config.Credentials["token"].(string)
	client, err := c.newClient(site, token)
	return client, site, err
}

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	client, site, err := c.client(config)
	if err != nil {
		return err
	}
	_, err = listFolder(ctx, client, site, rootResourceID)
	return err
}

const rootResourceID = "root"

type remoteFile struct {
	ID         string
	Name       string
	URL        string
	Ext        string
	ModifiedAt int64
	IsFolder   bool
}

func listFolder(ctx context.Context, client *mcpClient, site, folderID string) ([]remoteFile, error) {
	if folderID == rootResourceID {
		folderID = ""
	}
	files := []remoteFile{}
	const pageSize = 20
	for offset := 0; offset < 20000; offset += pageSize {
		if site == siteSaaS {
			args := map[string]any{"list_type": 1, "offset": offset, "count": pageSize}
			if folderID != "" {
				args["parent_id"] = folderID
			}
			var response struct {
				Files []struct {
					ID         string `json:"file_id"`
					Name       string `json:"name"`
					URL        string `json:"doc_url"`
					Ext        string `json:"ext"`
					ModifiedAt int64  `json:"modify_time"`
					IsFolder   bool   `json:"is_folder"`
				} `json:"files"`
				Finish bool `json:"finish"`
			}
			if err := client.call(ctx, "manage.list_file", args, &response); err != nil {
				return nil, err
			}
			for _, file := range response.Files {
				files = append(files, remoteFile{ID: file.ID, Name: file.Name, URL: file.URL,
					Ext: file.Ext, ModifiedAt: file.ModifiedAt, IsFolder: file.IsFolder})
			}
			if response.Finish || len(response.Files) < pageSize {
				return files, nil
			}
		} else {
			args := map[string]any{"start": offset}
			if folderID != "" {
				args["folder_id"] = folderID
			}
			var response struct {
				List []struct {
					ID       string `json:"id"`
					Title    string `json:"title"`
					URL      string `json:"url"`
					IsFolder bool   `json:"is_folder"`
				} `json:"list"`
				Finish bool `json:"finish"`
			}
			if err := client.call(ctx, "manage.folder_list", args, &response); err != nil {
				return nil, err
			}
			for _, file := range response.List {
				files = append(files, remoteFile{ID: file.ID, Name: file.Title, URL: file.URL,
					IsFolder: file.IsFolder})
			}
			if response.Finish || len(response.List) < pageSize {
				return files, nil
			}
		}
	}
	return nil, errors.New("Tencent Docs folder listing exceeded the page limit")
}

func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	client, site, err := c.client(config)
	if err != nil {
		return nil, err
	}
	if parentID == "" {
		return []types.Resource{{ExternalID: rootResourceID, Name: "我的文档", Type: "folder", HasChildren: true}}, nil
	}
	files, err := listFolder(ctx, client, site, parentID)
	if err != nil {
		return nil, err
	}
	out := make([]types.Resource, 0)
	for _, file := range files {
		if !file.IsFolder {
			continue
		}
		out = append(out, types.Resource{
			ExternalID: file.ID, Name: file.Name, Type: "folder",
			ParentID: parentID, HasChildren: true,
		})
	}
	return out, nil
}

func (c *Connector) ResolveResourceAncestors(
	ctx context.Context, config *types.DataSourceConfig, resourceIDs []string,
) ([]string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil
	}
	client, site, err := c.client(config)
	if err != nil {
		return nil, err
	}
	targets := make(map[string]bool, len(resourceIDs))
	for _, id := range resourceIDs {
		targets[id] = true
	}
	ancestors := map[string]bool{}
	var walk func(id string, lineage []string, depth int) error
	walk = func(id string, lineage []string, depth int) error {
		if depth > 32 {
			return errors.New("Tencent Docs folder tree is too deep")
		}
		files, err := listFolder(ctx, client, site, id)
		if err != nil {
			return err
		}
		for _, file := range files {
			if !file.IsFolder {
				continue
			}
			if targets[file.ID] {
				for _, ancestor := range append(lineage, id) {
					ancestors[ancestor] = true
				}
			}
			if err := walk(file.ID, append(lineage, id), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(rootResourceID, nil, 0); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ancestors))
	for id := range ancestors {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

type cursorEntry struct {
	ModifiedAt int64  `json:"modified_at"`
	Path       string `json:"path"`
	Title      string `json:"title"`
	Hash       string `json:"hash,omitempty"`
}

func readCursor(cursor *types.SyncCursor) map[string]cursorEntry {
	out := map[string]cursorEntry{}
	if cursor == nil || cursor.ConnectorCursor == nil {
		return out
	}
	raw, err := json.Marshal(cursor.ConnectorCursor["items"])
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	items, _, err := c.fetch(ctx, config, resourceIDs, nil)
	return items, err
}

func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	if config == nil {
		return nil, nil, errors.New("Tencent Docs configuration is required")
	}
	items, next, err := c.fetch(ctx, config, config.ResourceIDs, cursor)
	return items, next, err
}

func (c *Connector) fetch(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	client, site, err := c.client(config)
	if err != nil {
		return nil, nil, err
	}
	if len(resourceIDs) == 0 {
		return nil, nil, errors.New("select at least one Tencent Docs folder")
	}
	previous := readCursor(cursor)
	nextState := map[string]cursorEntry{}
	items := []types.FetchedItem{}
	warnings := []string{}
	seen := map[string]bool{}
	roots := append([]string(nil), resourceIDs...)
	sort.Strings(roots)
	var walk func(folderID, folderPath string, depth int) error
	walk = func(folderID, folderPath string, depth int) error {
		if depth > 32 {
			return errors.New("Tencent Docs folder tree is too deep")
		}
		files, err := listFolder(ctx, client, site, folderID)
		if err != nil {
			return err
		} // Never infer deletion from an incomplete listing.
		for _, file := range files {
			if file.ID == "" || seen[file.ID] {
				continue
			}
			seen[file.ID] = true
			name := safeSegment(file.Name)
			if file.IsFolder {
				if err := walk(file.ID, path.Join(folderPath, name), depth+1); err != nil {
					return err
				}
				continue
			}
			key := site + ":" + file.ID
			if site == sitePersonal {
				info, err := getFileInfo(ctx, client, file.ID)
				if err != nil {
					items = append(items, failedItem(file, "Could not read file metadata"))
					if old, ok := previous[key]; ok {
						nextState[key] = old
					}
					continue
				}
				file.ModifiedAt = info.ModifiedAt
				if file.URL == "" {
					file.URL = info.URL
				}
				if file.Ext == "" {
					file.Ext = info.Ext
				}
			}
			location := path.Join(folderPath, name)
			signature := cursorEntry{ModifiedAt: file.ModifiedAt, Path: location, Title: file.Name}
			if old, ok := previous[key]; ok && file.ModifiedAt > 0 && old.ModifiedAt == signature.ModifiedAt && old.Path == signature.Path && old.Title == signature.Title {
				nextState[key] = old
				continue
			}
			item := fetchContent(ctx, client, site, file, folderPath)
			if item.Metadata["error_reason_code"] == "tencent_docs_unsupported_format" {
				delete(item.Metadata, "error")
				if old, ok := previous[key]; ok && old.Hash != "" {
					nextState[key] = old
				} else {
					nextState[key] = signature
				}
				if len(warnings) < 100 {
					warnings = append(warnings, fmt.Sprintf("%s: no indexable content; upload manually if needed", safeSegment(file.Name)))
				}
			}
			if len(item.Content) > 0 {
				signature.Hash = fmt.Sprintf("%x", sha256.Sum256(item.Content))
				if old, ok := previous[key]; ok && old.Hash == signature.Hash && old.Path == signature.Path && old.Title == signature.Title {
					nextState[key] = old
					continue
				}
			}
			items = append(items, item)
			if item.Metadata["error"] == "" && item.Metadata["error_reason_code"] != "tencent_docs_unsupported_format" {
				nextState[key] = signature
			}
			if item.Metadata["error"] != "" && item.Metadata["error_reason_code"] != "tencent_docs_unsupported_format" {
				if old, ok := previous[key]; ok {
					nextState[key] = old
				}
			}
		}
		return nil
	}
	for _, root := range roots {
		folderPath := ""
		if root != rootResourceID {
			info, err := getFileInfo(ctx, client, root)
			if err != nil {
				return nil, nil, err
			}
			folderPath = safeSegment(info.Title)
		}
		if err := walk(root, folderPath, 0); err != nil {
			return nil, nil, err
		}
	}
	for key, old := range previous {
		if seen[strings.TrimPrefix(key, site+":")] || !strings.HasPrefix(key, site+":") {
			continue
		}
		items = append(items, types.FetchedItem{
			ExternalID: key, Title: old.Title, IsDeleted: true,
		})
	}
	next := &types.SyncCursor{
		LastSyncTime:    time.Now().UTC(),
		ConnectorCursor: map[string]interface{}{"items": nextState},
	}
	if len(warnings) > 0 {
		return items, next, &datasource.PartialFetchError{Details: warnings}
	}
	return items, next, nil
}

type remoteInfo struct {
	Title      string
	URL        string
	Ext        string
	ModifiedAt int64
}

func getFileInfo(ctx context.Context, client *mcpClient, id string) (remoteInfo, error) {
	type fileInfo struct {
		Title      string `json:"title"`
		Name       string `json:"name"`
		URL        string `json:"url"`
		DocURL     string `json:"doc_url"`
		Type       string `json:"type"`
		Ext        string `json:"ext"`
		ModifiedAt int64  `json:"last_modify_time"`
		ModifyTime int64  `json:"modify_time"`
	}
	var result map[string]json.RawMessage
	if err := client.call(ctx, "manage.query_file_info", map[string]any{"file_id": id}, &result); err != nil {
		return remoteInfo{}, err
	}
	data := result["file"]
	if len(data) == 0 {
		data, _ = json.Marshal(result)
	}
	var info fileInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return remoteInfo{}, err
	}
	title := info.Title
	if title == "" {
		title = info.Name
	}
	url := info.URL
	if url == "" {
		url = info.DocURL
	}
	ext := info.Ext
	if ext == "" {
		ext = info.Type
	}
	modifiedAt := info.ModifiedAt
	if modifiedAt == 0 {
		modifiedAt = info.ModifyTime
	}
	return remoteInfo{Title: title, URL: url, Ext: ext, ModifiedAt: modifiedAt}, nil
}

func fetchContent(ctx context.Context, client *mcpClient, site string, file remoteFile, folderPath string) types.FetchedItem {
	key := site + ":" + file.ID
	result := types.FetchedItem{
		ExternalID: key, Title: file.Name, SourceResourceID: file.ID,
		UpdatedAt: time.Unix(file.ModifiedAt, 0).UTC(),
		Metadata: map[string]string{
			"source_url": file.URL, "source_type": file.Ext, "source_site": site,
			"channel": "tencent_docs",
		},
	}
	tool := "manage.get_content"
	if site == sitePersonal {
		tool = "get_content"
	}
	var response struct {
		Content string `json:"content"`
	}
	if err := client.call(ctx, tool, map[string]any{"file_id": file.ID}, &response); err != nil {
		result.Metadata["error"] = "Tencent Docs content could not be read"
		result.Metadata["error_reason_code"] = "tencent_docs_content_unavailable"
		result.Metadata["error_reason"] = "This file could not be read through the authorized Tencent Docs API; upload it manually if needed."
		return result
	}
	content := strings.TrimSpace(response.Content)
	if content == "" || content == "{}" || content == "[]" {
		result.Metadata["error"] = "Tencent Docs returned no indexable content"
		result.Metadata["error_reason_code"] = "tencent_docs_unsupported_format"
		result.Metadata["error_reason"] = "This file type has no indexable content from the authorized Tencent Docs API; upload it manually if needed."
		return result
	}
	base := safeSegment(file.Name)
	if base == "" {
		base = "document-" + strconv.FormatInt(file.ModifiedAt, 10)
	}
	result.FileName = path.Join(folderPath, base+".md")
	result.ContentType = "text/markdown"
	result.Content = []byte("# " + file.Name + "\n\n" + content + "\n")
	return result
}

func failedItem(file remoteFile, reason string) types.FetchedItem {
	return types.FetchedItem{
		Title: file.Name, Metadata: map[string]string{
			"error": reason, "error_reason_code": "tencent_docs_metadata_unavailable",
			"error_reason": reason,
		},
	}
}

func safeSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r':
			return '_'
		}
		return r
	}, value)
	if value == "." || value == ".." || value == "" {
		return "untitled"
	}
	return value
}
