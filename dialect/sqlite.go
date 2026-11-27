package dialect

// SQLite is the SQLite3 dialect.
type SQLite struct {
	Base
}

// NewSQLite constructs the SQLite dialect.
func NewSQLite() SQLite {
	return SQLite{NewBase("sqlite", `"`,
		[]string{
			"AUTOINCREMENT", "WITHOUT", "ROWID", "CONFLICT", "ROLLBACK", "ABORT",
			"FAIL", "IGNORE", "REPLACE", "GLOB", "REGEXP", "RECURSIVE", "VACUUM",
			"PRAGMA", "ATTACH", "DETACH", "EXPLAIN", "QUERY", "PLAN",
		},
		[]Function{
			{Name: "STRFTIME", Signature: "STRFTIME(fmt, ts)", Doc: "formats a time"},
			{Name: "DATE", Signature: "DATE(ts)", Doc: "extracts the date part"},
			{Name: "TIME", Signature: "TIME(ts)", Doc: "extracts the time part"},
			{Name: "DATETIME", Signature: "DATETIME(ts)", Doc: "extracts the date-time part"},
			{Name: "JULIANDAY", Signature: "JULIANDAY(ts)", Doc: "Julian day"},
			{Name: "RANDOM", Signature: "RANDOM()", Doc: "random integer"},
			{Name: "PRINTF", Signature: "PRINTF(fmt, ...)", Doc: "formatted output"},
			{Name: "INSTR", Signature: "INSTR(s, sub)", Doc: "substring position"},
			{Name: "JSON_EXTRACT", Signature: "JSON_EXTRACT(json, path)", Doc: "extracts a JSON field"},
			{Name: "TOTAL", Signature: "TOTAL(expr)", Doc: "sum (returns 0 instead of NULL)", Aggregate: true},
			{Name: "GROUP_CONCAT", Signature: "GROUP_CONCAT(expr)", Doc: "concatenates values in a group into a string", Aggregate: true},
		}),
	}
}
