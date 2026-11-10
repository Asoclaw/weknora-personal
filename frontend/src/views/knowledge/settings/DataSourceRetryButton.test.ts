import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { parse, compileScript } from '@vue/compiler-sfc'
import ts from 'typescript'
import { submittingRetries } from './failedSyncRetry'

const require = createRequire(import.meta.url)
const filename = new URL('./DataSourceRetryButton.vue', import.meta.url)
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename: filename.pathname })
const compiled = ts.transpileModule(compileScript(descriptor, { id: 'retry-button-test' }).content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function fixture(props: { dataSourceId: string; logId: string; busy: boolean }, request: () => Promise<unknown>) {
  const exports: any = {}
  const events: unknown[][] = [], errors: unknown[] = []
  runInNewContext(compiled, { exports, require: (name: string) => {
    if (name === 'vue') return require('vue')
    if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
    if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, error: (value: unknown) => errors.push(value) } }
    if (name === '@/api/datasource') return { retryFailedSync: request }
    if (name === './failedSyncRetry') return { submittingRetries }
    throw new Error(name)
  } })
  const state = exports.default.setup(props, { expose() {}, emit: (...event: unknown[]) => events.push(event) })
  return { state, events, errors }
}

test('both entry points share one request and publish the returned retry log', async () => {
  submittingRetries.clear()
  let calls = 0, resolve!: (value: unknown) => void
  const request = () => { calls++; return new Promise(done => { resolve = done }) }
  const card = fixture({ dataSourceId: 'ds', logId: 'failed', busy: false }, request)
  const drawer = fixture({ dataSourceId: 'ds', logId: 'failed', busy: false }, request)
  const pending = card.state.retry()
  await drawer.state.retry()
  assert.equal(calls, 1)
  resolve({ id: 'retry-log' })
  await pending
  assert.deepEqual(card.events, [['retried', 'retry-log']])
  assert.equal(submittingRetries.size, 0)
})

test('running sync cannot submit; network error releases the button', async () => {
  submittingRetries.clear()
  let calls = 0
  const props = { dataSourceId: 'ds', logId: 'failed', busy: true }
  const view = fixture(props, async () => { calls++; throw new Error('unavailable') })
  await view.state.retry()
  assert.equal(calls, 0)
  props.busy = false
  await view.state.retry()
  assert.equal(calls, 1)
  assert.equal(view.errors.length, 1)
  assert.equal(submittingRetries.size, 0)
})
