import type { KnowledgeFolderTree } from '@/api/knowledge-base/index';
import { childFolders } from './folderTree';

export type FolderNavigationItem = {
  key: string;
  name: string;
  path: string;
  group: string | null;
  totalCount: number;
  sourceType?: string;
};

/** Navigation uses provider-scoped tree nodes, never document pagination. */
export function directFolderContents(tree: KnowledgeFolderTree | null, group: string | null, path: string): FolderNavigationItem[] {
  if (!tree) return [];
  const groups = tree.source_groups || [];
  const groupKey = (source: typeof groups[number]) => source.unassigned ? '__unassigned__' : source.id;
  if (groups.length && group === null) {
    return groups.map(source => ({ key: `source:${groupKey(source)}`, name: source.name, path: '', group: groupKey(source), totalCount: source.tree.total_document_count, sourceType: source.type }));
  }
  const scoped = groups.length ? groups.find(source => groupKey(source) === group)?.tree : tree;
  if (!scoped) return [];
  return childFolders(scoped, path).map(node => ({ key: `${group || ''}:${node.path}`, name: node.name, path: node.path, group, totalCount: node.total_count }));
}

export function shouldShowFolderContents(enabled: boolean, filtering: boolean): boolean {
  return enabled && !filtering;
}
