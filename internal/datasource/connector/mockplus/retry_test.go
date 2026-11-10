package mockplus

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRetryBoardTouchesOnlyTargetAndPreservesCursor(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	baseline, err := f.connector.FetchStream(ctx, f.config, nil, &recordingHandler{})
	require.NoError(t, err)
	before, _ := json.Marshal(loadState(baseline, baseline.LastSchemaHash)["mockplus:team:app:board2"])
	counts := map[string]int{}
	for k, v := range f.requests {
		counts[k] = v
	}
	h := &recordingHandler{retryID: "mockplus:team:app:board2"}
	next, err := f.connector.FetchRetryStream(ctx, f.config, baseline, []types.SyncRetryTarget{{Kind: "board", ExternalID: "mockplus:team:app:board1", ProjectID: "app", BoardID: "board1"}}, h)
	require.NoError(t, err)
	require.Len(t, h.items, 1)
	require.Equal(t, "mockplus:team:app:board1", h.items[0].ExternalID)
	require.Empty(t, h.items[0].Content, "unchanged target stays a skip")
	require.Equal(t, counts["/two.png"], f.requests["/two.png"])
	require.Equal(t, counts["/one.png"]+1, f.requests["/one.png"])
	after, _ := json.Marshal(loadState(next, next.LastSchemaHash)["mockplus:team:app:board2"])
	require.JSONEq(t, string(before), string(after))
}

func TestRetryLegacyProjectAndAmbiguousBoard(t *testing.T) {
	t.Run("project", func(t *testing.T) {
		f := setup(t)
		h := &recordingHandler{}
		_, err := f.connector.FetchRetryStream(context.Background(), f.config, nil, []types.SyncRetryTarget{{Kind: "legacy", Title: "财务 / 费用"}}, h)
		require.NoError(t, err)
		require.Len(t, h.items, 2)
		require.NotEmpty(t, h.items[0].Content)
	})
	t.Run("same name", func(t *testing.T) {
		f := setup(t)
		h := &recordingHandler{}
		_, err := f.connector.FetchRetryStream(context.Background(), f.config, nil, []types.SyncRetryTarget{{Kind: "legacy", Title: "财务 / 费用 / 审批"}}, h)
		require.NoError(t, err)
		require.Len(t, h.items, 1)
		require.Contains(t, h.items[0].Metadata["error"], "同名")
		require.Zero(t, f.requests["/one.png"])
		require.Zero(t, f.requests["/two.png"])
	})
}

func TestRetryRejectsOutOfScopeMissingAndForeignTeam(t *testing.T) {
	for _, target := range []types.SyncRetryTarget{
		{Kind: "project", ProjectID: "other", ExternalID: "other"},
		{Kind: "board", ProjectID: "app", BoardID: "absent", ExternalID: "mockplus:team:app:absent"},
		{Kind: "board", ProjectID: "app", BoardID: "board1", ExternalID: "mockplus:foreign:app:board1"},
		{Kind: "legacy", Title: "不存在 / 审批"},
	} {
		t.Run(target.ExternalID+target.Title, func(t *testing.T) {
			f := setup(t)
			h := &recordingHandler{}
			_, err := f.connector.FetchRetryStream(context.Background(), f.config, nil, []types.SyncRetryTarget{target}, h)
			require.NoError(t, err)
			require.Len(t, h.items, 1)
			require.NotEmpty(t, h.items[0].Metadata["error"])
			require.Zero(t, f.requests["/one.png"])
			require.Zero(t, f.requests["/two.png"])
			require.Zero(t, f.requests["/api/v1/app/other"])
		})
	}
}

func TestRetryBoardProjectFailureKeepsBoardIdentity(t *testing.T) {
	f := setup(t)
	f.broken = true
	h := &recordingHandler{}
	target := types.SyncRetryTarget{Kind: "board", ProjectID: "app", BoardID: "board1", ExternalID: "mockplus:team:app:board1"}
	_, err := f.connector.FetchRetryStream(context.Background(), f.config, nil, []types.SyncRetryTarget{target}, h)
	require.NoError(t, err)
	require.Len(t, h.items, 1)
	require.Equal(t, target.ExternalID, h.items[0].ExternalID)
	require.Equal(t, "board", h.items[0].Metadata["retry_kind"])
}

func TestRetryDoesNotEmitDeletions(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	baseline, err := f.connector.FetchStream(ctx, f.config, nil, &recordingHandler{})
	require.NoError(t, err)
	f.boards[0].IsDeleted = true
	h := &recordingHandler{}
	_, err = f.connector.FetchRetryStream(ctx, f.config, baseline, []types.SyncRetryTarget{{Kind: "project", ProjectID: "app", ExternalID: "app"}}, h)
	require.NoError(t, err)
	for _, item := range h.items {
		require.False(t, item.IsDeleted)
	}
}

func TestRetryLegacyUsesSavedIdentityAfterRename(t *testing.T) {
	f := setup(t)
	f.boards[1].Name = "其他画板"
	ctx := context.Background()
	baseline, err := f.connector.FetchStream(ctx, f.config, nil, &recordingHandler{})
	require.NoError(t, err)
	f.boards[0].Name = "审批新版"
	f.requests = map[string]int{}
	h := &recordingHandler{}
	_, err = f.connector.FetchRetryStream(ctx, f.config, baseline, []types.SyncRetryTarget{{Kind: "legacy", Title: "财务 / 费用 / 审批"}}, h)
	require.NoError(t, err)
	require.Len(t, h.items, 1)
	require.Equal(t, "mockplus:team:app:board1", h.items[0].ExternalID)
	require.Empty(t, h.items[0].Metadata["error"])
	require.Zero(t, f.requests["/two.png"])
}
