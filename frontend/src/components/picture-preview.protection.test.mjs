import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'

test('knowledge image overlays hide the raw-document link while legacy previews retain it', () => {
  const source = readFileSync(new URL('./picture-preview.vue', import.meta.url), 'utf8')
  const block = source.slice(source.indexOf('function markControls()'), source.indexOf('function keyboard('))
  for (const protectOriginal of [true, false]) {
    const attrs = {}
    const control = { style: {}, hidden: false, className: '', textContent: '', querySelector: selector => selector === '.t-icon-image' ? {} : null, setAttribute(name, value) { attrs[name] = value } }
    vm.runInNewContext(ts.transpile(block) + '; markControls()', { props: { protectOriginal }, previewRoot: () => ({ querySelectorAll: () => [control] }), t: key => key })
    assert.equal(control.hidden, protectOriginal)
    if (protectOriginal) assert.equal(control.style.display, 'none')
    else assert.equal(attrs.role, 'button')
  }
})
