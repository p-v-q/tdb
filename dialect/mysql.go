package dialect

// MySQL is the MySQL / MariaDB dialect.
type MySQL struct {
	Base
}

// NewMySQL constructs the MySQL dialect (quoting identifiers with backticks).
func NewMySQL() MySQL {
	return MySQL{NewBase("mysql", "`",
		[]string{
			"AUTO_INCREMENT", "ENGINE", "CHARSET", "COLLATE", "DUPLICATE", "KEY",
			"STRAIGHT_JOIN", "SQL_CALC_FOUND_ROWS", "FOUND_ROWS", "FORCE", "IGNORE",
			"LOCK", "UNLOCK", "TABLES", "WRITE", "READ", "LOW_PRIORITY",
			"UNSIGNED", "ZEROFILL", "TINYINT", "MEDIUMINT", "LONGBLOB", "MEDIUMTEXT",
			"LONGTEXT", "ENUM", "SET", "JSON", "DATETIME", "YEAR", "BIT",
			"DELIMITER", "PROCEDURE", "FUNCTION", "TRIGGER", "EVENT", "DECLARE",
			"CONTINUE", "EXIT", "HANDLER", "CURSOR", "FETCH", "OPEN", "CLOSE",
		},
		[]Function{
			{Name: "GROUP_CONCAT", Signature: "GROUP_CONCAT(expr ORDER BY ...)", Doc: "concatenates values in a group into a string", Aggregate: true},
			{Name: "IFNULL", Signature: "IFNULL(a, b)", Doc: "returns b when a is NULL"},
			{Name: "IF", Signature: "IF(cond, a, b)", Doc: "conditional expression"},
			{Name: "DATE_FORMAT", Signature: "DATE_FORMAT(date, fmt)", Doc: "formats a date"},
			{Name: "STR_TO_DATE", Signature: "STR_TO_DATE(s, fmt)", Doc: "parses a date from a format"},
			{Name: "DATEDIFF", Signature: "DATEDIFF(a, b)", Doc: "number of days between two dates"},
			{Name: "CURDATE", Signature: "CURDATE()", Doc: "current date"},
			{Name: "CURTIME", Signature: "CURTIME()", Doc: "current time"},
			{Name: "UUID", Signature: "UUID()", Doc: "generates a UUID"},
			{Name: "CONCAT_WS", Signature: "CONCAT_WS(sep, a, b, ...)", Doc: "concatenates strings with a separator"},
			{Name: "SUBSTRING_INDEX", Signature: "SUBSTRING_INDEX(s, delim, n)", Doc: "extracts a substring by delimiter"},
			{Name: "FOUND_ROWS", Signature: "FOUND_ROWS()", Doc: "total row count of the previous query"},
		}),
	}
}
