package types

import (
	"context"
	"sort"
)

// KnowledgeSourceScope is independent from folder_path: source groups are
// virtual navigation and never become stored directory prefixes.
type KnowledgeSourceScope struct {
	DataSourceID string `json:"datasource_id,omitempty"`
	Unassigned   bool   `json:"unassigned,omitempty"`
}
type sourceScopeContextKey struct{}

func WithKnowledgeSourceScope(ctx context.Context, scope KnowledgeSourceScope) context.Context {
	return context.WithValue(ctx, sourceScopeContextKey{}, scope)
}
func KnowledgeSourceScopeFromContext(ctx context.Context) KnowledgeSourceScope {
	scope, _ := ctx.Value(sourceScopeContextKey{}).(KnowledgeSourceScope)
	return scope
}

type KnowledgeSourceFolderCount struct {
	DataSourceID string `json:"datasource_id"`
	FolderPath   string `json:"folder_path"`
	Count        int64  `json:"count"`
}
type KnowledgeFolderSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Removed bool   `json:"removed"`
}
type KnowledgeSourceFolderGroup struct {
	KnowledgeFolderSource
	Unassigned bool                 `json:"unassigned,omitempty"`
	Tree       *KnowledgeFolderTree `json:"tree"`
}

func GroupKnowledgeSourceFolders(counts []*KnowledgeSourceFolderCount, sources []*KnowledgeFolderSource) []*KnowledgeSourceFolderGroup {
	groups := map[string][]*KnowledgeFolderCount{}
	for _, row := range counts {
		if row != nil {
			groups[row.DataSourceID] = append(groups[row.DataSourceID], &KnowledgeFolderCount{FolderPath: row.FolderPath, Count: row.Count})
		}
	}
	result := []*KnowledgeSourceFolderGroup{}
	seen := map[string]bool{}
	for _, source := range sources {
		if source == nil || source.ID == "" || (source.Removed && len(groups[source.ID]) == 0) {
			continue
		}
		seen[source.ID] = true
		result = append(result, &KnowledgeSourceFolderGroup{KnowledgeFolderSource: *source, Tree: BuildKnowledgeFolderTree(groups[source.ID])})
	}
	missing := []string{}
	for id := range groups {
		if id != "" && !seen[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		rows := groups[id]
		result = append(result, &KnowledgeSourceFolderGroup{KnowledgeFolderSource: KnowledgeFolderSource{ID: id, Removed: true}, Tree: BuildKnowledgeFolderTree(rows)})
	}
	if len(result) < 2 {
		return nil
	}
	if len(groups[""]) > 0 {
		result = append(result, &KnowledgeSourceFolderGroup{Unassigned: true, Tree: BuildKnowledgeFolderTree(groups[""])})
	}
	return result
}
