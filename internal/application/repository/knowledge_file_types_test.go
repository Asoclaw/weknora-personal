package repository

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestListFileTypesMultiSelectAndImageIntersection(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db)
	for id, ext := range map[string]string{"a": "PNG", "b": "jpg", "c": "jpeg", "d": "pdf", "e": "xlsx"} {
		require.NoError(t, db.Exec("INSERT INTO knowledges (id,tenant_id,knowledge_base_id,type,title,source,file_type,parse_status,folder_path) VALUES (?,1,'kb','file','sample','manual',?,'completed','folder')", id, ext).Error)
	}
	for _, tc := range []struct {
		filter types.KnowledgeListFilter
		want   int64
	}{
		{types.KnowledgeListFilter{FileTypes: []string{".PNG", "pdf", "png"}}, 2},
		{types.KnowledgeListFilter{FileTypes: []string{"jpg"}}, 1},
		{types.KnowledgeListFilter{FileTypes: []string{"jpg", "jpeg"}}, 2},
		{types.KnowledgeListFilter{FileType: "image", FileTypes: []string{"png", "pdf"}}, 1},
		{types.KnowledgeListFilter{FileType: "pdf"}, 1},
		{types.KnowledgeListFilter{FileTypes: []string{"png", "pdf"}, Keyword: "missing"}, 0},
		{types.KnowledgeListFilter{}, 5},
	} {
		rows, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(context.Background(), 1, "kb", &types.Pagination{Page: 1, PageSize: 20}, tc.filter)
		require.NoError(t, err)
		require.Equal(t, tc.want, total)
		require.Len(t, rows, int(tc.want))
	}
}
