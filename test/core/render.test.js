const { test } = require('node:test'); const assert = require('node:assert/strict');
const { renderString } = require('../../src/core/render');
test('substitutes only declared vars', () => {
  const out = renderString('image: ${SAI_WORKER_IMAGE}\nother: ${RUNTIME_VAR}', { SAI_WORKER_IMAGE: 'r/sai:abc' });
  assert.match(out, /image: r\/sai:abc/);
  assert.match(out, /other: \$\{RUNTIME_VAR\}/);
});
test('substitutes the same var in multiple places', () => {
  assert.equal(renderString('a: ${X}\nb: ${X}', { X: 'v' }), 'a: v\nb: v');
});
