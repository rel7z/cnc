import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import ts from 'typescript';
import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import * as rules from '../lib/version-rules.ts';

const require = createRequire(import.meta.url);
const source = readFileSync(new URL('../components/dashboard/VersionRuleEditor.tsx', import.meta.url), 'utf8');
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } }).outputText;
const module = { exports: {} };
new Function('require', 'module', 'exports', compiled)(name => name === '@/lib/version-rules' ? rules : require(name), module, module.exports);
const { VersionRuleEditor } = module.exports;

test('version-check form has no target-scope controls', () => {
  const html = renderToStaticMarkup(VersionRuleEditor({ rules: [], onRules: () => {} }));
  assert.match(html, /Version checks/);
  assert.doesNotMatch(html, /target scope|Exact hosts|CIDRs|Allowed ports|I am authorized/i);
});

test('friendly checks render names, boundaries, Joomla groups and accessible field errors', () => {
  const scope = { enabled: false, hosts: '', cidrs: '', schemes: ['http'], ports: '80', acknowledged: false };
  const render = rule => renderToStaticMarkup(VersionRuleEditor({ rules: [rule], onRules: () => {}, scope, onScope: () => {} }));
  const draft = { ...rules.newVersionCheck('friendly'), identifier: 'elementor', clauses: [{ operator: '<', version: '3.25.0' }] };
  let html = render(draft);
  assert.match(html, /Version checks/); assert.match(html, /Add version check/);
  assert.match(html, /Plugin folder name/); assert.match(html, /WordPress plugin elementor, older than 3.25.0/);
  assert.match(html, /<summary[^>]*>Advanced<\/summary>/);
  html = render({ ...draft, target: 'wordpress:core' });
  assert.doesNotMatch(html, /aria-label="Check 1 component name"/);
  html = render({ ...draft, clauses: [{ operator: '>=', version: '3' }, { operator: '<=', version: '4' }] });
  assert.match(html, /Minimum version/); assert.match(html, /Maximum version/); assert.match(html, /Both boundary versions are included/);
  html = render({ ...draft, target: 'joomla:plugin', group: '' });
  assert.match(html, /Plugin group/); assert.match(html, /Enter the plugin group/);
  html = render({ ...draft, clauses: [{ operator: '>=', version: '3' }, { operator: '<', version: '4' }] });
  assert.match(html, /Custom conditions \(advanced\)/); assert.match(html, /<details[^>]*open/);
  html = render(rules.newVersionCheck('missing'));
  assert.match(html, /aria-invalid="true"/); assert.match(html, /Enter the installed folder name/); assert.match(html, /Enter a version/);
});

test('Add version check handler works when the browser has no randomUUID', () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'crypto');
  Object.defineProperty(globalThis, 'crypto', { configurable: true, value: {} });
  try {
    let checks = [];
    const scope = { enabled: false, hosts: '', cidrs: '', schemes: ['http'], ports: '80', acknowledged: false };
    function findAdd(element) {
      if (!element || typeof element !== 'object') return;
      if (element.type === 'button' && element.props.children === 'Add version check') return element;
      for (const child of React.Children.toArray(element.props?.children)) { const found = findAdd(child); if (found) return found; }
    }
    for (let i = 0; i < 3; i++) {
      const tree = VersionRuleEditor({ rules: checks, onRules: next => { checks = next; }, scope, onScope: () => {} });
      findAdd(tree).props.onClick();
    }
    assert.equal(checks.length, 3);
    assert.equal(new Set(checks.map(check => check.id)).size, 3);
    assert.ok(checks.every(check => check.target === 'wordpress:plugin'));
  } finally {
    if (previous) Object.defineProperty(globalThis, 'crypto', previous);
    else delete globalThis.crypto;
  }
});

test('WordPress core shortcut submits a valid inventory rule and allows version filtering', () => {
  let checks = [];
  const tree = VersionRuleEditor({ rules: checks, onRules: next => { checks = next; } });
  function find(element, label) {
    if (!element || typeof element !== 'object') return;
    if (element.type === 'button' && element.props.children === label) return element;
    for (const child of React.Children.toArray(element.props?.children)) { const found = find(child, label); if (found) return found; }
  }
  find(tree, 'Scan WordPress core versions').props.onClick();
  assert.equal(checks.length, 1);
  const normalized = rules.normalizeVersionChecks(checks)[0];
  assert.equal(normalized.component, 'wordpress:core');
  assert.deepEqual(normalized.clauses, [{ operator: '>=', version: '0.0.0-0' }]);
  const html = renderToStaticMarkup(VersionRuleEditor({ rules: checks, onRules: () => {} }));
  assert.match(html, /WordPress core, any detected version/);
  assert.doesNotMatch(html, /aria-label="Check 1 version"/);
  const filtered = { ...checks[0], clauses: rules.clausesForComparison(checks[0], '<') };
  filtered.clauses[0].version = '6.8';
  assert.deepEqual(rules.normalizeVersionChecks([filtered])[0].clauses, [{ operator: '<', version: '6.8.0' }]);
});

test('PHP runtime check does not ask for a CMS plugin name and normalizes to php-src', () => {
 const check = rules.newPHPVersionCheck('php-check');
 const html = renderToStaticMarkup(VersionRuleEditor({rules:[check],onRules:()=>{}}));
 assert.match(html,/Scan PHP versions/);
 assert.match(html,/PHP runtime, any detected version/);
 assert.doesNotMatch(html,/aria-label="Check 1 component name"/);
 assert.equal(rules.normalizeVersionChecks([check])[0].component,'php:php-src');
});
