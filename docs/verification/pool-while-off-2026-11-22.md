# Collect candidates while scanning is OFF

The scanning pause flag continues to gate scanner dispatch, shard creation, worker archive waves and reverse-IP waves. It no longer gates controller-side CT ingestion, Common Crawl index admission or persistent pool refill. While OFF, an under-target pool restarts refill even above the normal low watermark. Source switches, deduplication and the 5,000,000-candidate ceiling remain enforced.

Tests: 65 generator/index unit tests passed, including paused collection, exact capacity, unique admission, and absent scanner shards. Current production sources are CT and Common Crawl index; no paid Dork runs were enabled.

The generator source before this patch matched the controller SHA-256 `bea121673c6f352772c654fc4a4ccef3bb609fcd1f5a8cae64906e78a384fc9e`. Production deployment retains rollback copies and preserves the scanning pause flag.
