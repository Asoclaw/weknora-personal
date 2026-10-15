package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

func sampleThumbnailImage(w, h int, c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes()
}

func TestThumbnailPreservesAspectAndTransparency(t *testing.T) {
	data, err := encodeKnowledgeThumbnail(bytes.NewReader(sampleThumbnailImage(400, 1600, color.NRGBA{R: 180, A: 80})))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 160 || img.Bounds().Dy() != 640 {
		t.Fatal(img.Bounds())
	}
	_, _, _, a := img.At(0, 0).RGBA()
	if a == 0 || a == 65535 {
		t.Fatalf("alpha lost: %d", a)
	}
	if _, err := encodeKnowledgeThumbnail(bytes.NewBufferString("not an image")); err == nil {
		t.Fatal("invalid source accepted")
	}
}

func TestThumbnailRejectsHugeDimensionsBeforeDecode(t *testing.T) {
	var header bytes.Buffer
	header.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&header, binary.BigEndian, uint32(13))
	chunk := []byte("IHDR")
	for range 2 {
		var dimension [4]byte
		binary.BigEndian.PutUint32(dimension[:], 100000)
		chunk = append(chunk, dimension[:]...)
	}
	chunk = append(chunk, 8, 6, 0, 0, 0)
	header.Write(chunk)
	_ = binary.Write(&header, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	_, err := encodeKnowledgeThumbnail(bytes.NewReader(header.Bytes()))
	if err == nil || !strings.Contains(err.Error(), "pixel limit") {
		t.Fatal(err)
	}
}

func TestThumbnailCacheBoundedAndContentVersioned(t *testing.T) {
	cache := newKnowledgeThumbnailCache(4)
	cache.put("one", []byte{1, 2})
	cache.put("two", []byte{3, 4})
	cache.get("one")
	cache.put("three", []byte{5, 6})
	if _, ok := cache.get("two"); ok {
		t.Fatal("LRU entry not evicted")
	}
	if cache.bytes > 4 {
		t.Fatal("cache exceeded byte limit")
	}
	k := &types.Knowledge{ID: "k", TenantID: 1, KnowledgeBaseID: "kb", FileHash: "first", FilePath: "resource://one"}
	before := thumbnailKey(k)
	k.Title = "new title"
	k.FolderPath = "new folder"
	if thumbnailKey(k) != before {
		t.Fatal("metadata invalidates content cache")
	}
	k.FileHash = "second"
	if thumbnailKey(k) == before {
		t.Fatal("changed content reuses stale thumbnail")
	}
}

type thumbnailServiceStub struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
	data      []byte
	reads     int
}

func (s *thumbnailServiceStub) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}
func (s *thumbnailServiceStub) GetKnowledgeFile(context.Context, string) (io.ReadCloser, string, error) {
	s.reads++
	return io.NopCloser(bytes.NewReader(s.data)), "image.png", nil
}

func TestThumbnailAuthorizationCacheAndReplacement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldCache := knowledgeThumbnails
	knowledgeThumbnails = newKnowledgeThumbnailCache(1 << 20)
	t.Cleanup(func() { knowledgeThumbnails = oldCache })
	service := &thumbnailServiceStub{knowledge: &types.Knowledge{ID: "thumb-k", TenantID: 42, KnowledgeBaseID: "kb", FileHash: "first"}, data: sampleThumbnailImage(20, 10, color.NRGBA{R: 255, A: 255})}
	handler := &KnowledgeHandler{kgService: service}
	request := func(tenant uint64, etag string) *httptest.ResponseRecorder {
		router := gin.New()
		router.Use(middleware.ErrorHandler(), func(c *gin.Context) { c.Set(types.TenantIDContextKey.String(), tenant); c.Next() })
		router.GET("/knowledge/:id/thumbnail", handler.ThumbnailKnowledgeFile)
		req := httptest.NewRequest(http.MethodGet, "/knowledge/thumb-k/thumbnail", nil)
		req.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	first := request(42, "")
	if first.Code != 200 || first.Header().Get("Content-Type") != "image/png" {
		t.Fatal(first.Code, first.Body.String())
	}
	if first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatal("public thumbnail cache")
	}
	etag := first.Header().Get("ETag")
	if request(42, etag).Code != 304 || service.reads != 1 {
		t.Fatal("unchanged thumbnail reread source")
	}
	if got := request(99, etag); got.Code != 403 {
		t.Fatal("cache bypasses grant", got.Code, got.Body.String())
	}
	service.knowledge.FileHash = "second"
	service.data = sampleThumbnailImage(20, 10, color.NRGBA{B: 255, A: 255})
	updated := request(42, etag)
	if updated.Code != 200 || updated.Header().Get("ETag") == etag || service.reads != 2 {
		t.Fatal("stale image after replacement")
	}
}
