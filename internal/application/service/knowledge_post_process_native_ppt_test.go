package service

import (
	"context"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"testing"
)

type nativePPTPostRepo struct{ optionalSummaryKnowledgeRepo }

func (r *nativePPTPostRepo) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	r.knowledge = k
	return nil
}

func TestNativeImagePPTCannotCompleteWithoutOCR(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    types.ChunkType
		content string
		want    string
	}{
		{"caption is not OCR", types.ChunkTypeImageCaption, "A slide with a diagram", types.ParseStatusFailed},
		{"empty OCR is not success", types.ChunkTypeImageOCR, " ", types.ParseStatusFailed},
		{"valid OCR permits completion", types.ChunkTypeImageOCR, "应收账款管理方案", types.ParseStatusCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := &types.Knowledge{ID: "doc", TenantID: 7, KnowledgeBaseID: "kb", ParseStatus: types.ParseStatusProcessing, Metadata: types.JSON(`{"tencent_docs_requires_ocr":"true"}`)}
			require.NoError(t, k.SetProcessOverrides(&types.KnowledgeProcessOverrides{SummaryEnabled: processConfigBoolPtr(false)}))
			repo := &nativePPTPostRepo{optionalSummaryKnowledgeRepo: optionalSummaryKnowledgeRepo{wikiEnqueueFailureKnowledgeRepo: wikiEnqueueFailureKnowledgeRepo{knowledge: k}}}
			queue := &wikiEnqueueFailureTaskQueue{}
			svc := &KnowledgePostProcessService{knowledgeRepo: repo, kbService: &wikiEnqueueFailureKBService{kb: &types.KnowledgeBase{ID: "kb", TenantID: 7}}, chunkRepo: &wikiEnqueueFailureChunkRepo{chunks: []*types.Chunk{{ID: "chunk", TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb", ChunkType: tc.kind, Content: tc.content}}}, taskEnqueuer: queue}
			raw, _ := json.Marshal(types.KnowledgePostProcessPayload{TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb"})
			require.NoError(t, svc.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgePostProcess, raw)))
			require.Equal(t, tc.want, k.ParseStatus)
			if tc.want == types.ParseStatusFailed {
				require.Contains(t, k.ErrorMessage, "OCR")
				require.Empty(t, queue.taskTypes)
			}
		})
	}
}
