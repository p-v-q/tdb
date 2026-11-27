package dialect

// commonFunctions is the set of built-in functions shared by most databases (a
// conservative collection).
var commonFunctions = []Function{
	{Name: "COUNT", Signature: "COUNT(*)", Doc: "counts rows", Aggregate: true},
	{Name: "COUNT", Signature: "COUNT(DISTINCT expr)", Doc: "counts distinct rows", Aggregate: true},
	{Name: "SUM", Signature: "SUM(expr)", Doc: "sum", Aggregate: true},
	{Name: "AVG", Signature: "AVG(expr)", Doc: "average", Aggregate: true},
	{Name: "MIN", Signature: "MIN(expr)", Doc: "minimum", Aggregate: true},
	{Name: "MAX", Signature: "MAX(expr)", Doc: "maximum", Aggregate: true},
	{Name: "COALESCE", Signature: "COALESCE(a, b, ...)", Doc: "returns the first non-NULL value"},
	{Name: "NULLIF", Signature: "NULLIF(a, b)", Doc: "returns NULL when a = b"},
	{Name: "CAST", Signature: "CAST(expr AS type)", Doc: "type conversion"},
	{Name: "ABS", Signature: "ABS(x)", Doc: "absolute value"},
	{Name: "ROUND", Signature: "ROUND(x, n)", Doc: "rounds to n decimal places"},
	{Name: "CEIL", Signature: "CEIL(x)", Doc: "rounds up"},
	{Name: "FLOOR", Signature: "FLOOR(x)", Doc: "rounds down"},
	{Name: "LENGTH", Signature: "LENGTH(s)", Doc: "string length"},
	{Name: "UPPER", Signature: "UPPER(s)", Doc: "converts to upper case"},
	{Name: "LOWER", Signature: "LOWER(s)", Doc: "converts to lower case"},
	{Name: "TRIM", Signature: "TRIM(s)", Doc: "trims leading and trailing whitespace"},
	{Name: "SUBSTRING", Signature: "SUBSTRING(s, start, len)", Doc: "substring"},
	{Name: "REPLACE", Signature: "REPLACE(s, old, new)", Doc: "string replacement"},
	{Name: "CONCAT", Signature: "CONCAT(a, b, ...)", Doc: "string concatenation"},
	{Name: "NOW", Signature: "NOW()", Doc: "current timestamp"},
	{Name: "CURRENT_DATE", Signature: "CURRENT_DATE", Doc: "current date"},
	{Name: "CURRENT_TIMESTAMP", Signature: "CURRENT_TIMESTAMP", Doc: "current timestamp"},
	{Name: "EXTRACT", Signature: "EXTRACT(field FROM ts)", Doc: "extracts a date field (year/month/day, etc.)"},
	{Name: "DATE_ADD", Signature: "DATE_ADD(date, INTERVAL n unit)", Doc: "adds or subtracts dates"},
}
