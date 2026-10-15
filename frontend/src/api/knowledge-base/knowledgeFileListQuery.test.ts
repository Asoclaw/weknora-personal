import assert from 'node:assert/strict';
import test from 'node:test';

import { buildListKnowledgeFilesQuery } from './knowledgeFileListQuery';

test('知识文件列表请求会传递排序字段和方向', () => {
  const query = new URLSearchParams(buildListKnowledgeFilesQuery({
    page: 2,
    page_size: 35,
    sort_by: 'file_name',
    sort_order: 'asc',
    folder_path: '',
  }));

  assert.equal(query.get('page'), '2');
  assert.equal(query.get('page_size'), '35');
  assert.equal(query.get('sort_by'), 'file_name');
  assert.equal(query.get('sort_order'), 'asc');
  assert.equal(query.get('folder_path'), '');
});

test('来源筛选与目录、分页、搜索共同传递，全部资料不限制目录', () => {
  const source = new URLSearchParams(buildListKnowledgeFilesQuery({page:2,page_size:10,datasource_id:'source-one',folder_path:'需求',folder_recursive:true,keyword:'审批'}));
  assert.equal(source.get('datasource_id'),'source-one');
  assert.equal(source.get('folder_recursive'),'true');
  assert.equal(source.get('keyword'),'审批');
  const other = new URLSearchParams(buildListKnowledgeFilesQuery({page:1,page_size:10,unassigned:true}));
  assert.equal(other.get('unassigned'),'true');
  assert.equal(other.has('folder_path'),false);
  assert.equal(other.has('datasource_id'),false);
});
