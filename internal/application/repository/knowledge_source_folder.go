package repository

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

func applyKnowledgeSourceScope(query *gorm.DB, scope types.KnowledgeSourceScope) *gorm.DB {
	if scope.DataSourceID != "" {
		return query.Where("metadata->>'datasource_id' = ?", scope.DataSourceID)
	}
	if scope.Unassigned {
		return query.Where("COALESCE(metadata->>'datasource_id', '') = ''")
	}
	return query
}

func (r *knowledgeRepository) ListKnowledgeSourceFolderCounts(ctx context.Context, tenantID uint64, kbID string) ([]*types.KnowledgeSourceFolderCount, []*types.KnowledgeFolderSource, error) {
	var counts []*types.KnowledgeSourceFolderCount
	err := r.db.WithContext(ctx).Model(&types.Knowledge{}).Select("COALESCE(metadata->>'datasource_id','') AS data_source_id, folder_path, COUNT(*) AS count").Where("tenant_id = ? AND knowledge_base_id = ? AND parse_status <> ?", tenantID, kbID, types.ParseStatusDeleting).Group("COALESCE(metadata->>'datasource_id',''), folder_path").Order("data_source_id, folder_path").Find(&counts).Error
	if err != nil {
		return nil, nil, err
	}
	var sources []*types.KnowledgeFolderSource
	if !r.db.Migrator().HasTable(&types.DataSource{}) {
		return counts, sources, nil
	}
	// Names and platform types only: never read or expose connection credentials.
	err = r.db.WithContext(ctx).Table("data_sources").Select("id, name, type, (deleted_at IS NOT NULL OR status = 'deleted') AS removed").Where("tenant_id = ? AND knowledge_base_id = ?", tenantID, kbID).Order("created_at, id").Find(&sources).Error
	return counts, sources, err
}
