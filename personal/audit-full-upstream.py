"""Compare personal source with the complete published v0.8.2 tree."""
import json
import re
import subprocess
import sys
from pathlib import Path

repo = Path(sys.argv[1]).resolve()
def git(*args):
    return subprocess.check_output(['git', '-C', str(repo), *args], encoding='utf-8').strip()
def upstream(path):
    return git('show', 'v0.8.2:' + path)
def current(path):
    return (repo / path).read_text(encoding='utf-8')

protected = ['internal', 'config', 'migrations', 'docreader', 'mcp-server', 'cli', 'client']
allowed = {'internal/container/container.go', 'internal/datasource/connector.go', 'internal/types/datasource.go'}
changes = git('diff', '--name-only', 'v0.8.2', '--', *protected).splitlines()
unexpected = [p for p in changes if p not in allowed and not p.startswith('internal/datasource/connector/tencentdocs/')]
assert not unexpected, 'Unexpected upstream implementation changes: ' + ', '.join(unexpected)
for line in git('diff', '--numstat', 'v0.8.2', '--', *sorted(allowed)).splitlines():
    added, removed, path = line.split('\t')
    assert removed == '0', 'Removed backend code: ' + path
assert not git('diff', '--name-only', '--diff-filter=D', 'v0.8.2'), 'Upstream files were deleted'

checks = {}
for name, path, pattern in [
    ('settings', 'frontend/src/views/settings/Settings.vue', r"key: '([^']+)'"),
    ('knowledge_settings', 'frontend/src/views/knowledge/KnowledgeBaseEditorModal.vue', r"key: '([^']+)'"),
    ('data_sources', 'frontend/src/views/knowledge/settings/DataSourceEditorDialog.vue', r"type: '([^']+)'"),
]:
    original = set(re.findall(pattern, upstream(path)))
    local = set(re.findall(pattern, current(path)))
    assert original <= local, name + ' declarations lost: ' + repr(original - local)
    checks[name] = {'upstream': len(original), 'current': len(local)}
for path in ['frontend/src/router/index.ts', 'frontend/src/config/toolbox.ts', 'frontend/src/config/integrations.ts', 'frontend/src/config/settingsAccess.ts']:
    assert not git('diff', '--name-only', 'v0.8.2', '--', path), 'Changed upstream access/route definition: ' + path
assert "personalHiddenSections = new Set(['tenant', 'members'])" in current('frontend/src/views/settings/Settings.vue')
assert "item.path === 'organizations'" in current('frontend/src/stores/menu.ts')
assert "command.id === 'open-product-tour'" not in current('frontend/src/components/GlobalCommandPalette.vue').split('return cmds.filter')[1].split("if (command.id === 'open-agents')")[0]
assert 'v-if="!personalMode"' not in current('frontend/src/components/ContextualGuide.vue')
assert "if (personalMode) return" not in current('frontend/src/components/ContextualGuide.vue')
assert '<NewUserGuide />' in current('frontend/src/views/platform/index.vue')
result = {
    'baseline': git('rev-parse', 'v0.8.2^{commit}'),
    'protected_upstream_files': len(git('ls-tree', '-r', '--name-only', 'v0.8.2', '--', *protected).splitlines()),
    'backend_changes': 'Tencent Docs additions only',
    'removed_upstream_files': 0,
    'declarations': checks,
    'routes_toolbox_integrations_permissions': 'identical to upstream',
    'personal_guides': 'restored',
}
print(json.dumps(result, ensure_ascii=False, indent=2))
