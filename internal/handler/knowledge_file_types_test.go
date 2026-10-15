package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListKnowledgeMultiFileTypesQuery(t *testing.T) {
	service := &stubFolderKGService{}
	router := newFolderRouter(service)
	request := httptest.NewRequest(http.MethodGet, "/knowledge-bases/kb-1/knowledge?file_types=.PNG,jpg,png,PDF&file_type=image&keyword=sample", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, []string{"png", "jpg", "pdf"}, service.gotFilter.FileTypes)
	require.Equal(t, "image", service.gotFilter.FileType)
	require.Equal(t, "sample", service.gotFilter.Keyword)
}
