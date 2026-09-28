package tasksource

import "orchestra/hostagent/internal/gateway"

// The gateway client moved to internal/gateway when host-side model calls (chat
// history compaction) started needing it too — a client for "anything that must
// go through the gateway" does not belong in the package about task sources.
//
// These are aliases rather than a rename so the adapters, and their tests, keep
// reading the way they did: an adapter cares that it has a gateway client, not
// which package declares it.

// GatewayClient issues an adapter's reads through the security gateway.
type GatewayClient = gateway.Client

// NewGatewayClient builds a client. base is e.g. "http://127.0.0.1:8787".
func NewGatewayClient(base, session string) *GatewayClient {
	return gateway.New(base, session)
}
