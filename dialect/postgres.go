package dialect

// Postgres is the PostgreSQL dialect.
type Postgres struct {
	Base
}

// IdentifiersCaseSensitive implements dialect.CaseSensitiveIdentifiers:
// PostgreSQL preserves quoted identifier case and distinguishes "FOO" from
// "foo". Unquoted identifiers are folded to lower case by the server before
// their result names reach the client.
func (Postgres) IdentifiersCaseSensitive() bool { return true }

// NewPostgres constructs the PostgreSQL dialect.
func NewPostgres() Postgres {
	return Postgres{NewBase("postgres", `"`,
		[]string{
			"ILIKE", "RETURNING", "CONFLICT", "DO", "NOTHING", "DISTINCT", "ON",
			"JSONB", "UUID", "SERIAL", "BIGSERIAL", "SMALLSERIAL", "INET", "CIDR",
			"TSVECTOR", "TSQUERY", "GIN", "GIST", "BRIN", "HASH",
			"PARTITION", "RANGE", "LIST", "HASH", "INHERITS", "LIKE", "INCLUDING",
			"GENERATED", "ALWAYS", "STORED", "IDENTITY", "DEFERRABLE", "INITIALLY",
			"FOR", "SHARE", "LOCK", "NOWAIT", "SKIP", "LOCKED", "VACUUM", "EXPLAIN",
		},
		[]Function{
			{Name: "STRING_AGG", Signature: "STRING_AGG(expr, sep)", Doc: "concatenates values in a group into a string", Aggregate: true},
			{Name: "ARRAY_AGG", Signature: "ARRAY_AGG(expr)", Doc: "collects values in a group into an array", Aggregate: true},
			{Name: "DATE_TRUNC", Signature: "DATE_TRUNC('day', ts)", Doc: "truncates a timestamp to the given precision"},
			{Name: "AGE", Signature: "AGE(timestamp)", Doc: "computes a time interval"},
			{Name: "GREATEST", Signature: "GREATEST(a, b, ...)", Doc: "returns the greatest value"},
			{Name: "LEAST", Signature: "LEAST(a, b, ...)", Doc: "returns the least value"},
			{Name: "ROW_NUMBER", Signature: "ROW_NUMBER() OVER (...)", Doc: "window function: row number", Window: true},
			{Name: "RANK", Signature: "RANK() OVER (...)", Doc: "window function: rank (with gaps for ties)", Window: true},
			{Name: "DENSE_RANK", Signature: "DENSE_RANK() OVER (...)", Doc: "window function: dense rank", Window: true},
			{Name: "LAG", Signature: "LAG(expr, offset)", Doc: "window function: value from N rows before", Window: true},
			{Name: "LEAD", Signature: "LEAD(expr, offset)", Doc: "window function: value from N rows after", Window: true},
			{Name: "JSONB_BUILD_OBJECT", Signature: "JSONB_BUILD_OBJECT(k, v, ...)", Doc: "builds a JSONB object"},
			{Name: "JSONB_EXTRACT_PATH", Signature: "JSONB_EXTRACT_PATH(json, path...)", Doc: "extracts a JSONB field by path"},
		}),
	}
}
