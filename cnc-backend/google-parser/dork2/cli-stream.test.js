import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, copyFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { once } from 'node:events';

test('CLI writes clean incremental output before provider completion and deduplicates', async () => {
    const dir = await mkdtemp(join(tmpdir(), 'dork-stream-'));
    try {
        await writeFile(join(dir, 'package.json'), '{"type":"module"}');
        const source = await readFile(new URL('./index.js', import.meta.url), 'utf8');
        await writeFile(join(dir, 'index.js'), source.replace("from 'apify-client'", "from './mock.js'"));
        await copyFile(new URL('./stream-run.js', import.meta.url), join(dir, 'stream-run.js'));
        await writeFile(join(dir, 'mock.js'), `
            import fs from 'node:fs';
            export class ApifyClient {
                actor() { return { start: async () => ({id:'fixture', status:'RUNNING', defaultDatasetId:'d'}) }; }
                dataset() { return { listItems: async ({offset}) => ({items: offset ? [] : [{organicResults:[{url:'https://example.com/a'},{url:'https://example.com/b'}]}]}) }; }
                run() { return {get: async () => {
                    if (fs.readFileSync('results.txt','utf8') !== 'example.com\\n') throw Error('not streamed before completion');
                    return {id:'fixture', status:'SUCCEEDED', defaultDatasetId:'d'};
                }}; }
            }
        `);
        const child = spawn(process.execPath, ['index.js', '-k', 'fixture', '-o', 'results.txt'], {
            cwd: dir, env: { ...process.env, APIFY_TOKEN: 'offline-fixture' },
        });
        let stdout = '', stderr = '';
        child.stdout.on('data', x => { stdout += x; });
        child.stderr.on('data', x => { stderr += x; });
        const [code] = await once(child, 'exit');
        assert.equal(code, 0, stderr);
        assert.equal(stdout, 'example.com\n');
        assert.equal(await readFile(join(dir, 'results.txt'), 'utf8'), stdout);
        assert.match(stderr, /streaming results while running/);
    } finally {
        await rm(dir, { recursive: true, force: true });
    }
});
