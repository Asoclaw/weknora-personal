package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeImageFilterKeepsSourceFolderAndPagination(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	kb := uuid.NewString()
	for _, row := range []struct{ name, folder, source, ext string }{
		{"one.PNG", "同名目录", "a", "PNG"}, {"two.jpg", "同名目录", "a", "jpg"},
		{"doc.pdf", "同名目录", "a", "pdf"}, {"other.png", "同名目录", "b", "png"},
		{"outside.png", "其他目录", "a", "png"},
	} {
		id := insertKnowledgeInFolder(t, db, 1, kb, row.folder, row.name)
		metadata, _ := json.Marshal(map[string]string{"datasource_id": row.source})
		require.NoError(t, db.Model(&types.Knowledge{}).Where("id=?", id).Updates(map[string]interface{}{"file_type": row.ext, "metadata": types.JSON(metadata)}).Error)
	}
	filter := types.KnowledgeListFilter{DataSourceID: "a", FolderPath: "同名目录", FolderScope: types.FolderScopeExact, FileType: "image"}
	var ids []string
	for page := 1; page <= 2; page++ {
		rows, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(context.Background(), 1, kb, &types.Pagination{Page: page, PageSize: 1}, filter)
		require.NoError(t, err)
		require.Equal(t, int64(2), total)
		require.Len(t, rows, 1)
		ids = append(ids, rows[0].ID)
		require.NotEqual(t, "pdf", rows[0].FileType)
	}
	require.NotEqual(t, ids[0], ids[1])
}
