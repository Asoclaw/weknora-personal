package service

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestCreateKnowledgeFromFileMockplusKeepsSameImageBoardsDistinct(t *testing.T) {
	db := setupKnowledgeSharedAccessDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := &gitLabKnowledgeRepo{KnowledgeRepository: repository.NewKnowledgeRepository(db)}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1", VLMConfig: types.VLMConfig{Enabled: true, ModelID: "test-vision"}}},
		fileSvc:   &createKnowledgeFileServiceStub{},
		task:      &createKnowledgeTaskEnqueuerStub{},
	}
	ctx := newCreateKnowledgeFileContext()
	create := func(board string) (*types.Knowledge, error) {
		return svc.CreateKnowledgeFromFile(ctx, "kb-1", newMultipartFileHeader(t, "审批.png", "same test image bytes"),
			map[string]string{"datasource_id": "mockplus-source", "external_id": "mockplus:team:app:" + board},
			nil, "财务/费用/审批.png", nil, types.ConnectorTypeMockplus, nil)
	}
	first, err := create("board-one")
	require.NoError(t, err)
	second, err := create("board-two")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, "财务/费用", second.FolderPath)
	require.Equal(t, "审批.png", second.Title)
	require.NotEmpty(t, second.FilePath, "original image must be retained for preview")
	createdAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", second.ID).Update("created_at", createdAt).Error)
	duplicate, err := create("board-two")
	var dupErr *types.DuplicateKnowledgeError
	require.ErrorAs(t, err, &dupErr)
	require.Equal(t, second.ID, duplicate.ID)
	require.True(t, createdAt.Equal(duplicate.CreatedAt), "duplicate upload must preserve creation time")
}
