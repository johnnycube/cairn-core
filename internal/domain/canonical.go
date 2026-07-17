package domain

// ActivitySourcePayload (source.go) is Cairn's canonical activity schema: every
// worker maps its raw payload onto it before pushing. Workers normalize; the
// server archives the raw blob and can re-derive via parse_blob. The mapping is
// versioned per (provider, package, version) on each source.

// CanonicalSchemaVersion is the version of the ActivitySourcePayload contract;
// bump it on a breaking shape change so older workers can be detected.
const CanonicalSchemaVersion = 1
