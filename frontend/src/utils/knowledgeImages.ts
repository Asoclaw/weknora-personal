import { resolveFilePreviewExt, resolvePreviewKind } from './filePreview'

export interface KnowledgeImage {
  id: string
  knowledge_base_id?: string
  file_type?: string
  file_name?: string
  original_file_name?: string
  title?: string
  file_hash?: string
  file_path?: string
}

export function isKnowledgeImage(item: KnowledgeImage): boolean {
  const ext = resolveFilePreviewExt(item.original_file_name || item.file_name || item.title || '', item.file_type || '')
  return resolvePreviewKind(ext) === 'image'
}

export function knowledgeImageVersion(item: KnowledgeImage): string {
  return item.file_hash || item.file_path || item.id
}

export function displayedImages(root: ParentNode, selector = 'img'): HTMLImageElement[] {
  return Array.from(root.querySelectorAll<HTMLImageElement>(selector)).filter(img =>
    img.complete && img.naturalWidth > 0 && img.getClientRects().length > 0 && Boolean(img.currentSrc || img.src)
      && img.getAttribute?.('data-img-loading') !== '1' && !img.closest?.('.t-image-viewer'),
  )
}
