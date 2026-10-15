package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Resume an atomically persisted spreadsheet's embedding stage. Never go
// through processChunks, whose replacement semantics remove existing rows.
func (s *knowledgeService) resumeSpreadsheetIndex(ctx context.Context, kb *types.KnowledgeBase, k *types.Knowledge, attempt int) (bool, error) {
	if !kb.NeedsEmbeddingModel() {
		return false, nil
	}
	load := func(page int) ([]*types.Chunk, int64, error) {
		return s.chunkRepo.ListPagedChunksByKnowledgeID(ctx, k.TenantID, k.ID, &types.Pagination{Page: page, PageSize: 128}, []types.ChunkType{types.ChunkTypeText}, nil, "", "", "asc", "manual", nil)
	}
	rows, total, err := load(1)
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	ctx = withAttempt(ctx, attempt)
	embedder, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return true, err
	}
	engine, err := retriever.CreateRetrieveEngineForKB(ctx, s.retrieveEngine, s.ownership, k.TenantID, kb.VectorStoreID)
	if err != nil {
		return true, err
	}
	s.beginStage(ctx, k.ID, types.StageEmbedding, types.JSONMap{"resumed_existing_chunks": true, "chunks": total})
	var storage int64
	for page := 1; len(rows) > 0; page++ {
		if aborted, status := s.isKnowledgeAborted(ctx, k.TenantID, k.ID); aborted {
			return true, abortRetryErr(ctx, k.ID, status)
		}
		if s.isKnowledgeSourceReplaced(ctx, k) {
			return true, nil
		}
		indices := make([]*types.IndexInfo, 0, len(rows))
		for _, row := range rows {
			if row.ImageInfo != "" && row.ImageInfo != "[]" {
				return true, fmt.Errorf("spreadsheet image stage must resume separately; existing chunks retained")
			}
			indices = append(indices, &types.IndexInfo{Content: buildKnowledgeIndexContent(k, row.EmbeddingContent()), SourceID: row.ID, SourceType: types.ChunkSourceType, ChunkID: row.ID, KnowledgeID: k.ID, KnowledgeBaseID: kb.ID, IsEnabled: row.IsEnabled})
		}
		if err := engine.BatchIndex(ctx, embedder, indices); err != nil {
			return true, err
		}
		storage += engine.EstimateStorageSize(ctx, embedder, indices)
		rows, _, err = load(page + 1)
		if err != nil {
			return true, err
		}
	}
	if aborted, status := s.isKnowledgeAborted(ctx, k.TenantID, k.ID); aborted {
		return true, abortRetryErr(ctx, k.ID, status)
	}
	if s.isKnowledgeSourceReplaced(ctx, k) {
		return true, nil
	}
	previousStorage := k.StorageSize
	now := time.Now()
	k.ParseStatus = types.ParseStatusProcessing
	k.EnableStatus = "enabled"
	k.ErrorMessage = ""
	k.ProcessedAt = &now
	k.StorageSize = storage
	if err := s.updateKnowledgeUnlessSourceReplaced(ctx, k); err != nil {
		return true, err
	}
	if delta := storage - previousStorage; delta > 0 {
		if err := s.tenantRepo.AdjustStorageUsed(ctx, k.TenantID, delta); err != nil {
			logger.Warnf(ctx, "resume spreadsheet storage accounting failed: %v", err)
		}
	}
	s.endStage(ctx, k.ID, types.StageEmbedding, types.JSONMap{"resumed_existing_chunks": true, "vectors_written": total})
	s.enqueueKnowledgePostProcessTask(ctx, k)
	return true, nil
}
