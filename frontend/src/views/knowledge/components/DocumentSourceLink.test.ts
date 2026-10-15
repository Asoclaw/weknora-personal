import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import { compile } from '@vue/compiler-dom'
import { createSSRApp, h } from 'vue'
import { renderToString } from '@vue/server-renderer'
import ts from 'typescript'
import { resolveKnowledgeSourceLink } from '../../../utils/knowledgeSourceLink'

const require = createRequire(import.meta.url)
const source = readFileSync(new URL('./DocumentActionMenu.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id:'source-menu-test', inlineTemplate:true }).content
const compiled = ts.transpileModule(script, { compilerOptions:{ module:ts.ModuleKind.CommonJS, target:ts.ScriptTarget.ES2022 } }).outputText

async function renderMenu(item: any, canEditActions: boolean) {
 const exports: any = {}
 runInNewContext(compiled, { exports, require(name: string) {
  if (name === '@/utils/knowledgeSourceLink') return { resolveKnowledgeSourceLink }
  return require(name)
 } })
 const app = createSSRApp(exports.default, { item, canEditActions, canDownload:true, canMutateKnowledge:true, traceVisible:true })
 app.config.globalProperties.$t = (key: string) => key
 app.config.warnHandler = () => {}
 app.component('t-icon', { render: () => h('span') })
 app.component('t-popconfirm', { setup(_props: any, {slots}: any) { return () => h('div',slots.default?.()) } })
 return renderToString(app)
}

test('read-only menu opens original without exposing mutations', async () => {
 const html = await renderMenu({id:'one',type:'file',channel:'tencent_docs',metadata:{source_url:'https://saas.docs.qq.com/doc/one'}},false)
 assert.match(html, /href="https:\/\/saas.docs.qq.com\/doc\/one"/)
 assert.match(html, /target="_blank"/)
 assert.match(html, /rel="noopener noreferrer"/)
 assert.match(html, /knowledgeBase.openOriginalDocument/)
 assert.doesNotMatch(html, /deleteDocument|rebuildDocument|moveDocument|tagEdit|common.download/)
})

test('editor retains normal menu and URL imports keep their original navigation', async () => {
 const html = await renderMenu({id:'two',type:'url',source:'https://example.com/original'},true)
 assert.match(html,/href="https:\/\/example.com\/original"/)
 assert.match(html,/knowledgeBase.rebuildDocument/)
 assert.match(html,/knowledgeBase.deleteDocument/)
 for (const source_url of ['', 'javascript:alert(1)']) {
  const missing = await renderMenu({id:'three',channel:'tencent_docs',metadata:{source_url}},false)
  assert.doesNotMatch(missing, /href=|openOriginalDocument/)
 }
})

const detailSource = readFileSync(new URL('../../../components/doc-content.vue', import.meta.url), 'utf8').replace(/\r\n/g, '\n')
const detailStart = detailSource.indexOf('<div v-if="details.channel === \'tencent_docs\'"')
const detailEnd = detailSource.indexOf('\n          </div>\n        </section>', detailStart)
const detailTemplate = detailSource.slice(detailStart, detailEnd)
const detailRender = runInNewContext('(function(){'+compile(detailTemplate, { mode:'function' }).code+'})()', { Vue:require('vue') })

test('actual detail row displays link actions or the missing-link state', async () => {
 for (const url of ['https://saas.docs.qq.com/doc/one', '']) {
  const app = createSSRApp({ data:() => ({details:{channel:'tencent_docs'},originalSourceLink:url,copyOriginalSourceLink:()=>{}}),render:detailRender })
  app.config.globalProperties.$t = (key: string) => key
  app.config.warnHandler = () => {}
  app.component('t-button', { setup(_props: any,{slots}: any){ return () => h('button',slots.default?.()) } })
  const html = await renderToString(app)
  assert.match(html,/knowledgeBase.originalSourceLink/)
  if (url) {
   assert.match(html,/knowledgeBase.openOriginalDocument/)
   assert.match(html,/knowledgeBase.copySourceLink/)
   assert.match(html,/target="_blank" rel="noopener noreferrer"/)
  } else {
   assert.match(html,/knowledgeBase.sourceLinkUnavailable/)
   assert.doesNotMatch(html,/href=|copySourceLink|openOriginalDocument/)
  }
 }
})

test('actual copy action copies the original URL and handles clipboard failures', async () => {
 const copyScript = detailSource.match(/const copyOriginalSourceLink = async \(\) => \{[\s\S]*?\n\};/)![0]
 for (const scenario of ['success','failure','missing']) {
  const writes: string[] = [], notices: string[] = []
  await runInNewContext(copyScript+'; copyOriginalSourceLink()', {
   originalSourceLink:{value:scenario==='missing'?'':'https://saas.docs.qq.com/doc/one'},
   navigator:{clipboard:{async writeText(url: string){ writes.push(url); if(scenario==='failure') throw new Error('denied') }}},
   t:(key: string)=>key,
   MessagePlugin:{success:(value: string)=>notices.push(value),error:(value: string)=>notices.push(value)},
  })
  assert.deepEqual(writes,scenario==='missing'?[]:['https://saas.docs.qq.com/doc/one'])
  assert.deepEqual(notices,scenario==='missing'?[]:[scenario==='failure'?'knowledgeBase.copySourceLinkFailed':'common.copied'])
 }
})
