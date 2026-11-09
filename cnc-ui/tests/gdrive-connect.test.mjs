import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import ts from 'typescript';
const require = createRequire(import.meta.url);

function compile(path, imports = {}) {
  const source = readFileSync(new URL(path, import.meta.url), 'utf8');
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText;
  const module = { exports: {} };
  new Function('require', 'module', 'exports', compiled)(name => imports[name] ?? require(name), module, module.exports);
  return module.exports;
}

const bridge = compile('../lib/drive-connect.ts');
const tickets = compile('../lib/drive-connect-ticket.ts');
const state = 'a'.repeat(43);
const callback = 'http://localhost:8081/api/config/gdrive/callback';
const authorization = `https://accounts.google.com/o/oauth2/auth?state=${state}&redirect_uri=${encodeURIComponent(callback)}`;

test('Connect keeps the fixed callback when the dashboard port changes', async () => {
  const stable = compile('../lib/drive-oauth.ts');
  const originalFetch = globalThis.fetch, originalWindow = globalThis.window;
  const requests = [];
  try {
    globalThis.fetch = async (url, options) => {
      requests.push([url, options?.method]);
      return url.endsWith('/_cnc-drive/health')
        ? Response.json({ protocol: 'cnc-drive-callback-v1', callback_url: stable.DEFAULT_DRIVE_CALLBACK })
        : Response.json({ url: 'http://localhost:8080/drive-connect?ticket=' + 'a'.repeat(32) });
    };
    for (const port of [8111, 9244]) {
      globalThis.window = { location: { origin: `http://localhost:${port}` } };
      assert.equal(new URL(await stable.driveConnectionURL(stable.DEFAULT_DRIVE_CALLBACK)).origin, 'http://localhost:8080');
    }
    assert.deepEqual(requests.map(([url]) => url), ['http://localhost:8080/_cnc-drive/health', '/api/drive-connect-ticket', 'http://localhost:8080/_cnc-drive/health', '/api/drive-connect-ticket']);
  } finally { globalThis.fetch = originalFetch; globalThis.window = originalWindow; }
});

test('Missing callback manager blocks Google authorization before making a ticket', async () => {
  const stable = compile('../lib/drive-oauth.ts');
  const originalFetch = globalThis.fetch, originalWindow = globalThis.window;
  const requests = [];
  try {
    globalThis.window = { location: { origin: 'http://localhost:8111' } };
    globalThis.fetch = async url => { requests.push(url); throw new Error('unavailable'); };
    await assert.rejects(stable.driveConnectionURL(stable.DEFAULT_DRIVE_CALLBACK), /Open CNC Manager/);
    assert.equal(requests.length, 1);
    assert.equal(requests[0], 'http://localhost:8080/_cnc-drive/health');
  } finally { globalThis.fetch = originalFetch; globalThis.window = originalWindow; }
});

test('Login returns to Drive recovery or fresh authorization without open redirects', () => {
  const { loginDestination } = compile('../lib/login-destination.ts');
  for (const destination of ['/drive-recover', '/drive-local-start', '/dashboard?connectDrive=1', '/dashboard/server']) {
    assert.equal(loginDestination(destination), destination);
  }
  for (const destination of ['//evil.example', 'https://evil.example', '/dashboard/../../evil', '/dashboard\\evil', '/drive-recover/elsewhere', '/dashboard-other']) {
    assert.equal(loginDestination(destination), '/dashboard');
  }
});

test('Drive bridge accepts Google OAuth for the current callback origin', () => {
  const bridgeURL = bridge.buildDriveBridgeURL(callback, authorization);
  const parsedBridge = new URL(bridgeURL);
  assert.equal(parsedBridge.origin, 'http://localhost:8081');
  assert.equal(parsedBridge.pathname, '/drive-connect');
  assert.equal(parsedBridge.searchParams.get('state'), state);
  assert.equal(parsedBridge.searchParams.get('authorization'), authorization);
  const result = bridge.validateDriveBridge('http://localhost:8081/drive-connect', authorization, state);
  assert.equal(result.authorization.href, authorization);
  assert.equal(result.state, state);
  assert.equal(result.secure, false);
  const payload = Buffer.from(authorization).toString('base64url');
  assert.equal(bridge.decodeDriveAuthorizationPayload(payload), authorization);
  const proxied = bridge.driveBridgeRequestURL('http://localhost:3000/drive-connect?payload=x', 'localhost:8081', 'http');
  assert.equal(proxied, 'http://localhost:8081/drive-connect?payload=x');
  assert.equal(bridge.validateDriveBridge(proxied, authorization, state).state, state);
});

test('Drive bridge rejects open redirects, state swaps, and callback-origin swaps', () => {
  assert.throws(() => bridge.buildDriveBridgeURL(callback, `https://evil.example/o/oauth2/auth?state=${state}&redirect_uri=${encodeURIComponent(callback)}`));
  assert.throws(() => bridge.validateDriveBridge('http://localhost:8081/drive-connect', `https://evil.example/o/oauth2/auth?state=${state}&redirect_uri=${encodeURIComponent(callback)}`, state));
  assert.throws(() => bridge.validateDriveBridge('http://localhost:8081/drive-connect', authorization, 'b'.repeat(43)));
  const wrongCallback = `https://accounts.google.com/o/oauth2/auth?state=${state}&redirect_uri=${encodeURIComponent('http://localhost:8080/api/config/gdrive/callback')}`;
  assert.throws(() => bridge.validateDriveBridge('http://localhost:8081/drive-connect', wrongCallback, state));
  assert.throws(() => bridge.decodeDriveAuthorizationPayload('../unsafe'));
  assert.throws(() => bridge.decodeDriveAuthorizationPayload('a'.repeat(4097)));
});

test('Drive connect tickets are short, random, and single-use', () => {
  const ticket = tickets.createDriveConnectTicket(authorization);
  assert.match(ticket, /^[A-Za-z0-9_-]{32}$/);
  assert.equal(tickets.consumeDriveConnectTicket(ticket), authorization);
  assert.equal(tickets.consumeDriveConnectTicket(ticket), null);
  assert.equal(tickets.consumeDriveConnectTicket('../unsafe'), null);
});

test('OAuth callback bypasses dashboard login while other API routes remain protected', () => {
  const NextResponse = {
    next: () => new Response(null, { status: 200 }),
    json: (value, init) => Response.json(value, init),
    redirect: url => Response.redirect(url),
  };
  const auth = { SESSION_COOKIE: 'session', validSession: () => false };
  const { proxy } = compile('../proxy.ts', { 'next/server': { NextResponse }, './lib/auth': auth });
  const request = path => {
    const nextUrl = new URL(`http://localhost:8081${path}`);
    nextUrl.clone = () => new URL(nextUrl.href);
    return { nextUrl, cookies: { get: () => undefined } };
  };
  assert.equal(proxy(request('/api/config/gdrive/callback?state=x')).status, 200);
  assert.equal(proxy(request('/api/config/gdrive/auth-status')).status, 401);
  assert.equal(proxy(request('/drive-local-start')).status, 302);
});

test('Tickets expire at the ten minute deadline', () => {
  const now = Date.now;
  try {
    Date.now = () => 1000;
    const ticket = tickets.createDriveConnectTicket(authorization);
    Date.now = () => 1000 + 10 * 60 * 1000;
    assert.equal(tickets.consumeDriveConnectTicket(ticket), null);
  } finally { Date.now = now; }
});

test('Invalid and reused links recover while valid tickets still set a state cookie', () => {
  class NextResponse extends Response {
    constructor(body, init) { super(body, init); this.cookies = { set: (...args) => { this.cookie = args; } }; }
    static redirect(url) { return new NextResponse(null, { status: 307, headers: { Location: String(url) } }); }
  }
  const route = compile('../app/drive-connect/route.ts', {
    'next/server': { NextResponse },
    '@/lib/drive-connect': bridge,
    '@/lib/drive-connect-ticket': tickets,
  });
  const request = query => {
    const url = `http://localhost:8081/drive-connect${query}`;
    return { url, nextUrl: new URL(url), headers: new Headers({ host: 'localhost:8081' }) };
  };
  for (const query of ['', '?ticket=missing', '?ticket=' + 'z'.repeat(32), '?authorization=https://evil.example&state=' + state]) {
    const response = route.GET(request(query));
    assert.equal(response.status, 303);
    assert.equal(response.headers.get('Location'), '/drive-recover');
    assert.equal(response.headers.get('Cache-Control'), 'no-store');
    assert.equal(response.headers.get('Referrer-Policy'), 'no-referrer');
  }
  const ticket = tickets.createDriveConnectTicket(authorization);
  const first = route.GET(request('?ticket=' + ticket));
  assert.equal(first.status, 307);
  assert.equal(first.headers.get('Location'), authorization);
  assert.equal(first.cookie[0], bridge.DRIVE_STATE_COOKIE);
  assert.equal(first.cookie[1], state);
  assert.equal(first.cookie[2].httpOnly, true);
  assert.equal(first.headers.get('Cache-Control'), 'no-store');
  assert.equal(route.GET(request('?ticket=' + ticket)).status, 303);
});
