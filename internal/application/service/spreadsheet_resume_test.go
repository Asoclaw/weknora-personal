package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

type resumeChunkRepo struct {
	parentChildChunkService
	rows []*types.Chunk
}

func (r *resumeChunkRepo) ListPagedChunksByKnowledgeID(_ context.Context, _ uint64, _ string, p *types.Pagination, _ []types.ChunkType, _ []string, _, _, _, _ string, _ *bool) ([]*types.Chunk, int64, error) {
	start := (p.Page - 1) * p.PageSize
	if start >= len(r.rows) {
		return nil, int64(len(r.rows)), nil
	}
	return r.rows[start:min(start+p.PageSize, len(r.rows))], int64(len(r.rows)), nil
}
func (r *resumeChunkRepo) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	panic("resume must not delete chunks")
}
func (r *resumeChunkRepo) CreateChunks(context.Context, []*types.Chunk) error {
	panic("resume must not recreate chunks")
}

type resumeIndexEngine struct {
	parentChildRetrieveEngine
	ids     []string
	windows []int
}

func (e *resumeIndexEngine) BatchIndex(_ context.Context, _ embedding.Embedder, rows []*types.IndexInfo, _ []types.RetrieverType) error {
	e.windows = append(e.windows, len(rows))
	for _, row := range rows {
		e.ids = append(e.ids, row.SourceID)
	}
	return nil
}

func TestResumeSpreadsheetPreservesChunkIDsAndUsesBoundedWindows(t *testing.T) {
	k := &types.Knowledge{ID: "sheet", TenantID: 1, KnowledgeBaseID: "kb", FileType: "xlsx", FilePath: "local://original", FileHash: "original-hash", ParseStatus: types.ParseStatusProcessing}
	r := &resumeChunkRepo{}
	for i := 0; i < 300; i++ {
		r.rows = append(r.rows, &types.Chunk{ID: fmt.Sprint(i), KnowledgeID: k.ID, TenantID: 1, KnowledgeBaseID: "kb", Content: "unchanged cells", IsEnabled: true})
	}
	e := &resumeIndexEngine{}
	tenant := &types.Tenant{ID: 1, RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{RetrieverType: types.VectorRetrieverType, RetrieverEngineType: types.PostgresRetrieverEngineType}}}}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	s := &knowledgeService{repo: &parentChildKnowledgeRepo{knowledge: k}, chunkRepo: r, modelService: parentChildModelService{embedder: parentChildEmbedder{}}, retrieveEngine: parentChildRetrieveRegistry{engine: e}, tenantRepo: parentChildTenantRepo{}, task: parentChildTaskEnqueuer{}}
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 1, EmbeddingModelID: "embed", IndexingStrategy: types.IndexingStrategy{VectorEnabled: true}}
	resumed, err := s.resumeSpreadsheetIndex(ctx, kb, k, 1)
	if err != nil || !resumed {
		t.Fatalf("resumed=%v err=%v", resumed, err)
	}
	if fmt.Sprint(e.windows) != "[128 128 44]" || len(e.ids) != 300 {
		t.Fatal("unbounded or incomplete resume")
	}
	seen := map[string]int{}
	for _, id := range e.ids {
		seen[id]++
	}
	for i, row := range r.rows {
		if seen[fmt.Sprint(i)] != 1 || row.ID != fmt.Sprint(i) || row.Content != "unchanged cells" {
			t.Fatal("identity/content changed")
		}
	}
	if k.FilePath != "local://original" || k.FileHash != "original-hash" || k.ProcessedAt == nil {
		t.Fatal("original source lost or resume not finished")
	}
}
