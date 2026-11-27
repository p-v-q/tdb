// Package dialect defines the SQL dialect abstraction.
//
// All of the editor's language-aware features build on dialects: keyword
// highlighting, keyword and function completion, identifier quoting rules, and
// more. A new database only needs to implement this interface and call Register
// to plug into tdb.
package dialect

import (
	"strings"

	"github.com/p-v-q/tdb/sql"
)

// Function describes a built-in function for completion and documentation.
type Function struct {
	Name      string
	Signature string // e.g. string_agg(col, sep)
	Doc       string
	Aggregate bool // whether it is an aggregate function
	Window    bool // whether it is a window function
}

// Dialect is a SQL dialect.
type Dialect interface {
	// Name returns the dialect name, e.g. "postgres".
	Name() string
	// QuoteIdent quotes an identifier using the dialect's rules.
	QuoteIdent(name string) string
	// IsKeyword reports whether the word is a dialect keyword (case-insensitive).
	IsKeyword(word string) bool
	// Keywords returns all keywords (upper case).
	Keywords() []string
	// Functions returns the list of built-in functions.
	Functions() []Function
}

// CaseSensitiveIdentifiers is an optional capability for dialects whose
// identifiers are case-sensitive. It stays out of Dialect so existing dialect
// implementations remain compatible; dialects without it retain the historical
// case-insensitive identifier matching.
type CaseSensitiveIdentifiers interface {
	IdentifiersCaseSensitive() bool
}

// IdentifiersCaseSensitive reports whether d distinguishes identifier case.
func IdentifiersCaseSensitive(d Dialect) bool {
	capable, ok := d.(CaseSensitiveIdentifiers)
	return ok && capable.IdentifiersCaseSensitive()
}

// Base is an embeddable, generic dialect implementation; subtypes customise
// behaviour by overriding fields.
type Base struct {
	dialectName string
	quoteChar   string
	keywords    []string
	keywordSet  sql.Keywords
	functions   []Function
}

// NewBase constructs a generic dialect. quoteChar is the identifier quote
// character (for example `"` or a backtick).
func NewBase(name, quoteChar string, extraKeywords []string, extraFunctions []Function) Base {
	kws := dedupeKeywords(append(append([]string{}, commonKeywords...), extraKeywords...))
	funcs := append(append([]Function{}, commonFunctions...), extraFunctions...)
	return Base{
		dialectName: name,
		quoteChar:   quoteChar,
		keywords:    kws,
		keywordSet:  sql.NewKeywords(kws...),
		functions:   funcs,
	}
}

// dedupeKeywords removes duplicates while preserving order.
func dedupeKeywords(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, w := range in {
		u := strings.ToUpper(w)
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// Name implements Dialect.
func (b Base) Name() string { return b.dialectName }

// QuoteIdent implements Dialect.
func (b Base) QuoteIdent(name string) string {
	qc := b.quoteChar
	if qc == "" {
		qc = `"`
	}
	return qc + strings.ReplaceAll(name, qc, qc+qc) + qc
}

// IsKeyword implements Dialect.
func (b Base) IsKeyword(word string) bool { return b.keywordSet.IsKeyword(word) }

// Keywords implements Dialect.
func (b Base) Keywords() []string { return b.keywords }

// Functions implements Dialect.
func (b Base) Functions() []Function { return b.functions }
