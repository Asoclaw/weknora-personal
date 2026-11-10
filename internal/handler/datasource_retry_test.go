package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type retryHandlerStub struct {
	*stubDataSourceService
	calls int
	busy  bool
}

func (s *retryHandlerStub) RetryFailedSync(_ context.Context, ds, log string) (*types.SyncLog, error) {
	s.calls++
	if s.busy {
		return nil, datasource.ErrSyncBusy
	}
	return &types.SyncLog{ID: "new-retry", DataSourceID: ds, Status: types.SyncLogStatusRunning}, nil
}

func TestDataSourceRetryOwnershipAndBusy(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		requestTenant, kbTenant uint64
		busy                    bool
		want, calls             int
	}{
		{"no login", 0, 1, false, 401, 0}, {"other tenant", 2, 1, false, 403, 0}, {"busy", 1, 1, true, 409, 1}, {"accepted", 1, 1, false, 202, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &retryHandlerStub{stubDataSourceService: &stubDataSourceService{getDataSource: func(context.Context, string) (*types.DataSource, error) {
				return &types.DataSource{ID: "ds", TenantID: tc.kbTenant, KnowledgeBaseID: "kb"}, nil
			}}, busy: tc.busy}
			h := NewDataSourceHandler(svc, &stubKBServiceForDS{getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
				return &types.KnowledgeBase{ID: "kb", TenantID: tc.kbTenant}, nil
			}})
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(types.TenantIDContextKey.String(), tc.requestTenant) })
			r.POST("/datasource/:id/logs/:log_id/retry", h.RetryFailedSync)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/datasource/ds/logs/failed/retry", nil))
			require.Equal(t, tc.want, w.Code)
			require.Equal(t, tc.calls, svc.calls)
		})
	}
}
