package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSyncLogAdmissionRejectsOverlappingRuns(t *testing.T) {
	db := setupDataSourceRepoTestDB(t)
	ctx := context.Background()
	require.NoError(t, db.Create(&types.DataSource{ID: "source", TenantID: 1}).Error)
	repo := NewSyncLogRepository(db)
	first := &types.SyncLog{DataSourceID: "source", TenantID: 1, Status: types.SyncLogStatusRunning}
	require.NoError(t, repo.Create(ctx, first))
	require.ErrorIs(t, repo.Create(ctx, &types.SyncLog{DataSourceID: "source", TenantID: 1, Status: types.SyncLogStatusRunning}), datasource.ErrSyncBusy)
	first.Status = types.SyncLogStatusSuccess
	require.NoError(t, repo.UpdateResult(ctx, first))
	require.NoError(t, repo.Create(ctx, &types.SyncLog{DataSourceID: "source", TenantID: 1, Status: types.SyncLogStatusRunning}))
}
