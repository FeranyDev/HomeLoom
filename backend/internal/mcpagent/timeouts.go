package mcpagent

import "time"

const (
	// AIProviderRequestTimeout bounds one upstream model request, including a
	// reasoning response. Tool loops are additionally bounded by
	// AIRunControlTimeout and the existing four-call limit.
	AIProviderRequestTimeout = 2 * time.Minute
	// AIRunControlTimeout keeps the Core-to-Agent request open across a normal
	// multi-step device analysis without allowing it to wait indefinitely.
	AIRunControlTimeout = 6 * time.Minute
)
