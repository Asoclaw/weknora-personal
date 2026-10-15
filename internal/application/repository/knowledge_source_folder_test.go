package repository

import (
	"context"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestKnowledgeSourceScopeSeparatesSameFoldersAndMutations(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	kb := uuid.NewString()
	ctx := context.Background()
	first := insertKnowledgeInFolder(t, db, 1, kb, "需求/审批", "one.pdf")
	second := insertKnowledgeInFolder(t, db, 1, kb, "需求/审批", "two.png")
	other := insertKnowledgeInFolder(t, db, 1, kb, "需求/审批", "manual.pdf")
	for id, source := range map[string]string{first: "source-one", second: "source-two"} {
		meta, _ := json.Marshal(map[string]string{"datasource_id": source})
		require.NoError(t, db.Model(&types.Knowledge{}).Where("id=?", id).Update("metadata", types.JSON(meta)).Error)
	}
	rows, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(ctx, 1, kb, &types.Pagination{Page: 1, PageSize: 20}, types.KnowledgeListFilter{DataSourceID: "source-one", FolderPath: "需求/审批", FolderScope: types.FolderScopeExact})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, first, rows[0].ID)
	rows, total, err = repo.ListPagedKnowledgeByKnowledgeBaseID(ctx, 1, kb, &types.Pagination{Page: 1, PageSize: 20}, types.KnowledgeListFilter{Unassigned: true})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, other, rows[0].ID)
	counts, _, err := repo.ListKnowledgeSourceFolderCounts(ctx, 1, kb)
	require.NoError(t, err)
	require.Len(t, counts, 3)
	scoped := types.WithKnowledgeSourceScope(ctx, types.KnowledgeSourceScope{DataSourceID: "source-one"})
	changed, err := repo.RenameKnowledgeFolderPath(scoped, 1, kb, "需求", "需求新版")
	require.NoError(t, err)
	require.Equal(t, int64(1), changed)
	var after []*types.Knowledge
	require.NoError(t, db.Where("knowledge_base_id=?", kb).Find(&after).Error)
	for _, row := range after {
		if row.ID == first {
			require.Equal(t, "需求新版/审批", row.FolderPath)
		} else {
			require.Equal(t, "需求/审批", row.FolderPath)
		}
	}
	rows, total, err = repo.ListPagedKnowledgeByKnowledgeBaseID(ctx, 2, kb, &types.Pagination{Page: 1, PageSize: 20}, types.KnowledgeListFilter{DataSourceID: "source-one"})
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, rows)
}
