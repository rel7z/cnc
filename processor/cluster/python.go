package cluster

import _ "embed"

//go:embed python/processor_publish.py
var publicationHelper []byte

//go:embed python/processor_supervise.py
var publicationSupervisor []byte
