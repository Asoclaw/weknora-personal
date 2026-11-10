package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRetryManifestRetainsFailuresBeyondDisplayCap(t *testing.T) {
	result := &types.SyncResult{RetryManifest: true}
	svc := &DataSourceService{}
	for i := 0; i < 151; i++ {
		svc.applyFetchedItem(context.Background(), &types.DataSource{Type: types.ConnectorTypeMockplus}, &types.FetchedItem{ExternalID: fmt.Sprintf("mockplus:team:project:b%d", i), Metadata: map[string]string{"error": "network"}}, nil, result)
	}
	require.Equal(t, 151, result.Failed)
	require.Len(t, result.Errors, 100)
	require.Len(t, result.RetryTargets, 151)
	encoded, err := result.ToJSON()
	require.NoError(t, err)
	log := &types.SyncLog{Result: encoded}
	decoded, err := log.ParseResult()
	require.NoError(t, err)
	targets, missing := retryTargets(decoded, 151)
	require.Len(t, targets, 151)
	require.Zero(t, missing)
}

func TestRetryLegacyMissingSamplesRemainUnresolved(t *testing.T) {
	targets, missing := retryTargets(&types.SyncResult{Errors: []types.SyncItemError{{Title: "项目 / 画板"}}}, 99)
	require.Len(t, targets, 1)
	require.Equal(t, "legacy", targets[0].Kind)
	require.Equal(t, 98, missing)
}

func TestSyncLeaseSerializesRunsAndReleases(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	svc := &DataSourceService{redisClient: rdb}
	_, release, err := svc.acquireSyncLease(context.Background(), "source", "first")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, _, err = svc.acquireSyncLease(ctx, "source", "second")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	release()
	_, release, err = svc.acquireSyncLease(context.Background(), "source", "second")
	require.NoError(t, err)
	require.NoError(t, rdb.Set(context.Background(), "weknora:datasource-sync:source", "different-owner", time.Minute).Err())
	release()
	value, err := mr.Get("weknora:datasource-sync:source")
	require.NoError(t, err)
	require.Equal(t, "different-owner", value)
}

func TestTargetedSyncRetryDoesNotRestartCanceledOrFailedParsing(t *testing.T) {
	for _, status := range []string{types.ParseStatusCancelled, types.ParseStatusFailed} {
		t.Run(status, func(t *testing.T) {
			existing := &types.Knowledge{ID: "stable", Type: "file", FileHash: calculateStr("same"), FileName: "a.md", ParseStatus: status, Metadata: types.JSON(`{}`)}
			repo := &stableSourceRepo{sourceLinkRepo: sourceLinkRepo{existing: existing}}
			ks := &stableSourceKS{sourceLinkKS: sourceLinkKS{sweepFakeKS: sweepFakeKS{repo: repo}}}
			svc := &DataSourceService{knowledgeService: ks}
			ctx := context.WithValue(context.Background(), targetedSyncRetryKey{}, true)
			_, err := svc.ingestItem(ctx, &types.DataSource{ID: "ds", Type: types.ConnectorTypeMockplus}, &types.FetchedItem{ExternalID: "doc", FileName: "a.md", Content: []byte("same")}, nil)
			require.Error(t, err)
			require.Empty(t, ks.reparsed)
			require.Empty(t, ks.deleted)
		})
	}
}
