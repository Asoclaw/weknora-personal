import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import { baseParse, compile } from '@vue/compiler-dom'
import { renderToString } from '@vue/server-renderer'
import { createSSRApp } from 'vue'
import ts from 'typescript'
import { buildFolderRows } from '../folderTree'

const require = createRequire(import.meta.url)
const source = readFileSync(new URL('./KbFolderTree.vue', import.meta.url), 'utf8')
const descriptor = parse(source).descriptor
function findCount(node: any): any {
  if (node.tag === 'span' && node.props?.some((p:any) => p.name === 'class' && p.value?.content === 'kb-folder-row__count')) return node
  for (const child of node.children || []) { const found = findCount(child); if (found) return found }
}
const countTemplate = findCount(baseParse(descriptor.template!.content, { isVoidTag: tag => ['input','img','br'].includes(tag) })).loc.source
const countRender = runInNewContext('(function(){'+compile(countTemplate, {mode:'function',prefixIdentifiers:true}).code+'})()', {Vue:require('vue')})
async function countHtml(row:any, embedded=false) {
  const app=createSSRApp({data:()=>({row,embedded,t:(_key:string,params:any)=>`直属文件：${params.direct}`}),render:countRender})
  return renderToString(app)
}

const folders=[{path:'parent',name:'parent',document_count:0,total_count:7,children:[
  {path:'parent/branch',name:'branch',document_count:2,total_count:7,children:[{path:'parent/branch/leaf',name:'leaf',document_count:5,total_count:5}]},
  {path:'parent/empty',name:'empty',document_count:0,total_count:0},
]}]
const tree={folders,root_document_count:1,total_document_count:8}

test('actual folder count template shows subtree total and only direct count in tooltip',async()=>{
  const rows=buildFolderRows(tree,new Set(['','parent','parent/branch']))
  for(const [path,total,direct] of [['',8,1],['parent',7,0],['parent/branch',7,2],['parent/branch/leaf',5,5],['parent/empty',0,0]] as const){
    const row=rows.find(r=>r.path===path)!
    assert.ok(row)
    const html=await countHtml(row)
    assert.match(html,new RegExp(`title="直属文件：${direct}"`))
    assert.match(html,new RegExp(`>${total}</span>`))
  }
  const folded=buildFolderRows(tree,new Set(['']))
  assert.equal(await countHtml(folded.find(r=>r.path==='parent')),await countHtml(rows.find(r=>r.path==='parent')))
})

test('virtual source and all-files rows display totals without a direct-count tooltip',async()=>{
  for(const total of [0,8,6339]){
    const html=await countHtml({kind:'root',documentCount:0,totalCount:total},true)
    assert.match(html,new RegExp(`>${total}</span>`))
    assert.doesNotMatch(html,/title=|直属文件/)
  }
  const realFolder=await countHtml({kind:'folder',documentCount:0,totalCount:7},true)
  assert.match(realFolder,/title="直属文件：0"/)
  assert.match(realFolder,/>7<\/span>/)
})

test('source-group wrapper preserves real direct counters and keeps source totals independent',()=>{
  const file=readFileSync(new URL('./SourceGroupedFolderTree.vue',import.meta.url),'utf8')
  const parsed=parse(file).descriptor
  const script=compileScript(parsed,{id:'folder-count-group-test'}).content
  const compiled=ts.transpileModule(script,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
  const exports:any={}
  runInNewContext(compiled,{exports,require:(name:string)=>name==='vue'?require('vue'):name==='vue-i18n'?{useI18n:()=>({t:(key:string)=>key})}:{default:{}},console})
  const groups=[
    {id:'tencent',name:'腾讯文档',tree:{...tree,root_document_count:1,total_document_count:8}},
    {id:'mockplus',name:'摹客',tree:{...tree,root_document_count:0,total_document_count:6339}},
    {unassigned:true,tree:{folders:[],root_document_count:2,total_document_count:2}},
  ]
  const result=exports.default.setup({tree:{folders:[],root_document_count:3,total_document_count:6349,source_groups:groups},selectedGroup:null,selectedPath:'',canEdit:false},{expose:()=>{},emit:()=>{}})
  assert.equal(result.allTree.value.root_document_count,3)
  assert.equal(result.allTree.value.total_document_count,6349)
  assert.match(parsed.template!.content,/:tree="group\.tree"/)
  assert.equal(result.groupKey(groups[0]),'tencent')
  assert.equal(result.groupKey(groups[1]),'mockplus')
  assert.equal(result.groupKey(groups[2]),'__unassigned__')
  assert.deepEqual(groups.map(g=>[g.tree.root_document_count,g.tree.total_document_count]),[[1,8],[0,6339],[2,2]])
})
