package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stableContentConnector struct {
	deletedItemConnector
	items []types.FetchedItem
}

func (c stableContentConnector) Type() string { return types.ConnectorTypeTencentDocs }
func (c stableContentConnector) FetchAll(context.Context, *types.DataSourceConfig, []string) ([]types.FetchedItem, error) {
	return c.items, nil
}
func (c stableContentConnector) FetchIncremental(context.Context, *types.DataSourceConfig, *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	return c.items, &types.SyncCursor{ConnectorCursor: map[string]interface{}{"marker": "new"}}, nil
}

func TestProcessSyncContentStabilityWithPersistedWikiAndChunks(t *testing.T) {
	for _, trigger := range []string{"manual", "scheduled", "full"} {
		t.Run(trigger, func(t *testing.T) {
			db := setupKnowledgeSharedAccessDB(t)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			require.NoError(t, db.AutoMigrate(&types.WikiPage{}))
			require.NoError(t, db.Exec("CREATE TABLE chunks (id text PRIMARY KEY, knowledge_id text, content text)").Error)
			existing := &types.Knowledge{ID: "stable", TenantID: 1, KnowledgeBaseID: "kb", Type: "file", FileName: "a.md", Title: "a.md", FileHash: calculateStr("unchanged"), ParseStatus: types.ParseStatusCompleted, Metadata: types.JSON(`{"external_id":"doc","datasource_id":"ds"}`)}
			require.NoError(t, db.Create(existing).Error)
			page := types.WikiPage{ID: "wiki-stable", TenantID: 1, KnowledgeBaseID: "kb", Slug: "entity/stable", Content: "Existing curated Wiki content", SourceRefs: types.StringArray{"stable"}, OutLinks: types.StringArray{"concept/other"}}
			require.NoError(t, db.Create(&page).Error)
			require.NoError(t, db.First(&page, "id = ?", page.ID).Error)
			require.NoError(t, db.Exec("INSERT INTO chunks VALUES ('chunk-stable','stable','Existing parsed content')").Error)
			ks := &stableSourceKS{sourceLinkKS: sourceLinkKS{sweepFakeKS: sweepFakeKS{repo: repository.NewKnowledgeRepository(db)}}}
			config, err := (&types.DataSourceConfig{Type: types.ConnectorTypeTencentDocs}).ToJSON()
			require.NoError(t, err)
			ds := &types.DataSource{ID: "ds", TenantID: 1, KnowledgeBaseID: "kb", Type: types.ConnectorTypeTencentDocs, Config: config, Status: types.DataSourceStatusActive, SyncMode: types.SyncModeIncremental}
			log := &types.SyncLog{ID: "log", DataSourceID: ds.ID}
			logs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{"log": log}}
			registry := datasource.NewConnectorRegistry()
			require.NoError(t, registry.Register(stableContentConnector{items: []types.FetchedItem{{ExternalID: "doc", FileName: "new/Renamed.md", Content: []byte("unchanged"), Metadata: map[string]string{"source_url": "https://example.com/doc"}}}}))
			svc := &DataSourceService{dsRepo: newKBDeleteDSRepo("kb", ds), syncLogRepo: logs, knowledgeService: ks, kbService: &processSyncKBService{kb: &types.KnowledgeBase{ID: "kb", TenantID: 1}}, connectorRegistry: registry, tenantRepo: &processSyncTenantRepo{tenant: &types.Tenant{ID: 1}}, tagService: &processSyncTagService{}}
			payload, err := json.Marshal(types.DataSourceSyncPayload{DataSourceID: ds.ID, TenantID: 1, SyncLogID: "log", ForceFull: trigger == "full", Trigger: trigger})
			require.NoError(t, err)
			require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
			assert.Equal(t, 1, logs.logs["log"].ItemsSkipped)
			assert.Zero(t, logs.logs["log"].ItemsUpdated)
			assert.Empty(t, ks.deleted)
			assert.Nil(t, ks.metadata)
			assert.Empty(t, ks.reparsed)
			var after types.Knowledge
			require.NoError(t, db.First(&after, "id = ?", "stable").Error)
			assert.Equal(t, "Renamed.md", after.FileName)
			assert.Equal(t, "https://example.com/doc", after.GetMetadata()["source_url"])
			assert.True(t, existing.CreatedAt.Equal(after.CreatedAt))
			var wikiAfter types.WikiPage
			require.NoError(t, db.First(&wikiAfter, "id = ?", page.ID).Error)
			assert.Equal(t, page, wikiAfter, "Wiki body, references and graph links must remain identical")
			var content string
			require.NoError(t, db.Raw("SELECT content FROM chunks WHERE id='chunk-stable'").Scan(&content).Error)
			assert.Equal(t, "Existing parsed content", content)
		})
	}
}

type stableSourceRepo struct {
	sourceLinkRepo
	writes              []map[string]interface{}
	writeErr, lookupErr error
}

func TestProcessSyncTencentFailedMetadataWriteRetainsCursorForRetry(t *testing.T) {
	oldCursor := types.JSON(`{"connector_cursor":{"marker":"old"}}`)
	config, err := (&types.DataSourceConfig{Type: types.ConnectorTypeTencentDocs}).ToJSON()
	require.NoError(t, err)
	ds := &types.DataSource{ID: "ds", TenantID: 1, KnowledgeBaseID: "kb", Type: types.ConnectorTypeTencentDocs, Config: config, Status: types.DataSourceStatusActive, SyncMode: types.SyncModeIncremental, LastSyncCursor: oldCursor}
	existing := &types.Knowledge{ID: "stable", Type: "file", FileHash: calculateStr("same"), FileName: "a.md", Metadata: types.JSON(`{}`)}
	repo := &stableSourceRepo{sourceLinkRepo: sourceLinkRepo{existing: existing}, writeErr: errors.New("metadata unavailable")}
	ks := &stableSourceKS{sourceLinkKS: sourceLinkKS{sweepFakeKS: sweepFakeKS{repo: repo}}}
	logs := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{"log": {ID: "log", DataSourceID: ds.ID}}}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(stableContentConnector{items: []types.FetchedItem{{ExternalID: "doc", FileName: "a.md", Content: []byte("same")}, {ExternalID: "unsupported"}}}))
	svc := &DataSourceService{dsRepo: newKBDeleteDSRepo("kb", ds), syncLogRepo: logs, knowledgeService: ks, kbService: &processSyncKBService{kb: &types.KnowledgeBase{ID: "kb", TenantID: 1}}, connectorRegistry: registry, tenantRepo: &processSyncTenantRepo{tenant: &types.Tenant{ID: 1}}, tagService: &processSyncTagService{}}
	payload, err := json.Marshal(types.DataSourceSyncPayload{DataSourceID: ds.ID, TenantID: 1, SyncLogID: "log", Trigger: "manual"})
	require.NoError(t, err)
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
	assert.Equal(t, 1, logs.logs["log"].ItemsFailed)
	assert.JSONEq(t, string(oldCursor), string(ds.LastSyncCursor))
	assert.Empty(t, ks.deleted)
	// The next run can still fetch the metadata update after the failure clears.
	repo.writeErr = nil
	require.NoError(t, svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload)))
	assert.Zero(t, logs.logs["log"].ItemsFailed)
	assert.Equal(t, 2, logs.logs["log"].ItemsSkipped)
	assert.Contains(t, string(ds.LastSyncCursor), "new")
	assert.Empty(t, ks.deleted)
}

func (r *stableSourceRepo) FindByDataSourceExternalID(context.Context, uint64, string, string, string) (*types.Knowledge, error) {
	return r.existing, r.lookupErr
}

func (r *stableSourceRepo) UpdateKnowledgeColumns(_ context.Context, _ string, columns map[string]interface{}) error {
	r.writes = append(r.writes, columns)
	return r.writeErr
}

type stableSourceKS struct {
	sourceLinkKS
	reparsed      []string
	storedContent string
}

func (k *stableSourceKS) GetKnowledgeFile(context.Context, string) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader(k.storedContent)), "a.md", nil
}

func (k *stableSourceKS) ReparseKnowledge(_ context.Context, id string, _ *types.KnowledgeProcessOverrides) (*types.Knowledge, error) {
	k.reparsed = append(k.reparsed, id)
	return &types.Knowledge{ID: id}, nil
}

func TestSyncUnchangedContentPreservesKnowledgeAndDerivedState(t *testing.T) {
	for _, connector := range []string{types.ConnectorTypeTencentDocs, types.ConnectorTypeMockplus, types.ConnectorTypeRSS} {
		for _, status := range []string{types.ParseStatusCompleted, types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing, types.ParseStatusCancelled} {
			t.Run(connector+"/"+status, func(t *testing.T) {
				original := types.Knowledge{
					ID: "stable", Type: "file", FileHash: calculateStr("unchanged body"),
					Title: "Document.md", FileName: "Document.md", FolderPath: "old",
					FilePath: "stored/file.md", ParseStatus: status, PendingSubtasksCount: 2,
					CreatedAt: time.Unix(1, 0),
					Metadata:  types.JSON(`{"external_id":"doc-1","datasource_id":"ds","custom":{"keep":true},"source_url":"https://example.com/old"}`),
				}
				existing := original
				repo := &stableSourceRepo{sourceLinkRepo: sourceLinkRepo{existing: &existing}}
				ks := &stableSourceKS{sourceLinkKS: sourceLinkKS{sweepFakeKS: sweepFakeKS{repo: repo}}}
				svc := &DataSourceService{knowledgeService: ks}
				ds := &types.DataSource{ID: "ds", TenantID: 1, KnowledgeBaseID: "kb", Type: connector}
				item := &types.FetchedItem{ExternalID: "doc-1", Content: []byte("unchanged body"), FileName: "new/Renamed.md", Metadata: map[string]string{"source_url": "https://example.com/new"}, UpdatedAt: time.Unix(100, 0)}
				result := &types.SyncResult{}
				svc.applyFetchedItem(context.Background(), ds, item, nil, result)
				require.Equal(t, 1, result.Skipped)
				assert.Zero(t, result.Created)
				assert.Zero(t, result.Updated)
				assert.Zero(t, result.Failed)
				assert.Empty(t, ks.deleted, "deletion would retract Wiki pages and graph links")
				assert.Nil(t, ks.metadata, "no fresh file ingestion")
				assert.Empty(t, ks.reparsed, "no parse, Wiki or graph rebuild")
				require.Len(t, repo.writes, 1)
				assert.Equal(t, "Renamed.md", existing.Title)
				assert.Equal(t, "new", existing.FolderPath)
				assert.Equal(t, "https://example.com/new", existing.GetMetadata()["source_url"])
				metadata, err := existing.Metadata.Map()
				require.NoError(t, err)
				assert.Equal(t, map[string]interface{}{"keep": true}, metadata["custom"])
				// Only source metadata, filename, folder and a mirrored title may change.
				existing.Title, existing.FileName, existing.FolderPath, existing.Metadata = original.Title, original.FileName, original.FolderPath, original.Metadata
				assert.Equal(t, original, existing, "all persisted processing/storage state is preserved")
			})
		}
	}
}

func TestTencentRenameIgnoresGeneratedHeadingButDetectsRealBodyChanges(t *testing.T) {
	for _, body := range []string{"same body", "changed body"} {
		t.Run(body, func(t *testing.T) {
			stored := "# Original title\n\nsame body\n"
			repo := &stableSourceRepo{sourceLinkRepo: sourceLinkRepo{existing: &types.Knowledge{ID: "stable", Type: "file", Channel: types.ConnectorTypeTencentDocs, FileName: "Original title.md", Title: "Original title.md", FilePath: "stored.md", FileHash: calculateStr(stored), Metadata: types.JSON(`{}`)}}}
			ks := &stableSourceKS{sourceLinkKS: sourceLinkKS{sweepFakeKS: sweepFakeKS{repo: repo}}, storedContent: stored}
			svc := &DataSourceService{knowledgeService: ks}
			result := &types.SyncResult{}
			svc.applyFetchedItem(context.Background(), &types.DataSource{ID: "ds", Type: types.ConnectorTypeTencentDocs}, &types.FetchedItem{ExternalID: "doc", Title: "Renamed title", FileName: "Renamed title.md", Content: []byte("# Renamed title\n\n" + body + "\n")}, nil, result)
			if body == "same body" {
				assert.Equal(t, 1, result.Skipped)
				assert.Empty(t, ks.deleted)
				assert.Nil(t, ks.metadata)
				assert.Equal(t, "Renamed title.md", repo.existing.FileName)
			} else {
				assert.Equal(t, 1, result.Updated)
				assert.Equal(t, []string{"stable"}, ks.deleted)
			}
		})
	}
}

func TestSyncMetadataWriteAndLookupFailureNeverDeleteKnowledge(t *testing.T) {
	for _, failure := range []string{"metadata write", "lookup"} {
		t.Run(failure, func(t *testing.T) {
			existing := &types.Knowledge{ID: "stable", Type: "file", FileHash: calculateStr("same"), FileName: "a.md", Metadata: types.JSON(`{}`)}
			repo := &stableSourceRepo{sourceLinkRepo: sourceLinkRepo{existing: existing}}
			if failure == "metadata write" {
				repo.writeErr = errors.New("write failed")
			} else {
				repo.lookupErr = errors.New("lookup failed")
			}
			ks := &stableSourceKS{sourceLinkKS: sourceLinkKS{sweepFakeKS: sweepFakeKS{repo: repo}}}
			svc := &DataSourceService{knowledgeService: ks}
			result := &types.SyncResult{}
			svc.applyFetchedItem(context.Background(), &types.DataSource{ID: "ds", Type: types.ConnectorTypeTencentDocs}, &types.FetchedItem{ExternalID: "doc", FileName: "a.md", Content: []byte("same")}, nil, result)
			assert.Equal(t, 1, result.Failed)
			assert.Empty(t, ks.deleted)
			assert.Nil(t, ks.metadata)
			assert.Empty(t, ks.reparsed)
		})
	}
}

func TestSyncUnchangedFailedParseRetriesExistingID(t *testing.T) {
	existing := &types.Knowledge{ID: "stable", Type: "file", FileHash: calculateStr("same"), FileName: "a.md", ParseStatus: types.ParseStatusFailed, Metadata: types.JSON(`{}`)}
	repo := &stableSourceRepo{sourceLinkRepo: sourceLinkRepo{existing: existing}}
	ks := &stableSourceKS{sourceLinkKS: sourceLinkKS{sweepFakeKS: sweepFakeKS{repo: repo}}}
	svc := &DataSourceService{knowledgeService: ks}
	updated, err := svc.ingestItem(context.Background(), &types.DataSource{ID: "ds"}, &types.FetchedItem{ExternalID: "doc", FileName: "a.md", Content: []byte("same")}, nil)
	require.NoError(t, err)
	assert.True(t, updated)
	assert.Equal(t, []string{"stable"}, ks.reparsed)
	assert.Empty(t, ks.deleted)
	assert.Nil(t, ks.metadata)
}

func TestSyncChangedContentStillUpdatesSource(t *testing.T) {
	repo := &stableSourceRepo{sourceLinkRepo: sourceLinkRepo{existing: &types.Knowledge{ID: "old", Type: "file", FileHash: calculateStr("old body")}}}
	ks := &stableSourceKS{sourceLinkKS: sourceLinkKS{sweepFakeKS: sweepFakeKS{repo: repo}}}
	svc := &DataSourceService{knowledgeService: ks}
	updated, err := svc.ingestItem(context.Background(), &types.DataSource{ID: "ds"}, &types.FetchedItem{ExternalID: "doc", FileName: "a.md", Content: []byte("new body")}, nil)
	require.NoError(t, err)
	assert.True(t, updated)
	assert.Equal(t, []string{"old"}, ks.deleted)
	assert.NotNil(t, ks.metadata)
	assert.Empty(t, repo.writes)
}

func TestReplaceKnowledgeFileSameContentRenameAndMovePreservesProcessing(t *testing.T) {
	h := newReplaceFileHarness(t)
	got, err := h.replace(t, replaceFileOldContent, "new/sub/Renamed.md", map[string]string{"source_url": "https://example.com/new"})
	var duplicate *types.DuplicateKnowledgeError
	require.ErrorAs(t, err, &duplicate)
	assert.Equal(t, h.original.ID, got.ID)
	assert.Equal(t, "Renamed.md", h.repo.row.FileName)
	assert.Equal(t, "new/sub", h.repo.row.FolderPath)
	assert.Equal(t, h.original.ParseStatus, h.repo.row.ParseStatus)
	assert.Equal(t, h.original.FilePath, h.repo.row.FilePath)
	assert.Equal(t, h.original.CreatedAt, h.repo.row.CreatedAt)
	assert.Empty(t, h.events, "no save, cancel, enqueue or storage deletion")
}
