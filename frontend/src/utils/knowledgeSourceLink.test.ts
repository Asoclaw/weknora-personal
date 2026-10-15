import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveKnowledgeSourceLink } from './knowledgeSourceLink'

test('Tencent file metadata and legacy URL imports resolve to their original links', () => {
 const url = 'https://saas.docs.qq.com/doc/example'
 for (const metadata of [{ source_url: url }, JSON.stringify({ source_url: url })]) {
  assert.equal(resolveKnowledgeSourceLink({ type:'file', channel:'tencent_docs', source:'file', metadata }), url)
 }
 assert.equal(resolveKnowledgeSourceLink({ type:'url', source:url }),url)
 assert.equal(resolveKnowledgeSourceLink({ type:'file', source:url }), '')
})

test('missing, malformed, unsafe or authenticated links have no navigation target', () => {
 for (const source_url of [undefined, 42, 'javascript:alert(1)', '/files/x', '//example.com/x', 'https://u:p@example.com/x', 'https://', '']) {
  assert.equal(resolveKnowledgeSourceLink({ channel:'tencent_docs', metadata:{source_url} }), '')
 }
 assert.equal(resolveKnowledgeSourceLink({ channel:'tencent_docs', metadata:'{broken' }), '')
 assert.equal(resolveKnowledgeSourceLink({ channel:'tencent_docs', source:'https://example.com' }), '')
})
