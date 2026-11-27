package dialect

// commonKeywords is the set of SQL keywords shared by most databases (upper
// case). It is deliberately conservative, containing only standard SQL keywords
// supported by the vast majority of implementations; dialect-specific keywords
// are appended in their respective files.
var commonKeywords = []string{
	// DDL
	"CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME", "TABLE", "VIEW",
	"MATERIALIZED", "INDEX", "UNIQUE", "PRIMARY", "FOREIGN", "KEY", "REFERENCES",
	"CONSTRAINT", "CHECK", "DEFAULT", "GENERATED", "IDENTITY", "SERIAL",
	"ADD", "COLUMN", "IF", "EXISTS", "CASCADE", "COMMENT", "ON",
	"SCHEMA", "DATABASE", "SEQUENCE", "TEMP", "TEMPORARY", "EXTENSION",
	// DML
	"SELECT", "FROM", "WHERE", "INSERT", "INTO", "VALUES", "UPDATE", "SET",
	"DELETE", "MERGE", "USING", "RETURNING", "DISTINCT", "ALL", "AS",
	"WITH", "RECURSIVE", "UNION", "INTERSECT", "EXCEPT", "TOP", "LIMIT",
	"OFFSET", "FETCH", "FIRST", "NEXT", "ONLY", "ROWS", "ROW",
	// joins
	"JOIN", "INNER", "LEFT", "RIGHT", "FULL", "OUTER", "CROSS", "NATURAL",
	"ON", "USING",
	// aggregation and grouping
	"GROUP", "BY", "HAVING", "WINDOW", "OVER", "PARTITION", "ORDER", "ASC",
	"DESC", "NULLS", "LAST", "FIRST",
	// conditions and logic
	"AND", "OR", "NOT", "IN", "EXISTS", "BETWEEN", "LIKE", "ILIKE", "ESCAPE",
	"IS", "NULL", "TRUE", "FALSE", "UNKNOWN", "CASE", "WHEN", "THEN", "ELSE",
	"END", "CAST", "FILTER", "ANY", "SOME",
	// transactions and privileges
	"BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "TRANSACTION", "WORK",
	"GRANT", "REVOKE", "PRIVILEGES", "TO", "FROM",
	// type keywords
	"INT", "INTEGER", "BIGINT", "SMALLINT", "DECIMAL", "NUMERIC", "REAL",
	"DOUBLE", "PRECISION", "FLOAT", "CHAR", "VARCHAR", "TEXT", "STRING",
	"BOOLEAN", "BOOL", "DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "INTERVAL",
	"JSON", "JSONB", "XML", "UUID", "BLOB", "BINARY", "ARRAY", "ENUM",
	"VARYING", "CHARACTER",
	// miscellaneous
	"RETURNING", "PIVOT", "UNPIVOT", "LATERAL", "TABLESAMPLE", "EXPLAIN", "ANALYZE", "DESCRIBE",
}
