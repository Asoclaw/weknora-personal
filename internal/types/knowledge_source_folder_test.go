package types

import "testing"

func TestSourceFolderGroupsPreserveIndependentTreesAndRemovedSources(t *testing.T) {
	counts := []*KnowledgeSourceFolderCount{{DataSourceID: "one", FolderPath: "需求/审批", Count: 2}, {DataSourceID: "two", FolderPath: "需求/审批", Count: 3}, {FolderPath: "", Count: 1}, {DataSourceID: "removed", FolderPath: "旧稿", Count: 1}}
	sources := []*KnowledgeFolderSource{{ID: "one", Name: "腾讯文档一", Type: "tencent_docs"}, {ID: "two", Name: "腾讯文档二", Type: "tencent_docs"}}
	groups := GroupKnowledgeSourceFolders(counts, sources)
	if len(groups) != 4 || groups[0].Tree.TotalDocumentCount != 2 || groups[1].Tree.TotalDocumentCount != 3 || !groups[2].Removed || !groups[3].Unassigned {
		t.Fatalf("groups=%+v", groups)
	}
	if counts[0].FolderPath != "需求/审批" {
		t.Fatal("grouping changed the stored path")
	}
	if groups[0].Tree.Folders[0].Path != groups[1].Tree.Folders[0].Path {
		t.Fatal("same paths must remain source-local")
	}
	if GroupKnowledgeSourceFolders(counts[:1], sources[:1]) != nil {
		t.Fatal("single source should keep the existing tree")
	}
}
