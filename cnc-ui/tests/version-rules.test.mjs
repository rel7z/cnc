import { test } from 'node:test';
import assert from 'node:assert/strict';
import { componentOf, componentTypes, normalizeRules, normalizeVersion } from '../lib/version-rules.ts';
import { dashboardAuditSignature, boundedLaunchBody } from '../lib/enum-audit.ts';
import { createHash, createHmac } from 'node:crypto';

const rule = (id, clauses = [{ operator: '=', version: 'v6.4' }]) => ({ id, name: id, target: 'wordpress:core', identifier: '', group: '', clauses });
test('supported component types produce complete API identities', () => {
  for (const type of componentTypes) {
    const draft = { ...rule(type), target: type, identifier: 'example', group: 'system' };
    const expected = (type === 'php:php-src' || type.endsWith(':core')) ? type : type === 'joomla:plugin' ? type + ':system:example' : type + ':example';
    assert.equal(componentOf(draft), expected);
    assert.equal(normalizeRules([draft])[0].component, expected);
  }
  for (const [target, identifier] of [['joomla:component', 'com_example'], ['joomla:module', 'mod_example'], ['wordpress:plugin', 'Uppercase'], ['joomla:package', '../unsafe']]) assert.throws(() => componentOf({ ...rule('bad'), target, identifier }));
});
test('version grammar and normalized duplicate detection match server contracts', () => {
  assert.equal(normalizeVersion('v6'), '6.0.0');
  assert.equal(normalizeVersion('999999999999999999999999999999.2'), '999999999999999999999999999999.2.0');
  for (const version of ['3.1.8.10','3.8.4.9','6.5.0.1','2.9.99.5','3.5.1.35','2.6.8c']) assert.equal(normalizeVersion(version),version);
  assert.equal(normalizeVersion('3.1.8.0'),'3.1.8');
  for (const value of ['06.4', '6.4.0+build', '6.4-beta', '6.4.0-01', '6.*', 'trunk', ' 6.4']) assert.throws(() => normalizeVersion(value));
  assert.throws(() => normalizeRules([rule('a'), rule('b', [{ operator: '=', version: '6.4.0' }])]));
  assert.deepEqual(normalizeRules([rule('a')])[0].clauses, [{ operator: '=', version: '6.4.0' }]);
  assert.throws(() => normalizeRules([{ ...rule('a'), name: '' }]));
  assert.throws(() => normalizeRules([{ ...rule('a'), clauses: [] }]));
});
test('AND ranges handle exclusive boundaries, prerelease successors, and minimum version', () => {
  for (const clauses of [[['>=', '6.4'], ['<', '6.4']], [['>', '1.0.0-alpha'], ['<', '1.0.0-alpha.0']], [['>', '1.0.0.9'], ['<', '1.0.0.10-0']], [['<', '0.0.0-0']]]) assert.throws(() => normalizeRules([rule('a', clauses.map(([operator, version]) => ({ operator, version })))]));
  const clauses = [{ operator: '>=', version: '6.4' }, { operator: '<', version: '6.5' }];
  assert.equal(normalizeRules([rule('a', clauses)]).length, 1);
});
test('audit signature binds actor, exact body, timestamp, method, and API route', async () => {
  const secret = 'fixture-secret-with-at-least-32-bytes', body = Buffer.from('{"tool_id":"enum"}'), actor = 'owner', timestamp = '1700000000000';
  const digest = createHash('sha256').update(body).digest('hex');
  const expected = createHmac('sha256', secret).update([actor, timestamp, 'POST', '/api/tools/launch', digest].join('\n')).digest('hex');
  assert.equal(dashboardAuditSignature(secret, actor, timestamp, body), expected);
  assert.notEqual(dashboardAuditSignature(secret, 'attacker', timestamp, body), expected);
  assert.notEqual(dashboardAuditSignature(secret, actor, timestamp, Buffer.from('{}')), expected);
  assert.deepEqual(Buffer.from(await boundedLaunchBody(new Request('http://localhost/api/tools/launch', { method: 'POST', body }))), body);
  await assert.rejects(boundedLaunchBody(new Request('http://localhost/api/tools/launch', { method: 'POST', body: Buffer.alloc(8 * 1024 * 1024 + 1) })));
});

test('friendly version checks preserve the backend contract for every comparison and component', async () => {
  const { newVersionCheck, comparisonChoices, clausesForComparison, comparisonOf, normalizeVersionChecks, versionCheckSummary, validateVersionChecks } = await import('../lib/version-rules.ts');
  const draft = { ...newVersionCheck('plugin'), identifier: 'elementor', clauses: [{ operator: '=', version: '3.25' }] };
  assert.equal(draft.target, 'wordpress:plugin');
  for (const choice of comparisonChoices) {
    const clauses = clausesForComparison(draft, choice.value);
    if (choice.value === 'between') clauses[1].version = '3.26';
    const check = { ...draft, clauses };
    const normalized = normalizeVersionChecks([check])[0];
    assert.equal(normalized.component, 'wordpress:plugin:elementor');
    assert.equal(comparisonOf(check), choice.value);
    assert.equal(normalized.clauses.length, choice.value === 'between' ? 2 : 1);
    assert.equal(normalized.name, versionCheckSummary(check));
    if (choice.value === 'any') assert.deepEqual(normalized.clauses, [{ operator: '>=', version: '0.0.0-0' }]);
    else if (choice.value === 'between') assert.deepEqual(normalized.clauses, [{ operator: '<=', version: '3.26.0' }, { operator: '>=', version: '3.25.0' }]);
    else assert.deepEqual(normalized.clauses, [{ operator: choice.value, version: '3.25.0' }]);
  }
  for (const target of componentTypes) assert.equal(normalizeVersionChecks([{ ...draft, target, group: 'system' }])[0].component, componentOf({ ...draft, target, group: 'system' }));
  assert.deepEqual(normalizeVersionChecks([]), []);
  assert.equal(normalizeVersionChecks([{ ...draft, name: 'Custom label' }])[0].name, 'Custom label');
  const custom = { ...draft, clauses: [{ operator: '>=', version: '3.25' }, { operator: '<', version: '3.26' }] };
  assert.equal(comparisonOf(custom), 'custom');
  assert.deepEqual(clausesForComparison(custom, 'custom'), custom.clauses);
  assert.match(versionCheckSummary(custom), /at least 3.25 and older than 3.26/);
  assert.ok(validateVersionChecks([newVersionCheck('empty')])[0].identifier);
  assert.ok(validateVersionChecks([newVersionCheck('empty')])[0]['version-0']);
  assert.ok(validateVersionChecks([{ ...draft, target: 'joomla:plugin' }])[0].group);
  assert.ok(validateVersionChecks([{ ...draft, clauses: [{ operator: '>=', version: '4' }, { operator: '<=', version: '3' }] }])[0].conditions);
  assert.ok(validateVersionChecks([draft, { ...draft, id: 'duplicate', clauses: [{ operator: '=', version: '3.25.0' }] }])[1].duplicate);
  assert.ok(validateVersionChecks([{ ...draft, name: 'Same' }, { ...draft, id: 'other', name: 'Same', identifier: 'other' }])[1].name);
  assert.throws(() => normalizeVersionChecks([{ ...draft, clauses: [{ operator: '=', version: '3.*' }] }]), /Version check 1/);
});

test('adding checks works without secure-context randomUUID and avoids collisions', async () => {
  const { createVersionCheckID } = await import('../lib/version-rules.ts');
  const first = createVersionCheckID([], {});
  const second = createVersionCheckID([{ id: first }], {});
  assert.match(first, /^rule-[a-z0-9-]+$/);
  assert.notEqual(first, second);
  assert.notEqual(createVersionCheckID([], null), first);
  assert.match(createVersionCheckID([], { randomUUID() { throw new Error('unavailable'); } }), /^rule-[a-z0-9-]+$/);
  assert.equal(createVersionCheckID([], { randomUUID: () => 'fixture' }), 'rule-fixture');
  assert.notEqual(createVersionCheckID([{ id: 'rule-fixture' }], { randomUUID: () => 'fixture' }), 'rule-fixture');
  const ids = new Set(Array.from({ length: 1000 }, () => createVersionCheckID([], {})));
  assert.equal(ids.size, 1000);
});
