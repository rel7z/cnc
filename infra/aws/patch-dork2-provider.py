"""Prepare the generic CLI patch; source hash is verified again on deployment."""
from pathlib import Path
import hashlib,json
root=Path('/private/tmp/cnc-dork2-fix');s=(root/'index.original.js').read_text();original=hashlib.sha256(s.encode()).hexdigest()
s=s.replace("\n", "\nimport { runActorQueued } from './cnc-provider-queue.mjs';\n", 1)
start=s.index('                // Process results\n')
end=s.index('\n\n            } catch (queryError)',start)
processing=s[start:end]
assert processing.count('items.forEach')==1
function='function processItems(items) {\n'+processing+'\n}\n\n'
s=s[:start]+'                processItems(items);'+s[end:]
s=s.replace('(async () => {',function+'(async () => {',1)
s=s.replace('let totalDomains = 0;','let queryFailures = 0;\nlet activeRunId = null;\nlet totalDomains = 0;',1)
needle="const run = await client.actor('nFJndFXA5zjCTuudP').call(input);"
assert s.count(needle)==1
s=s.replace(needle,"const run = await runActorQueued(client, 'nFJndFXA5zjCTuudP', input, processItems, { onRun: id => { activeRunId = id; } });")
s=s.replace('            } catch (queryError) {','            } catch (queryError) {\n                queryFailures++;',1)
old="        console.error(`\\n[+] Done. ${totalDomains} unique domains discovered.`);\n        process.exit(0);"
new="        console.error(`\\n[+] Done. ${totalDomains} unique domains discovered; ${queryFailures} query failures.`);\n        process.exit(queryFailures ? 1 : 0);"
assert old in s;s=s.replace(old,new)
# Best effort remote cancellation on graceful termination. Provider timeout also bounds orphan lifetime.
needle='(async () => {'
handler="""for (const signal of ['SIGINT', 'SIGTERM']) {
    process.once(signal, async () => {
        const timer = setTimeout(() => process.exit(143), 5000);
        try { if (activeRunId) await client.run(activeRunId).abort(); } catch {}
        clearTimeout(timer);
        process.exit(143);
    });
}

"""
s=s.replace(needle,handler+needle,1)
(root/'index.js').write_text(s)
(root/'cnc-provider-queue.mjs').write_text(Path('infra/aws/dork2-provider-queue.mjs').read_text())
(root/'patch.json').write_text(json.dumps({'original_sha256':original,'patched_sha256':hashlib.sha256(s.encode()).hexdigest()}))
print('Prepared bounded provider queue, incremental dataset streaming, and nonzero exit on query failures.')
