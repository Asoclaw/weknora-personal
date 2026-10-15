package tencentdocs

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type nativeCollector struct{ items []types.FetchedItem }

func (h *nativeCollector) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}
func (*nativeCollector) Checkpoint(context.Context, *types.SyncCursor) error { return nil }

func (c *Connector) FetchStream(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler) (*types.SyncCursor, error) {
	_, site, err := c.client(config)
	if err != nil {
		return nil, err
	}
	if site != siteSaaS {
		items, next, err := c.fetch(ctx, config, config.ResourceIDs, cursor)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if err := h.Emit(ctx, item); err != nil {
				return nil, err
			}
		}
		return next, h.Checkpoint(ctx, next)
	}
	return c.fetchNativeStream(ctx, config, config.ResourceIDs, cursor, h, false)
}

func (c *Connector) FetchFullStream(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler) (*types.SyncCursor, error) {
	_, site, err := c.client(config)
	if err != nil {
		return nil, err
	}
	if site != siteSaaS {
		return c.FetchStream(ctx, config, nil, h)
	}
	return c.fetchNativeStream(ctx, config, config.ResourceIDs, cursor, h, true)
}

func (c *Connector) fetchNativeBatch(ctx context.Context, config *types.DataSourceConfig, ids []string, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	h := &nativeCollector{}
	next, err := c.fetchNativeStream(ctx, config, ids, cursor, h, cursor == nil)
	return h.items, next, err
}

func (c *Connector) fetchNativeStream(ctx context.Context, config *types.DataSourceConfig, ids []string, cursor *types.SyncCursor, h datasource.StreamHandler, full bool) (*types.SyncCursor, error) {
	client, _, err := c.client(config)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("请选择至少一个腾讯文档文件夹或文件")
	}
	previous := readCursor(cursor)
	state := map[string]cursorEntry{}
	for key, value := range previous {
		state[key] = value
	} // Complete resumable checkpoints, including not-yet-visited entries.
	makeCursor := func() *types.SyncCursor {
		return &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: map[string]interface{}{"items": state, "format_version": nativeFormatVersion}}
	}
	emit := func(item types.FetchedItem) (bool, error) {
		if ack, ok := h.(datasource.AcknowledgingStreamHandler); ok {
			return ack.EmitAcknowledged(ctx, item)
		}
		err := h.Emit(ctx, item)
		return err == nil && item.Metadata["error"] == "", err
	}
	seen := map[string]bool{}
	folders := map[string]bool{}
	process := func(file remoteFile, folderPath string) error {
		if file.ID == "" || seen[file.ID] {
			return nil
		}
		seen[file.ID] = true
		if err := ctx.Err(); err != nil {
			return err
		}
		key := siteSaaS + ":" + file.ID
		old, exists := previous[key]
		file.URL = validSourceURL(file.URL)
		if file.Ext == "" || file.URL == "" {
			info, err := getFileInfo(ctx, client, file.ID)
			if err != nil && file.Ext == "" {
				_, e := emit(nativeFailure(file, "tencent_docs_metadata_unavailable", "无法读取文件信息"))
				return e
			}
			if err == nil {
				if file.Ext == "" {
					file.Ext = info.Ext
				}
				if file.URL == "" {
					file.URL = validSourceURL(info.URL)
				}
			}
		}
		if file.URL == "" {
			file.URL = validSourceURL(old.SourceURL)
		}
		signature := cursorEntry{SourceURL: file.URL, ModifiedAt: file.ModifiedAt, Path: path.Join(folderPath, safeSegment(file.Name)), Title: file.Name, FormatVersion: nativeFormatVersion}
		retry := false
		if aware, ok := h.(datasource.RetryAwareStreamHandler); ok && exists && old.SkipReason == "" {
			retry, err = aware.ShouldRetryItem(ctx, key)
			if err != nil {
				return err
			}
		}
		if !full && !retry && exists && old.FormatVersion == nativeFormatVersion && file.ModifiedAt > 0 && old.ModifiedAt == signature.ModifiedAt && old.SourceURL == signature.SourceURL && old.Path == signature.Path && old.Title == signature.Title {
			return nil
		}
		item := fetchNativeFile(ctx, client, file, folderPath)
		signature.SkipReason = item.Metadata["skip_reason_code"]
		if len(item.Content) > 0 {
			signature.Hash = fmt.Sprintf("%x", sha256.Sum256(item.Content))
			if !full && !retry && old.FormatVersion == nativeFormatVersion && old.Hash == signature.Hash && old.Title == signature.Title && old.Path == signature.Path && old.SourceURL == signature.SourceURL {
				state[key] = signature
				return h.Checkpoint(ctx, makeCursor())
			}
		}
		accepted, err := emit(item)
		if err != nil {
			return err
		}
		if accepted {
			state[key] = signature
		} // Failed ingestion retains old version and is retried.
		return h.Checkpoint(ctx, makeCursor())
	}
	var walk func(string, string, int) error
	walk = func(id, folderPath string, depth int) error {
		if depth > 32 {
			return fmt.Errorf("腾讯文档目录层级过深")
		}
		if folders[id] {
			return nil
		}
		folders[id] = true
		files, err := listFolder(ctx, client, siteSaaS, id)
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.IsFolder {
				if err := walk(file.ID, path.Join(folderPath, safeSegment(file.Name)), depth+1); err != nil {
					return err
				}
			} else if err := process(file, folderPath); err != nil {
				return err
			}
		}
		return nil
	}
	roots := append([]string(nil), ids...)
	sort.Strings(roots)
	for _, id := range roots {
		if strings.HasPrefix(id, fileResourcePrefix) {
			fileID := strings.TrimPrefix(id, fileResourcePrefix)
			info, err := getFileInfo(ctx, client, fileID)
			if err != nil {
				return nil, err
			}
			if err := process(remoteFile{ID: fileID, Name: info.Title, URL: info.URL, Ext: info.Ext, ModifiedAt: info.ModifiedAt}, ""); err != nil {
				return nil, err
			}
			continue
		}
		folderPath := ""
		if id != rootResourceID {
			info, err := getFileInfo(ctx, client, id)
			if err != nil {
				return nil, err
			}
			folderPath = safeSegment(info.Title)
		}
		if err := walk(id, folderPath, 0); err != nil {
			return nil, err
		}
	}
	// Reconcile removals only after every selected folder has been fully listed.
	for key, old := range previous {
		if !strings.HasPrefix(key, siteSaaS+":") || seen[strings.TrimPrefix(key, siteSaaS+":")] {
			continue
		}
		accepted, err := emit(types.FetchedItem{ExternalID: key, Title: old.Title, IsDeleted: true})
		if err != nil {
			return nil, err
		}
		if accepted {
			delete(state, key)
		}
	}
	next := makeCursor()
	return next, h.Checkpoint(ctx, next)
}
