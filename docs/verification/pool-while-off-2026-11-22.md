# Collect candidates while scanning is OFF

The scanning pause flag continues to gate scanner dispatch, shard creation, worker archive waves and reverse-IP waves. It no longer gates controller-side CT ingestion, Common Crawl index admission or persistent pool refill. While OFF, an under-target pool restarts refill even above the normal low watermark. Source switches, deduplication and the 5,000,000-candidate ceiling remain enforced.

Tests: 65 generator/index unit tests passed, including paused collection, exact capacity, unique admission, and absent scanner shards. Current production sources are CT and Common Crawl index; no paid Dork runs were enabled.

The generator source before this patch matched the controller SHA-256 `bea121673c6f352772c654fc4a4ccef3bb609fcd1f5a8cae64906e78a384fc9e`. Production deployment retains rollback copies and preserves the scanning pause flag.

Live follow-up found an older source-policy module missing `collection_records`; it was backed up and synchronized with the committed index-reader dependency. Installation now checks this reader/source contract before service activation. Scanning remained OFF.

The controller could not reach the official collection metadata endpoint. An unmodified official inventory from `https://index.commoncrawl.org/collinfo.json` (129 records, SHA-256 `a683312f4448cdda63b38f40c7289853fa82eb1e7e3cd5a3674e46142b01891a`) was installed into the existing inventory cache. Reader progress then advanced through public parquet parts; live pool increased to 114,423 while the pause flag remained present. No scanner jobs were enabled. The pool has not yet reached its target.
