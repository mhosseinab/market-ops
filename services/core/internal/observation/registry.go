package observation

import (
	"strings"
	"sync"
)

// ParserSupport declares ONE supported capture identity: a (sourceType,
// parserVersion) tuple, optionally pinned to a set of compatible connector
// versions (schema/connector-compatibility, docs/14). An empty ConnectorVersions
// means the parser is compatible with any connector (Route C, the server observer,
// carries no connector version). This is the unit the server-owned registry stores.
type ParserSupport struct {
	SourceType        SourceType
	ParserVersion     string
	ConnectorVersions []string
}

// parserKey is the exact-match lookup key: the source discriminator plus the parser
// version token. Identity is exact — a version is unknown until explicitly declared,
// never inferred from a prefix or a "close enough" semver (quarantine over inference).
type parserKey struct {
	sourceType SourceType
	parser     string
}

// ParserRegistry is the SERVER-OWNED allow-list of supported capture parser
// identities (#154). It is consulted BEFORE quality derivation: a capture whose
// (sourceType, parserVersion[, connectorVersion]) is not registered is treated as
// schema-INVALID and fails closed to Unverified/quarantine, so an unknown, retired,
// or malformed parser can never accumulate qualifying history or corroboration and
// reach an execution-capable state. The client-sent parser version and confidence
// are evidence, never authority — the registry decides. Safe for concurrent use;
// registration is additive (a version, once admitted, is never silently dropped, and
// admitting a version never rewrites append-only evidence).
type ParserRegistry struct {
	mu        sync.RWMutex
	supported map[parserKey]connectorSet
}

// connectorSet is the compatibility rule for one parser key. When any is true the
// parser accepts any connector version; otherwise only versions in the set match.
type connectorSet struct {
	any        bool
	connectors map[string]struct{}
}

// NewParserRegistry builds a registry from explicit entries (tests and the boot
// seed use this). Later entries for the same key MERGE their connector
// compatibility additively.
func NewParserRegistry(entries ...ParserSupport) *ParserRegistry {
	r := &ParserRegistry{supported: make(map[parserKey]connectorSet)}
	for _, e := range entries {
		r.Register(e)
	}
	return r
}

// DefaultParserRegistry is the boot-seeded registry of the REAL production capture
// identities. Bump/extend these ONLY alongside the producing parser's version and a
// re-freeze of the compatibility contract — a new version is unsupported until it is
// added here (fail closed):
//   - Route C server observer: routec-parser/1.0.0 over the public web endpoint
//     (internal/routec ParserVersion), no connector version.
//   - Route B extension: dk-product@1.0.0 over the public web endpoint, pinned to the
//     extension connector market-ops-ext@0.1.0 (apps/extension constants).
func DefaultParserRegistry() *ParserRegistry {
	return NewParserRegistry(
		ParserSupport{SourceType: SourcePublicWebEndpoint, ParserVersion: "routec-parser/1.0.0"},
		ParserSupport{
			SourceType:        SourcePublicWebEndpoint,
			ParserVersion:     "dk-product@1.0.0",
			ConnectorVersions: []string{"market-ops-ext@0.1.0"},
		},
	)
}

// Register admits a parser identity. It is additive: it never removes an existing
// key, and merging a new entry for an existing key unions its connector
// compatibility. A blank parser version is ignored (an empty version can never be a
// supported identity). Version-support changes go through here so they stay explicit
// and auditable at the call site.
func (r *ParserRegistry) Register(e ParserSupport) {
	if strings.TrimSpace(e.ParserVersion) == "" || !e.SourceType.valid() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := parserKey{sourceType: e.SourceType, parser: e.ParserVersion}
	cur, ok := r.supported[k]
	if !ok {
		cur = connectorSet{connectors: make(map[string]struct{})}
	}
	if len(e.ConnectorVersions) == 0 {
		cur.any = true
	}
	for _, c := range e.ConnectorVersions {
		cur.connectors[c] = struct{}{}
	}
	r.supported[k] = cur
}

// Supported reports whether a capture's parser identity is registered. It is exact:
// the (sourceType, parserVersion) must be present AND, when the entry pins connector
// versions, the connectorVersion must match one of them. A blank/whitespace parser
// version, an unknown source, or an unregistered tuple all return false — the
// fail-closed answer. This is the single authority the ingest path consults before
// deriving quality.
func (r *ParserRegistry) Supported(sourceType SourceType, parserVersion, connectorVersion string) bool {
	if strings.TrimSpace(parserVersion) == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	set, ok := r.supported[parserKey{sourceType: sourceType, parser: parserVersion}]
	if !ok {
		return false
	}
	if set.any {
		return true
	}
	_, matched := set.connectors[connectorVersion]
	return matched
}
