// Package capability issues and verifies capability tokens: scoped, expiring,
// revocable grants of read access, the only source of authority in AgentNet
// (Docs/protocol/grant.md). This package covers the token itself (canonical
// form, signing, offline verification steps 1-8); the grants store, issuance
// flow and online enforcement (steps 9-10) are built on top of it in later
// tickets (2.2c, 2.3a).
package capability
