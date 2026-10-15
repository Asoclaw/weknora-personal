import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import { baseParse, compile } from '@vue/compiler-dom'
import { renderToString } from '@vue/server-renderer'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'
import { createSSRApp } from 'vue'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./DataSourceEditorDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'datasource-editor-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function findPicker(node: any): any {
  if (node.tag === 'div' && node.props?.some((prop:any) => prop.name === 'class' && prop.value?.content === 'resource-picker__list')) return node
  for (const child of node.children || []) { const found = findPicker(child); if (found) return found }
}
const pickerTemplate = findPicker(baseParse(descriptor.template!.content, { isVoidTag: tag => ['input','img','br','hr','meta','link'].includes(tag) })).loc.source
const pickerRender = new Function('Vue', compile(pickerTemplate, { mode: 'function' }).code)(require('vue'))

async function renderPicker(vm:any) {
  const app = createSSRApp({render:() => pickerRender.call(vm, vm, [])})
  app.component('t-icon', {render:()=>h('i')})
  app.component('t-loading', {render:()=>h('i')})
  return renderToString(app)
}

test('real Tencent saved scope renders every parent half checked with only roots initially expanded', {skip: !process.env.SELECTION_LIVE_FIXTURE}, async () => {
  const live = JSON.parse(readFileSync(process.env.SELECTION_LIVE_FIXTURE!, 'utf8'))
  const source = {id:'live-read-only', name:'Tencent', type:'tencent_docs', credentials:{credentials:{configured:true}},config:{resource_ids:live.selected,settings:{site:'saas'}}}
  const f = await fixture({source,tree:live.tree,children:live.children,ancestors:live.ancestors})
  try {
    await f.vm.loadResources()
    const rootIds = new Set<string>(live.tree.map((n:any)=>n.external_id))
    for (const node of f.vm.resources) {
      if (f.vm.expandedResourceIds.has(node.external_id)) assert.ok(rootIds.has(node.external_id), 'only roots expanded')
    }
    const project = f.vm.resources.find((n:any)=>n.name==='格力FMS项目')
    assert.ok(project)
    assert.equal(f.vm.resourceRowState(project.external_id),'indeterminate')
    assert.equal(f.vm.expandedResourceIds.has(project.external_id),false)
    assert.equal(f.calls.some(c=>c.method==='listResources' && c.args[1] && !rootIds.has(c.args[1])),false, 'no automatic child-directory requests')
    let html = await renderPicker(f.vm)
    assert.ok(html.includes('格力FMS项目'))
    assert.ok(html.includes('resource-picker__check is-indeterminate'))
    for (const id of live.ancestors) {
      if (id==='root') continue
      await f.vm.ensureChildrenLoaded(id)
    }
    for (const id of live.ancestors) assert.equal(f.vm.resourceRowState(id), live.selected.includes(id)?'checked':'indeterminate', `all saved ancestors ${id}`)
    for (const id of live.selected) assert.equal(f.vm.resourceRowState(id),'checked', `all saved selections ${id}`)
    assert.equal(f.calls.some(c=>['updateDataSource','triggerSync'].includes(c.method)),false)
  } finally { f.close() }
})

test('Tencent file leaves can be selected independently and excluded from a selected folder', async () => {
  const f = await fixture()
  try {
    f.vm.form.type = 'tencent_docs'
    f.vm.resources = [
      { external_id: 'root', name: '我的文档', type: 'folder', has_children: true },
      { external_id: 'folder', parent_id: 'root', name: '项目', type: 'folder', has_children: true },
      { external_id: 'file:one', parent_id: 'folder', name: '需求', type: 'document', has_children: false },
      { external_id: 'file:two', parent_id: 'folder', name: '纪要', type: 'document', has_children: false },
    ]
    f.vm.expandedResourceIds = new Set(['root', 'folder'])
    await nextTick()
    assert.equal(f.vm.visibleTree.length, 4)
    assert.equal(f.vm.resourceIconName(f.vm.resources[2]), 'file')
    f.vm.toggleResource('file:one')
    assert.deepEqual([...f.vm.selectedResourceIds], ['file:one'])
    assert.equal(f.vm.resourceRowState('folder'), 'indeterminate')
    assert.equal(f.vm.resourceRowState('file:two'), 'unchecked')
    f.vm.selectedResourceIds = []
    f.vm.toggleResource('folder')
    assert.deepEqual([...f.vm.selectedResourceIds], ['folder'])
    f.vm.toggleResource('file:one')
    assert.deepEqual([...f.vm.selectedResourceIds], ['file:two'])
    assert.equal(f.vm.resourceRowState('file:one'), 'unchecked')
  } finally {
    f.close()
  }
})

async function fixture({ configured = true, create = false, mockTeams = [{id: 'auto-team', name: 'Team'}], teamError = '', source = null as any, tree = [] as any[], children = {} as Record<string, any[]>, ancestors = [] as string[], ancestryResult = null as Promise<any> | null } = {}) {
  const calls: Array<{ method: string; args: any[] }> = []
  let storedToken = configured ? 'expired-token' : ''
  const api = {
    async listResources(id: string, parent?: string) {
      calls.push({method: 'listResources', args: [id, parent]})
      return { data: parent ? (children[parent] || []) : tree }
    },
    async resolveResourceAncestors(id: string, ids: string[]) {
      calls.push({method: 'resolveResourceAncestors', args: [id, [...ids]]})
      return ancestryResult ? await ancestryResult : { data: { ancestors } }
    },
    async validateCredentials(type: string, credentials: Record<string, string>) {
      calls.push({ method: 'validateCredentials', args: [type, { ...credentials }] })
      if (type === 'mockplus') return { status: 'connected', teams: mockTeams, team_id: credentials.team_id || (mockTeams.length === 1 ? mockTeams[0].id : ''), team_discovery_error: teamError, resources: [
        { external_id: 'new-team-project', name: 'New project', type: 'document' },
      ] }
      if (credentials.access_token !== 'rotated-token') throw new Error('gitlab API /user: status 401')
    },
    async validateConnection(id: string) {
      calls.push({ method: 'validateConnection', args: [id] })
      if (storedToken !== 'rotated-token') throw new Error('gitlab API /user: status 401')
    },
    async updateDataSource(id: string, data: any) {
      calls.push({ method: 'updateDataSource', args: [id, JSON.parse(JSON.stringify(data))] })
      // The main update endpoint deliberately preserves stored credentials.
    },
    async putDataSourceCredentials(id: string, credentials: Record<string, string>) {
      calls.push({ method: 'putDataSourceCredentials', args: [id, { ...credentials }] })
      storedToken = credentials.access_token
    },
  }
  const props = reactive({
    visible: false, kbId: 'kb-one',
    dataSource: create ? null : source || {
      id: 'source-one', name: 'GitLab', type: 'gitlab',
      credentials: { credentials: { configured } },
      config: { resource_ids: [], settings: { projects: [{ project_id: '123', paths: [] }] } },
      sync_schedule: '0 0 */6 * * *', sync_mode: 'incremental',
      conflict_strategy: 'skip', sync_deletions: true,
    },
  })
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { warning() {}, success() {}, error() {} } }
      if (name === '@/api/datasource') return api
      return { default: {} }
    },
    URL, console,
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  app.mount({})
  props.visible = true
  await nextTick()
  const vm = instance.value
  async function replace(token = 'rotated-token') {
    vm.enterReplaceCredentials()
    vm.form.config.credentials = { base_url: 'https://gitlab.example.com', access_token: token }
    await nextTick()
  }
  return { vm, calls, replace, storedToken: () => storedToken, close: () => app.unmount() }
}

for (const type of ['mockplus', 'tencent_docs']) {
  test(`${type}: every ancestor is half checked before deepest selection is loaded; initial expansion stops at roots`, async () => {
    const nodes = [
      {external_id:'root',name:'我的文档',type:'folder',has_children:true},
      {external_id:'project',parent_id:'root',name:'格力FMS项目',type:'folder',has_children:true},
      {external_id:'phase',parent_id:'project',name:'03项目执行',type:'folder',has_children:true},
      {external_id:'solution',parent_id:'phase',name:'产品方案',type:'folder',has_children:true},
      {external_id:'chosen',parent_id:'solution',name:'账务',type:'folder',has_children:true},
      {external_id:'other',parent_id:'project',name:'未选分支',type:'folder',has_children:true},
    ]
    const source = {id:'source-one',name:type,type,credentials:{credentials:{configured:true}},config:{resource_ids:['chosen'],settings:{site:'saas'}},sync_mode:'incremental'}
    const f = await fixture({source,tree:type==='mockplus'?nodes:[nodes[0]],children:{root:[nodes[1]],project:[nodes[2],nodes[5]],phase:[nodes[3]],solution:[nodes[4]]},ancestors:['root','project','phase','solution']})
    try {
      await f.vm.loadResources()
      assert.deepEqual([...f.vm.expandedResourceIds], ['root'])
      assert.equal(f.vm.resourceRowState('root'),'indeterminate')
      assert.equal(f.vm.resourceRowState('project'),'indeterminate')
      assert.equal(f.vm.visibleTree.some((n:any)=>n.resource.external_id==='phase'),false)
      assert.equal(f.calls.some(c=>c.method==='listResources' && c.args[1]==='project'),false, 'first level must not be fetched automatically')
      const initialMarkup = await renderPicker(f.vm)
      assert.equal((initialMarkup.match(/class="resource-picker__check is-indeterminate"/g) || []).length, 2, 'root and collapsed project render half checked')
      await f.vm.ensureChildrenLoaded('project')
      f.vm.expandedResourceIds = new Set(['root','project'])
      assert.equal(f.vm.resourceRowState('phase'),'indeterminate', 'collapsed intermediate ancestor must not look unchecked')
      const markup = await renderPicker(f.vm)
      assert.equal((markup.match(/class="resource-picker__check is-indeterminate"/g) || []).length, 3, 'root, project and collapsed phase must render green minus checkbox classes')
      assert.equal(markup.includes('aria-expanded="false"'),true)
      assert.equal(f.vm.resourceRowState('other'),'unchecked', 'unrelated branch must not be half checked')
      assert.equal(f.vm.visibleTree.some((n:any)=>n.resource.external_id==='chosen'),false)
      assert.equal(f.calls.some(c=>c.method==='listResources' && ['phase','solution','chosen'].includes(c.args[1])),false)
      await f.vm.ensureChildrenLoaded('phase')
      f.vm.expandedResourceIds = new Set(['root','project','phase'])
      assert.equal(f.vm.resourceRowState('solution'),'indeterminate')
      assert.equal(f.vm.visibleTree.some((n:any)=>n.resource.external_id==='chosen'),false)
      await f.vm.ensureChildrenLoaded('solution')
      f.vm.expandedResourceIds = new Set(['root','project','phase','solution'])
      assert.equal(f.vm.resourceRowState('chosen'),'checked')
      for (const id of ['root','project','phase','solution']) assert.equal(f.vm.resourceRowState(id),'indeterminate')
      f.vm.expandedResourceIds = new Set(['root','project'])
      assert.equal(f.vm.resourceRowState('phase'),'indeterminate', 'collapse must not change checkbox state')
    } finally { f.close() }
  })
}

test('slow ancestry does not block initial root expansion; returned membership fixes collapsed parents', async () => {
  let resolve!: (result:any)=>void
  const pending = new Promise<any>(done=>{resolve=done})
  const root={external_id:'root',name:'Root',has_children:true}
  const project={external_id:'project',parent_id:'root',name:'Project',has_children:true}
  const phase={external_id:'phase',parent_id:'project',name:'Phase',has_children:true}
  const source={id:'source-one',name:'Tencent',type:'tencent_docs',credentials:{credentials:{configured:true}},config:{resource_ids:['chosen'],settings:{site:'saas'}}}
  const f=await fixture({source,tree:[root],children:{root:[project],project:[phase]},ancestryResult:pending})
  try {
    await f.vm.loadResources()
    assert.equal(f.vm.loadingResources,false)
    assert.deepEqual([...f.vm.expandedResourceIds],['root'])
    resolve({data:{ancestors:['root','project','phase']}})
    await new Promise<void>(done => setImmediate(done))
    await nextTick()
    for(const id of ['root','project']) assert.equal(f.vm.resourceRowState(id),'indeterminate')
    assert.equal(f.vm.expandedResourceIds.has('project'),false)
    await f.vm.ensureChildrenLoaded('project')
    assert.equal(f.vm.resourceRowState('phase'),'indeterminate')
    assert.equal(f.vm.expandedResourceIds.has('phase'),false)
  } finally { f.close() }
})

for (const type of ['mockplus', 'tencent_docs']) {
  test(`${type} restores saved deep selection and does not clear it on unchanged connection events`, async () => {
    const source = { id: 'source-one', name: type, type, credentials: {credentials: {configured: true}}, config: {resource_ids: ['leaf'], settings: {site: 'saas', base_url: 'https://example.test', team_id: 'team'}}, sync_mode: 'incremental' }
    const root = {external_id: 'root', name: 'Root', type: 'folder', has_children: true}
    const nested = {external_id: 'nested', parent_id: 'root', name: 'Nested', type: 'folder', has_children: true}
    const leaf = {external_id: 'leaf', parent_id: 'nested', name: 'Saved project', type: 'document'}
    const f = await fixture({source, tree: type === 'mockplus' ? [root,nested,leaf] : [root], children: {root: [nested], nested: [leaf]}, ancestors: ['root','nested']})
    try {
      assert.deepEqual([...f.vm.selectedResourceIds], ['leaf'])
      assert.equal(f.vm.selectedResourceCount, 1, 'unloaded selections must not display zero')
      if(type === 'mockplus') { f.vm.mockplusSettingsChanged(); f.vm.mockplusAccountSettingsChanged() }
      else f.vm.tencentSiteChanged('saas')
      assert.deepEqual([...f.vm.selectedResourceIds], ['leaf'])
      await f.vm.loadResources()
      assert.equal(f.vm.visibleTree.some((r: any) => r.resource.external_id === 'leaf'), false)
      assert.equal(f.vm.resourceRowState('root'), 'indeterminate')
      assert.equal(f.calls.filter(c => c.method === 'listResources').length, type === 'tencent_docs' ? 2 : 1)
      assert.equal(f.vm.expandedResourceIds.has('nested'), false)
      if(type === 'tencent_docs') { await f.vm.ensureChildrenLoaded('root'); f.vm.expandedResourceIds = new Set(['root']) }
      await f.vm.ensureChildrenLoaded('nested')
      f.vm.expandedResourceIds = new Set(['root','nested'])
      assert.equal(f.vm.visibleTree.some((r: any) => r.resource.external_id === 'leaf'), true)
      assert.equal(f.vm.resourceRowState('leaf'), 'checked')
      assert.equal(f.vm.resourceRowState('root'), 'indeterminate')
      assert.equal(f.calls.some(c => ['createDataSource','updateDataSource','triggerSync'].includes(c.method)), false)
      f.vm.form.config.settings.site = 'personal'
      if(type === 'tencent_docs') f.vm.tencentSiteChanged('personal')
      else { f.vm.form.config.settings.team_id = 'other'; f.vm.mockplusSettingsChanged() }
      assert.equal(f.vm.selectedResourceIds.length, 0, 'actual connection changes invalidate the old scope')
      assert.deepEqual(source.config.resource_ids, ['leaf'], 'editing must not mutate saved props')
      assert.equal(source.config.settings.team_id, 'team')
    } finally { f.close() }
  })
}

test('Mockplus requires generic connection parameters and page credentials', async () => {
  const f = await fixture({ create: true })
  try {
    f.vm.selectType({ type: 'mockplus', available: true })
    await nextTick()
    assert.equal(f.vm.form.sync_schedule, '0 0 2 * * *')
    assert.equal(f.vm.form.sync_deletions, false)
    assert.equal(f.vm.selectedResourceIds.length, 0)
    assert.equal(f.vm.displayedCredentialFields.length, 2)
    assert.equal(f.vm.form.config.settings.base_url, '')
    assert.equal(f.vm.form.config.settings.team_id, '')
    assert.equal(f.vm.form.config.settings.insecure_tls, false)
    await f.vm.testConnection()
    assert.equal(f.calls.length, 0)
    f.vm.form.config.settings = { base_url: 'https://mockplus.example.test', team_id: 'my-team', insecure_tls: false }
    await f.vm.testConnection()
    assert.equal(f.calls.length, 0)
    f.vm.form.config.credentials = { username: 'page-user', password: 'page-password' }
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    assert.equal(f.vm.credentialsConfigured, false, 'testing must not pretend credentials have been saved')
    assert.deepEqual(f.calls[0].args, ['mockplus', {
      username: 'page-user', password: 'page-password', base_url: 'https://mockplus.example.test', team_id: 'my-team', insecure_tls: false,
    }])
    assert.deepEqual(Object.keys(f.vm.buildConfigPayload().credentials), ['username', 'password'])
    f.vm.step = 2
    await f.vm.nextStep()
    assert.equal(f.vm.step, 2, 'empty selection must not imply the entire team')
    f.vm.selectedResourceIds = ['project-one']
    await f.vm.nextStep()
    assert.equal(f.vm.step, 3)
  } finally { f.close() }
})

test('Mockplus connection settings invalidate the tested connection and selection', async () => {
  const f = await fixture({ create: true })
  try {
    f.vm.selectType({ type: 'mockplus', available: true })
    await f.vm.testConnection()
    f.vm.selectedResourceIds = ['old-project']
    f.vm.mockplusSettingsChanged()
    assert.equal(f.vm.testResult, '')
    assert.equal(f.vm.selectedResourceIds.length, 0)
  } finally { f.close() }
})

test('Mockplus selects a single team after login without requiring its ID', async () => {
 const f=await fixture({create:true})
 try {f.vm.selectType({type:'mockplus',available:true});f.vm.form.config.settings.base_url='https://mockplus.example.test';f.vm.form.config.credentials={username:'page',password:'page-password'};await f.vm.testConnection();assert.equal(f.vm.form.config.settings.team_id,'auto-team');assert.equal(f.vm.mockplusTeams[0].name,'Team');assert.equal(f.vm.mockplusManualTeam,false)}finally{f.close()}
})
test('Mockplus multiple teams require selection and discovery failure offers manual fallback', async () => {
 for(const options of [{mockTeams:[{id:'one',name:'One'},{id:'two',name:'Two'}],teamError:''},{mockTeams:[],teamError:'unavailable'}]) {
 const f=await fixture({create:true,...options});try{f.vm.selectType({type:'mockplus',available:true});f.vm.form.config.settings.base_url='https://mockplus.example.test';f.vm.form.config.credentials={username:'page',password:'page-password'};await f.vm.testConnection();assert.equal(f.vm.form.config.settings.team_id,'');assert.equal(f.vm.mockplusManualTeam,!!options.teamError);f.vm.step=1;await f.vm.nextStep();assert.equal(f.vm.step,1)}finally{f.close()}
 }
})

test('Mockplus replacement previews the new connection without persisting credentials', async () => {
  const f = await fixture()
  try {
    f.vm.form.type = 'mockplus'
    f.vm.enterReplaceCredentials()
    f.vm.form.config.settings = { base_url: 'https://new-server.example.test', team_id: 'new-team', insecure_tls: false, auth_mode: 'password' }
    f.vm.form.config.credentials = { username: 'new-account', password: 'new-password' }
    await f.vm.testConnection()
    await f.vm.loadResources()
    assert.deepEqual(f.calls.map(call => call.method), ['validateCredentials'])
    assert.equal(f.vm.resources[0].external_id, 'new-team-project')
    await f.vm.commitCredentialsIfNeeded('source-one')
    assert.deepEqual(f.calls[1].args, ['source-one', {
      username: 'new-account', password: 'new-password', base_url: 'https://new-server.example.test', team_id: 'new-team', insecure_tls: false, auth_mode: 'password',
    }])
    assert.equal(f.vm.form.config.credentials.password, undefined)
  } finally { f.close() }
})

test('rotated GitLab credentials are tested without updating the saved data source', async () => {
  const f = await fixture()
  try {
    await f.replace()
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    assert.deepEqual(f.calls, [{ method: 'validateCredentials', args: ['gitlab', {
      base_url: 'https://gitlab.example.com', access_token: 'rotated-token',
    }] }])
    assert.equal(f.storedToken(), 'expired-token')
  } finally { f.close() }
})

test('Next tests the replacement and final save commits credentials before settings', async () => {
  const f = await fixture()
  try {
    await f.replace()
    await f.vm.nextStep()
    assert.equal(f.vm.step, 2)
    await f.vm.nextStep()
    assert.equal(f.vm.step, 3)
    await f.vm.handleSubmit()
    assert.deepEqual(f.calls.map(call => call.method), [
      'validateCredentials', 'putDataSourceCredentials', 'updateDataSource',
    ])
    assert.equal(f.storedToken(), 'rotated-token')
    assert.deepEqual(f.calls[2].args[1].config.credentials, {})
    assert.equal(f.calls[2].args[1].conflict_strategy, undefined, 'legacy conflict strategy must not be resubmitted')
  } finally { f.close() }
})

test('invalid replacement stays on the credentials step and can be corrected', async () => {
  const f = await fixture()
  try {
    await f.replace('invalid-token')
    await f.vm.nextStep()
    assert.equal(f.vm.step, 1)
    assert.equal(f.vm.testResult, 'error')
    assert.match(f.vm.testErrorMsg, /401/)
    assert.equal(f.storedToken(), 'expired-token')
    await f.replace()
    await f.vm.nextStep()
    assert.equal(f.vm.step, 2)
  } finally { f.close() }
})

test('testing unchanged credentials still validates the stored token', async () => {
  const f = await fixture()
  try {
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'error')
    assert.deepEqual(f.calls.map(call => call.method), ['updateDataSource', 'validateConnection'])
  } finally { f.close() }
})

test('an existing data source with no saved credentials tests the entered token', async () => {
  const f = await fixture({ configured: false })
  try {
    await f.replace()
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    assert.deepEqual(f.calls.map(call => call.method), ['validateCredentials'])
    assert.equal(f.storedToken(), '')
  } finally { f.close() }
})

test('new GitLab data sources continue to test credentials without persistence', async () => {
  const f = await fixture({ create: true })
  try {
    f.vm.selectType(f.vm.connectorDefs.find((def: any) => def.type === 'gitlab'))
    await f.replace()
    await f.vm.testConnection()
    assert.equal(f.vm.testResult, 'success')
    assert.deepEqual(f.calls.map(call => call.method), ['validateCredentials'])
  } finally { f.close() }
})
