package service

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func (s *DataSourceService) sameSyncContent(ctx context.Context, ds *types.DataSource, existing *types.Knowledge, item *types.FetchedItem) (bool, error) {
	if len(item.Content) == 0 || existing.Type != "file" {
		return false, nil // A URL or source timestamp alone is not content evidence.
	}
	if existing.FileHash != "" && existing.FileHash == calculateStr(string(item.Content)) {
		return true, nil
	}
	// Tencent Docs prepends a generated title heading to the downloaded body.
	// A document rename changes those wrapper bytes, not the source body. Read
	// the stored file for older rows too, without rewriting its storage or index.
	if ds.Type != types.ConnectorTypeTencentDocs || existing.Channel != types.ConnectorTypeTencentDocs || existing.FilePath == "" {
		return false, nil
	}
	prefix := "# " + item.Title + "\n\n"
	if !strings.HasPrefix(string(item.Content), prefix) {
		return false, nil
	}
	file, _, err := s.knowledgeService.GetKnowledgeFile(ctx, existing.ID)
	if err != nil {
		return false, fmt.Errorf("read existing source content: %w", err)
	}
	defer file.Close()
	stored, err := io.ReadAll(file)
	if err != nil {
		return false, fmt.Errorf("read existing source content: %w", err)
	}
	heading, body, ok := strings.Cut(string(stored), "\n\n")
	return ok && strings.HasPrefix(heading, "# ") && body == strings.TrimPrefix(string(item.Content), prefix), nil
}

// unchangedFileColumns deliberately excludes parsing, storage, chunk and Wiki
// fields. Metadata enrichment and renames must never invalidate derived content.
func unchangedFileColumns(existing *types.Knowledge, fileName, folderPath string, metadata types.JSON) (map[string]interface{}, error) {
	current, err := existing.Metadata.Map()
	if err != nil {
		return nil, err
	}
	next, err := metadata.Map()
	if err != nil {
		return nil, err
	}
	columns := map[string]interface{}{}
	if !reflect.DeepEqual(current, next) {
		columns["metadata"] = metadata
	}
	if fileName != existing.FileName {
		columns["file_name"] = fileName
		if existing.Title == existing.FileName {
			columns["title"] = fileName
		}
	}
	if folderPath != existing.FolderPath {
		columns["folder_path"] = folderPath
	}
	return columns, nil
}

func (s *DataSourceService) updateUnchangedSyncItem(ctx context.Context, existing *types.Knowledge, item *types.FetchedItem, updates map[string]string) error {
	fileName, folderPath := existing.FileName, existing.FolderPath
	if item.FileName != "" {
		folderPath, fileName = types.SplitKnowledgeRelativePath(item.FileName)
		var valid bool
		fileName, valid = secutils.ValidateInput(fileName)
		if !valid || fileName == "" {
			return fmt.Errorf("invalid source filename")
		}
		if folderPath != "" {
			folderPath, valid = secutils.ValidateInput(folderPath)
			if !valid {
				return fmt.Errorf("invalid source folder path")
			}
			folderPath = types.NormalizeKnowledgeFolderPath(folderPath)
		}
	}
	metadata, err := mergeKnowledgeMetadata(existing.Metadata, updates)
	if err != nil {
		return err
	}
	columns, err := unchangedFileColumns(existing, fileName, folderPath, metadata)
	if err != nil {
		return err
	}
	if len(columns) == 0 {
		return nil
	}
	if _, changed := columns["metadata"]; changed {
		columns["metadata"] = types.KnowledgeMetadataPatch(updates)
	}
	if err := s.knowledgeService.GetRepository().UpdateKnowledgeColumns(ctx, existing.ID, columns); err != nil {
		return fmt.Errorf("update unchanged source metadata: %w", err)
	}
	if title, ok := columns["title"].(string); ok {
		existing.Title = title
	}
	existing.FileName, existing.FolderPath, existing.Metadata = fileName, folderPath, metadata
	return nil
}
