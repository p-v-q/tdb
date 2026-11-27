package dialect

import "unicode"

// QuoteIfNeeded quotes an identifier only when necessary: when it is a reserved
// word, contains special characters, or starts with a digit. In most cases the
// name is emitted as-is to keep the SQL readable.
func QuoteIfNeeded(d Dialect, name string) string {
	if isPlainIdent(name) && !d.IsKeyword(name) {
		return name
	}
	return d.QuoteIdent(name)
}

// isPlainIdent reports whether name is a "clean" identifier (starting with a
// letter or underscore and containing only letters, digits and underscores).
func isPlainIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || unicode.IsLetter(r):
		case i > 0 && (unicode.IsDigit(r) || r == '$'):
		default:
			return false
		}
	}
	return true
}
