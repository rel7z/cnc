import test from 'node:test';
import assert from 'node:assert/strict';
import { streamRun } from './stream-run.js';

test('streams before completion, paginates, and drains the final page', async () => {
    let finished = false;
    let rows = [{ url: 'a' }, { url: 'b' }, { url: 'c' }];
    const seen = [];
    const client = {
        dataset: () => ({ listItems: async ({ offset, limit, desc }) => {
            assert.equal(desc, false);
            return { items: rows.slice(offset, offset + limit) };
        } }),
        run: () => ({ get: async () => {
            assert.deepEqual(seen, ['a', 'b', 'c']);
            finished = true;
            rows.push({ url: 'final' });
            return { id: 'r', defaultDatasetId: 'd', status: 'SUCCEEDED' };
        } }),
    };
    const n = await streamRun(client, { id: 'r', defaultDatasetId: 'd', status: 'RUNNING' }, item => {
        if (item.url !== 'final') assert.equal(finished, false);
        seen.push(item.url);
    }, { pageSize: 2, sleep: async () => {} });
    assert.equal(n, 4);
    assert.deepEqual(seen, ['a', 'b', 'c', 'final']);
});

test('preserves partial results and reports provider failure', async () => {
    const seen = [];
    const client = {
        dataset: () => ({ listItems: async ({ offset }) => ({ items: offset ? [] : ['partial'] }) }),
        run: () => ({ get: async () => ({ id: 'r', defaultDatasetId: 'd', status: 'TIMED-OUT' }) }),
    };
    await assert.rejects(streamRun(client, { id: 'r', defaultDatasetId: 'd', status: 'RUNNING' }, x => seen.push(x), { sleep: async () => {} }), /TIMED-OUT/);
    assert.deepEqual(seen, ['partial']);
});

test('empty poll does not lose later results or replay earlier rows', async () => {
    let polls = 0;
    const seen = [];
    const client = {
        dataset: () => ({ listItems: async ({ offset }) => ({ items: polls && !offset ? ['new'] : [] }) }),
        run: () => ({ get: async () => ({ id: 'r', defaultDatasetId: 'd', status: ++polls === 3 ? 'SUCCEEDED' : 'RUNNING' }) }),
    };
    await streamRun(client, { id: 'r', defaultDatasetId: 'd', status: 'RUNNING' }, x => seen.push(x), { sleep: async () => {} });
    assert.deepEqual(seen, ['new']);
});
