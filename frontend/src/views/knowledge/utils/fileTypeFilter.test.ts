import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { FILE_TYPE_FILTER_EXTENSIONS } from './fileTypeFilter'
import { buildListKnowledgeFilesQuery } from '../../../api/knowledge-base/knowledgeFileListQuery'

test('specific file options cover supported imports and images without virtual types',()=>{
  const backend=readFileSync(new URL('../../../../../internal/application/service/knowledge_util.go',import.meta.url),'utf8')
  const section=backend.split('var supportedImportFileExtensions')[1].split('var dataTableFileExtensions')[0]
  const supported=[...section.matchAll(/"([a-z0-9]+)"\s*:/g)].map(m=>m[1])
  for(const ext of supported) assert.ok(FILE_TYPE_FILTER_EXTENSIONS.includes(ext as any),ext)
  for(const ext of ['png','jpg','jpeg','gif','webp','bmp','tiff','svg']) assert.ok(FILE_TYPE_FILTER_EXTENSIONS.includes(ext as any))
  for(const ext of ['image','manual','url']) assert.equal(FILE_TYPE_FILTER_EXTENSIONS.includes(ext as any),false)
})
test('multi-type query coexists with source scope and image-only restriction',()=>{
  const query=new URLSearchParams(buildListKnowledgeFilesQuery({page:1,page_size:20,file_types:['png','pdf'],file_type:'image',datasource_id:'source',folder_path:'folder',folder_recursive:true,keyword:'sample'}))
  assert.equal(query.get('file_types'),'png,pdf');assert.equal(query.get('file_type'),'image');assert.equal(query.get('datasource_id'),'source')
  assert.equal(new URLSearchParams(buildListKnowledgeFilesQuery({page:1,page_size:20,file_types:[]})).has('file_types'),false)
})
test('real filter control is multiple and folders checkbox spans the row without wrapping',()=>{
  const source=readFileSync(new URL('../KnowledgeBase.vue',import.meta.url),'utf8')
  assert.match(source, /v-model="selectedFileTypes"[^>]*multiple[^>]*filterable[^>]*clearable/)
  assert.match(source,/doc-filter-field doc-filter-field--folders/)
  assert.match(source,/\.doc-filter-field--folders[\s\S]*?white-space: nowrap/)
  assert.match(source,/selectedFileTypes\.value\.length > 0/)
})
