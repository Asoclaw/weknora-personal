package service

import (
	"context"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type sourceLinkRepo struct {
	sweepFakeRepo
	existing *types.Knowledge
}

func (r *sourceLinkRepo) FindByDataSourceExternalID(context.Context, uint64, string, string, string) (*types.Knowledge, error) {
	return r.existing, nil
}

type sourceLinkKS struct {
	sweepFakeKS
	metadata map[string]string
}

func (k *sourceLinkKS) CreateKnowledgeFromFile(_ context.Context, _ string, _ *multipart.FileHeader, metadata map[string]string, _ *bool, _ string, _ []string, _ string, _ *types.KnowledgeProcessOverrides) (*types.Knowledge, error) {
	k.metadata = metadata
	return &types.Knowledge{ID: "new"}, nil
}

func TestIngestItem_TencentSourceLinkPreservedAndReplaced(t *testing.T) {
	for _, tc := range []struct{ name, connector, incoming, want string }{
		{"missing URL preserves legacy metadata", types.ConnectorTypeTencentDocs, "", "https://docs.qq.com/doc/old"},
		{"fresh URL replaces old URL", types.ConnectorTypeTencentDocs, "https://docs.qq.com/doc/new", "https://docs.qq.com/doc/new"},
		{"other connectors unchanged", types.ConnectorTypeRSS, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &sourceLinkRepo{existing: &types.Knowledge{ID: "old", Metadata: types.JSON(`{"source_url":"https://docs.qq.com/doc/old"}`)}}
			ks := &sourceLinkKS{sweepFakeKS: sweepFakeKS{repo: repo}}
			service := &DataSourceService{knowledgeService: ks}
			ds := &types.DataSource{ID: "ds", TenantID: 1, KnowledgeBaseID: "kb", Type: tc.connector}
			updated, err := service.ingestItem(context.Background(), ds, &types.FetchedItem{ExternalID: "saas:d1", FileName: "Document.md", Content: []byte("# Content"), Metadata: map[string]string{"source_url": tc.incoming}}, nil)
			if err != nil || !updated || ks.metadata["source_url"] != tc.want {
				t.Fatalf("source link=%q, updated=%v, err=%v", ks.metadata["source_url"], updated, err)
			}
		})
	}
}
