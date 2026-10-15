import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import vm from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import * as vue from 'vue'
import { compileScript, parse } from '@vue/compiler-sfc'
import { isKnowledgeImage, knowledgeImageVersion, displayedImages } from '../../../utils/knowledgeImages'
import * as filePreview from '../../../utils/filePreview'

const require = createRequire(import.meta.url)
const card = readFileSync(new URL('./DocumentCardView.vue', import.meta.url), 'utf8')

test('image types and content versions are independent of titles and folders', () => {
  for (const file_type of ['PNG', 'jpg', 'WEBP', 'svg', 'gif']) assert.equal(isKnowledgeImage({ id: '1', file_type }), true)
  assert.equal(isKnowledgeImage({ id: '1', file_name: 'long-image.png' }), true)
  assert.equal(isKnowledgeImage({ id: '1', file_type: 'pdf' }), false)
  assert.equal(knowledgeImageVersion({ id: '1', file_hash: 'hash', file_name: 'renamed' }), 'hash')
  assert.notEqual(knowledgeImageVersion({ id: '1', file_hash: 'old' }), knowledgeImageVersion({ id: '1', file_hash: 'new' }))
})

test('thumbnail opens the image only; batch mode selects and card/title still opens details', () => {
  const code = card.slice(card.indexOf('const onCardClick ='), card.indexOf('// --- Hover popover ---'))
  for (const batchMode of [false, true]) {
    const events: unknown[][] = []
    const handlers = vm.runInNewContext(ts.transpile(code) + '; ({onCardClick,onThumbnailClick})', {
      props: { batchMode, selectedIds: new Set() }, emit: (...args: unknown[]) => events.push(args), dismissCardPopover() {},
    })
    handlers.onThumbnailClick({ id: 'image' })
    assert.equal(events[0][0], batchMode ? 'toggle-checkbox' : 'preview-image')
    assert.equal(events.length, 1)
    events.length = 0
    handlers.onCardClick({ id: 'image' })
    assert.equal(events[0][0], batchMode ? 'toggle-checkbox' : 'open')
  }
})

test('detail gallery collects only visible, loaded images from its own render root', () => {
  const img = (src: string, visible = true, loaded = true) => ({ src, currentSrc: src, complete: true, naturalWidth: loaded ? 100 : 0, getClientRects: () => visible ? [{}] : [] })
  const first = img('blob:first'), second = img('blob:second')
  const images = displayedImages({ querySelectorAll: () => [first, img('blob:hidden', false), img('bad', true, false), second] } as unknown as ParentNode)
  assert.deepEqual(images, [first, second])
})

test('image activation leaves ordinary document keys alone and keeps detail state untouched', () => {
  const source = readFileSync(new URL('../../../hooks/useRenderedImagePreview.ts', import.meta.url), 'utf8')
  const code = source.slice(source.indexOf('  function open('), source.indexOf('  function close('))
  class ImageElement {
    complete = true; naturalWidth = 100; src = 'blob:document'; currentSrc = this.src
    getClientRects() { return [{}] }
  }
  const image = new ImageElement()
  const root = { value: { querySelectorAll: () => [image] } }
  const visible = { value: false }, urls = { value: [] }, index = { value: -1 }
  const handlers = vm.runInNewContext(ts.transpile(code) + '; ({open,keydown})', { root, active: () => true, displayedImages, selector: 'img', HTMLImageElement: ImageElement, visible, urls, index })
  let blocked = 0
  const event = (target: unknown, key: string) => ({ target, key, preventDefault() { blocked++ }, stopPropagation() {} })
  handlers.keydown(event({}, 'Enter'))
  handlers.keydown(event(image, 'ArrowDown'))
  assert.equal(blocked, 0)
  handlers.keydown(event(image, 'Enter'))
  assert.equal(blocked, 1)
  assert.equal(visible.value, true)
  assert.equal(index.value, 0)
  assert.equal(urls.value[0], 'blob:document')
})

function gallery(t: any, initialIndex: number, filterOverrides: Record<string, any> = {}) {
  const source = readFileSync(new URL('./KnowledgeImageGallery.vue', import.meta.url), 'utf8')
  const { descriptor } = parse(source)
  const script = compileScript(descriptor, { id: 'image-gallery-test' }).content
  const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const rows = Array.from({ length: 52 }, (_, i) => ({ id: `image-${i}`, title: `Image ${i}`, file_type: 'png', file_hash: `hash-${i}` }))
  const queries: any[] = [], reads: string[] = [], revoked: string[] = []
  let mounted: () => Promise<void>, cleanup: () => void
  const exports: any = {}
  const scope = vue.effectScope()
  let state: any
  scope.run(() => {
    vm.runInNewContext(compiled, {
      exports, Blob, URL: { createObjectURL: () => `blob:test-${reads.length}`, revokeObjectURL: (url: string) => revoked.push(url) },
      require(name: string) {
        if (name === 'vue') return { ...vue, onMounted: (fn: any) => { mounted = fn }, onBeforeUnmount: (fn: any) => { cleanup = fn } }
        if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
        if (name === '@/api/knowledge-base') return {
          async listKnowledgeFiles(kb: string, query: any) { queries.push({ kb, ...query }); return { data: rows.slice((query.page - 1) * 50, query.page * 50), total: rows.length } },
          async previewKnowledgeFile(id: string) { reads.push(id); return new Blob(['image']) },
          async thumbnailKnowledgeFile() { return new Blob(['thumbnail']) },
        }
        if (name === '@/utils/knowledgeImages') return { knowledgeImageVersion }
        if (name === '@/utils/filePreview') return filePreview
        if (name.endsWith('.vue')) return { default: {} }
        return require(name)
      },
    })
    state = exports.default.setup({ kbId: 'kb', initial: rows[initialIndex], filters: { datasource_id: 'source-a', folder_path: '同名目录', keyword: 'Image', sort_by: 'file_name', ...filterOverrides }, canDownload: false }, { expose() {}, emit() {} })
  })
  t.after(() => { cleanup(); scope.stop() })
  return { state, mounted: () => mounted(), queries, reads, revoked }
}

test('opening a later card locates it without showing preceding unrelated images', async t => {
  const ctx = gallery(t, 51)
  assert.equal(ctx.state.current.value.id, 'image-51')
  await ctx.mounted()
  await vue.nextTick()
  assert.equal(ctx.state.current.value.id, 'image-51')
  assert.equal(ctx.queries.length, 2)
  for (const query of ctx.queries) {
    assert.equal(query.datasource_id, 'source-a')
    assert.equal(query.folder_path, '同名目录')
    assert.equal(query.keyword, 'Image')
    assert.equal(query.file_type, 'image')
  }
  assert.ok(ctx.reads.every(id => ['image-50', 'image-51'].includes(id)))
})

test('next at page boundary loads the next scoped page and original images stay bounded', async t => {
  const ctx = gallery(t, 49)
  await ctx.mounted()
  await vue.nextTick()
  await ctx.state.changeIndex(49, { trigger: 'next' })
  await vue.nextTick()
  assert.equal(ctx.state.current.value.id, 'image-50')
  assert.equal(ctx.queries[1].page, 2)
  assert.ok(ctx.reads.length < 8, 'all original files were fetched')
  assert.equal(ctx.state.images.value[50].download, false)
})

test('gallery intersects selected extensions with image-only scope on every page', async t => {
  const ctx = gallery(t, 51, {file_types:['png','pdf']})
  await ctx.mounted()
  for (const query of ctx.queries) {
    assert.equal(query.file_type,'image')
    assert.deepEqual([...query.file_types],['png','pdf'])
    assert.equal(query.datasource_id,'source-a')
  }
})
