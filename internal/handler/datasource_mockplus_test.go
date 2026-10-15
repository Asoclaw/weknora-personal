package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type mockplusPreviewService struct {
	interfaces.DataSourceService
	called bool
}

func (s *mockplusPreviewService) ValidateCredentialsWithResources(_ context.Context, kind string, creds map[string]interface{}) ([]types.Resource, error) {
	s.called = true
	return []types.Resource{{ExternalID: "new-project", Name: "Project", Type: "document"}}, nil
}

func TestMockplusCredentialPreviewDoesNotExposeSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &mockplusPreviewService{}
	h := NewDataSourceHandler(svc, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(types.TenantIDContextKey.String(), uint64(1)) })
	r.POST("/preview", h.ValidateCredentials)
	req := httptest.NewRequest(http.MethodPost, "/preview", strings.NewReader(`{"type":"mockplus","credentials":{"username":"page-account","password":"page-secret","base_url":"https://mockplus.example.test","team_id":"team"},"include_resources":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || !svc.called || !strings.Contains(w.Body.String(), "new-project") || strings.Contains(w.Body.String(), "page-secret") || strings.Contains(w.Body.String(), "page-account") {
		t.Fatal("connection preview must return only status and resources")
	}
}
