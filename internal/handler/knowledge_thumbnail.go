package handler

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"sync"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	"golang.org/x/sync/singleflight"
)

const thumbnailEdge = 640
const thumbnailMaxBytes = 60 << 20
const thumbnailMaxPixels = 64 << 20

type thumbnailEntry struct {
	key  string
	data []byte
}

// Derived bytes only: bounded and independent of knowledge/chunk storage.
type knowledgeThumbnailCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lru     *list.List
	bytes   int
	limit   int
	work    singleflight.Group
	decode  chan struct{}
}

func newKnowledgeThumbnailCache(limit int) *knowledgeThumbnailCache {
	return &knowledgeThumbnailCache{entries: make(map[string]*list.Element), lru: list.New(), limit: limit, decode: make(chan struct{}, 1)}
}

var knowledgeThumbnails = newKnowledgeThumbnailCache(32 << 20)

func (cache *knowledgeThumbnailCache) get(key string) ([]byte, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	e, ok := cache.entries[key]
	if !ok {
		return nil, false
	}
	cache.lru.MoveToFront(e)
	return e.Value.(thumbnailEntry).data, true
}

func (cache *knowledgeThumbnailCache) put(key string, data []byte) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(data) > cache.limit {
		return
	}
	if old, ok := cache.entries[key]; ok {
		cache.bytes -= len(old.Value.(thumbnailEntry).data)
		cache.lru.Remove(old)
	}
	cache.entries[key] = cache.lru.PushFront(thumbnailEntry{key, data})
	cache.bytes += len(data)
	for cache.bytes > cache.limit {
		old := cache.lru.Back()
		entry := old.Value.(thumbnailEntry)
		delete(cache.entries, entry.key)
		cache.bytes -= len(entry.data)
		cache.lru.Remove(old)
	}
}

func thumbnailKey(k *types.Knowledge) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("thumb-v1:%d:%s:%s:%s:%s", k.TenantID, k.KnowledgeBaseID, k.ID, k.FileHash, k.FilePath))))
}

func encodeKnowledgeThumbnail(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, thumbnailMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > thumbnailMaxBytes {
		return nil, fmt.Errorf("image exceeds thumbnail byte limit")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unsupported image")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > thumbnailMaxPixels/int64(config.Height) {
		return nil, fmt.Errorf("image exceeds thumbnail pixel limit")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid image")
	}
	w, h := config.Width, config.Height
	if w > thumbnailEdge || h > thumbnailEdge {
		if w >= h {
			h = max(1, h*thumbnailEdge/w)
			w = thumbnailEdge
		} else {
			w = max(1, w*thumbnailEdge/h)
			h = thumbnailEdge
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ThumbnailKnowledgeFile uses the same read grant as the original preview.
// Authorization is checked before cache hits and conditional responses.
func (h *KnowledgeHandler) ThumbnailKnowledgeFile(c *gin.Context) {
	knowledge, ctx, err := h.resolveKnowledgeAndValidateKBAccess(c, c.Param("id"), types.OrgRoleViewer)
	if err != nil {
		c.Error(err)
		return
	}
	key := thumbnailKey(knowledge)
	c.Header("Cache-Control", "private, no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	data, ok := knowledgeThumbnails.get(key)
	if !ok {
		result, err, _ := knowledgeThumbnails.work.Do(key, func() (interface{}, error) {
			if data, ok := knowledgeThumbnails.get(key); ok {
				return data, nil
			}
			select {
			case knowledgeThumbnails.decode <- struct{}{}:
				defer func() { <-knowledgeThumbnails.decode }()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			file, _, err := h.kgService.GetKnowledgeFile(ctx, knowledge.ID)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			data, err := encodeKnowledgeThumbnail(file)
			if err == nil {
				knowledgeThumbnails.put(key, data)
			}
			return data, err
		})
		if err != nil {
			if err == context.Canceled {
				return
			}
			c.Error(errors.NewBadRequestError("Image thumbnail is unavailable"))
			return
		}
		data = result.([]byte)
	}
	etag := fmt.Sprintf("\"%x\"", sha256.Sum256(data))
	c.Header("ETag", etag)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "image/png", data)
}
