import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import ts from 'typescript';
import * as vue from 'vue';
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc';
import { directFolderContents, shouldShowFolderContents } from './folderContents';
import { isFilteringDocuments } from './folderTree';
import type { KnowledgeFolderTree } from '@/api/knowledge-base/index';

const source = (name: string): KnowledgeFolderTree => ({ root_document_count: 0, total_document_count: 8, folders: [{name, path: name, document_count: 0, total_count: 8, children: [
  {name: 'UI',path: name+'/UI',document_count: 4,total_count: 4},
  {name: 'UX',path: name+'/UX',document_count: 0,total_count: 4,children:[{name:'深层',path:name+'/UX/深层',document_count:4,total_count:4}]},
]}] });
const tree: KnowledgeFolderTree = { ...source('ignored'), source_groups: [
  { id:'a',name:'腾讯文档',type:'tencent_docs',removed:false,tree:source('同名') },
  { id:'b',name:'摹客',type:'mockplus',removed:false,tree:source('同名') },
  { id:'',name:'',type:'',removed:false,unassigned:true,tree:source('其他') },
] };

test('source roots, direct children and leaf/empty nodes stay scoped', () => {
  assert.deepEqual(directFolderContents(tree,null,'').map(x=>x.group), ['a','b','__unassigned__']);
  assert.equal(directFolderContents(tree,'a','')[0].path,'同名');
  const a=directFolderContents(tree,'a','同名'),b=directFolderContents(tree,'b','同名');
  assert.deepEqual(a.map(x=>x.name),['UI','UX']);
  assert.deepEqual(a.map(x=>x.totalCount),[4,4]);
  assert.notEqual(a[0].key,b[0].key);
  assert.equal(a[0].group,'a');
  assert.equal(b[0].group,'b');
  assert.deepEqual(directFolderContents(tree,'a','同名/UI'),[]);
  assert.equal(directFolderContents(tree,'a','同名/UX')[0].name,'深层');
  assert.deepEqual(directFolderContents(tree,'missing','同名'),[]);
  assert.deepEqual(directFolderContents(null,null,''),[]);
  assert.equal(directFolderContents(source('普通'),null,'普通').length,2);
});

test('display setting never changes the document filtering predicate', () => {
  assert.equal(shouldShowFolderContents(true,false),true);
  assert.equal(shouldShowFolderContents(false,false),false);
  for (const filter of [{keyword:'UI'},{fileType:'png'},{parseStatus:'pending'},{source:'mockplus'},{tagIds:['tag']},{timeRange:['2026-10-01']}]) {
    assert.equal(shouldShowFolderContents(true,isFilteringDocuments(filter)),false);
  }
});

test('actual folder component renders navigable icons in both views without document selection', () => {
  const input=readFileSync(new URL('./components/FolderNavigationItems.vue',import.meta.url),'utf8');
  const {descriptor}=parse(input);
  const compiled=compileScript(descriptor,{id:'folders'});
  const exports:any={};
  vm.runInNewContext(ts.transpileModule(compiled.content,{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText,{
    exports,require:(name:string)=>name==='vue'?vue:name==='vue-i18n'?{useI18n:()=>({t:(key:string,{count}:any)=>`${count} documents`})}:{getDatasourceIconUrl:()=>undefined},
  });
  const render=compileTemplate({source:descriptor.template!.content,filename:'FolderNavigationItems.vue',id:'folders',compilerOptions:{bindingMetadata:compiled.bindings}});
  const renderExports:any={};
  vm.runInNewContext(ts.transpileModule(render.code,{compilerOptions:{module:ts.ModuleKind.CommonJS}}).outputText,{exports:renderExports,require:()=>({...vue,resolveComponent:(name:string)=>name})});
  for (const mode of ['grid','list']) {
    const events:any[]=[];
    const props={items:directFolderContents(tree,'a','同名'),mode};
    const setup=exports.default.setup(props,{expose(){},emit:(...args:any[])=>events.push(args)});
    const vnode=renderExports.render({},[],props,setup,{},{});
    const all:any[]=[];
    const visit=(node:any)=>{ if(!node||typeof node!=='object')return;all.push(node);if(Array.isArray(node.children))node.children.forEach(visit); };
    visit(vnode);
    const buttons=all.filter(x=>x.type==='button');
    assert.equal(buttons.length,2);
    assert.ok(all.some(x=>x.props?.name==='folder'));
    assert.ok(!all.some(x=>x.props?.['data-select-id']));
    buttons[0].props.onClick({target:{tagName:'SVG'}});
    assert.equal(events.length,1);
    assert.equal(events[0][0],'navigate');
    assert.equal(events[0][1].path,'同名/UI');
    assert.equal(events[0][1].group,'a');
  }
});
