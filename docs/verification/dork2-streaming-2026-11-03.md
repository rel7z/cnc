# Dork2 incremental results fix

Dork2 starts an Apify run asynchronously and polls its append-only dataset every two seconds. New pages are processed immediately into the local results file; the existing worker watcher forwards file additions to merged. The cursor prevents page replay, existing domain/URL sets prevent duplicate output within a task, and the terminal poll drains final rows. Failed or timed-out searches retain partial results and return a failing exit code.

Verified: four Node tests (including full CLI output before provider completion and duplicate domains), five worker package integrity tests, and three Go tests for live worker output, file-record replay, and server stream routing. Offline tests create no paid Apify runs.

Deployed with rollback copies to the DigitalOcean controller and both workers. Installed file hashes match across all nodes. Canonical worker download package rebuilt with the streaming module. DigitalOcean and UpCloud source packaging includes the new module; worker readiness rejects packages missing it.

Already running searches retain their previously loaded code and were not interrupted. The fix applies to subsequent job launches. Results become available when Apify publishes dataset items; this cannot expose results before the provider writes them.

Apify client reference: https://docs.apify.com/api/client/js/docs
