package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestKnowledgeSourceMetadataPatchPreservesConcurrentProcessingMetadata(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	knowledge := &types.Knowledge{ID: "stable", Type: "file", ParseStatus: types.ParseStatusFinalizing, Metadata: types.JSON(`{"source_url":"old","processing":{"preserve":true},"added_after_read":"keep"}`)}
	require.NoError(t, db.Create(knowledge).Error)
	repo := NewKnowledgeRepository(db)
	require.NoError(t, repo.UpdateKnowledgeColumns(context.Background(), knowledge.ID, map[string]interface{}{
		"metadata": types.KnowledgeMetadataPatch{"source_url": "new"}, "file_name": "renamed.md",
	}))
	var after types.Knowledge
	require.NoError(t, db.First(&after, "id = ?", knowledge.ID).Error)
	metadata, err := after.Metadata.Map()
	require.NoError(t, err)
	require.Equal(t, "new", metadata["source_url"])
	require.Equal(t, "keep", metadata["added_after_read"])
	require.Equal(t, map[string]interface{}{"preserve": true}, metadata["processing"])
	require.Equal(t, types.ParseStatusFinalizing, after.ParseStatus)
}
