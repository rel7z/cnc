const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const terminal = new Set(['SUCCEEDED','FAILED','TIMED-OUT','ABORTED']);

export async function runActorQueued(client, actorId, input, onItems, options = {}) {
    const pause = options.sleep || sleep;
    const deadline = Date.now() + (options.maxWaitMs ?? 600000);
    let run;
    for (let attempt = 0; ; attempt++) {
        try {
            run = await client.actor(actorId).start(input, { timeout: 900 });
            break;
        } catch (error) {
            const capacity = /concurrent\s+actor\s+runs|actor.*concurren.*limit/i.test(String(error.message));
            if (!capacity || attempt >= (options.maxAttempts ?? 120) || Date.now() >= deadline) throw error;
            console.error('[queue] Waiting for an available Apify Actor slot.');
            await pause(Math.min(30000, 5000 * (1 + Math.floor(attempt / 3))));
        }
    }
    options.onRun?.(run.id);
    let offset = 0;
    const drain = async () => {
        for (;;) {
            const { items } = await client.dataset(run.defaultDatasetId).listItems({ offset, limit: 250 });
            if (items.length) { onItems(items); offset += items.length; }
            if (items.length < 250) break;
        }
    };
    for (;;) {
        await drain();
        const latest = await client.run(run.id).get();
        if (terminal.has(latest.status)) {
            await drain();
            options.onRun?.(null);
            if (latest.status !== 'SUCCEEDED') throw new Error(`Apify Actor ended with status ${latest.status}`);
            return latest;
        }
        await pause(options.pollMs ?? 3000);
    }
}
