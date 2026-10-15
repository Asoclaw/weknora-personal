import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';
import ts from 'typescript';
import { ref, computed } from 'vue';

test('pointer drag clamps width, saves on release, and keyboard adjusts it', () => {
  const source = readFileSync(new URL('./ResizableFolderPane.vue', import.meta.url), 'utf8');
  const script = source.slice(source.indexOf('const key ='), source.indexOf('onMounted('));
  const storage = new Map();
  const state = vm.runInNewContext(ts.transpile(script) + '; ({ start, move, finish, keyboard, width, available, dragging })', {
    ref, computed, localStorage: { setItem: (key: string, value: string) => storage.set(key, value) },
  });
  let captured = 0;
  state.start({ isPrimary: true, button: 0, pointerId: 7, clientX: 220, currentTarget: { setPointerCapture: () => captured++ }, preventDefault() {} });
  state.move({ pointerId: 8, clientX: 600 });
  assert.equal(state.width.value, 220);
  state.move({ pointerId: 7, clientX: 500 });
  assert.equal(state.width.value, 500);
  assert.equal(captured, 1);
  assert.equal(storage.size, 0);
  state.move({ pointerId: 7, clientX: 2000 });
  assert.equal(state.width.value, 680);
  state.finish();
  assert.equal(state.dragging.value, false);
  assert.equal(storage.values().next().value, '680');
  state.available.value = 600;
  assert.equal(state.width.value, 280);
  state.keyboard({ key: 'Home', preventDefault() {} });
  assert.equal(state.width.value, 180);
  state.keyboard({ key: 'ArrowRight', preventDefault() {} });
  assert.equal(state.width.value, 200);
});
