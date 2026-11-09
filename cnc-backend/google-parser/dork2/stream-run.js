const terminal = new Set(['SUCCEEDED', 'FAILED', 'TIMED-OUT', 'ABORTED']);

// Datasets are append-only. Advance only after processing a whole page, and
// drain again AFTER observing terminal state to include the final appended page.
export async function streamRun(client, initialRun, onItem, {
    pollMs = 2000, pageSize = 250,
    sleep = ms => new Promise(resolve => setTimeout(resolve, ms)),
    onProgress = () => {},
} = {}) {
    let run = initialRun;
    let offset = 0;
    let previousProgress;
    const drain = async () => {
        if (!run.defaultDatasetId) return;
        for (;;) {
            const { items } = await client.dataset(run.defaultDatasetId).listItems({
                offset, limit: pageSize, desc: false,
            });
            for (const item of items) await onItem(item);
            offset += items.length;
            if (items.length < pageSize) return;
        }
    };
    for (;;) {
        await drain();
        const progress = `${run.status}${run.statusMessage ? ': ' + run.statusMessage : ''}`;
        if (progress !== previousProgress) {
            onProgress(progress);
            previousProgress = progress;
        }
        if (terminal.has(run.status)) {
            if (run.status !== 'SUCCEEDED') throw new Error(`Apify run ${run.id} ended ${run.status}`);
            return offset;
        }
        await sleep(pollMs);
        run = await client.run(run.id).get();
        if (!run) throw new Error(`Apify run ${initialRun.id} was not found`);
    }
}
