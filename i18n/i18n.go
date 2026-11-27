// Package i18n provides localization for the user interface.
//
// Design notes:
//   - Locales use simplified BCP 47 tags (en, zh-Hans); Detect infers one from
//     the environment.
//   - Messages are addressed by Key (dotted constants) so callers never embed
//     human-readable text.
//   - A Bundle is immutable: switching languages swaps the bundle, which keeps
//     the model value-copy friendly and race-free.
//   - Missing keys fall back three levels deep (current locale → default
//     locale → the key itself), so the UI never renders an empty string.
//   - Validate keeps every catalog in sync; a missing translation fails tests.
package i18n

import (
	"fmt"
	"sort"
	"strings"
)

// Locale is a language tag (simplified BCP 47).
type Locale string

// Supported locales.
const (
	// En is English, the default language.
	En Locale = "en"
	// ZhHans is Simplified Chinese.
	ZhHans Locale = "zh-Hans"
)

// Default is used when the environment gives no usable hint.
const Default = En

// Available returns the built-in locales. The UI language picker and the CLI
// flag validation share this list.
func Available() []Locale { return []Locale{En, ZhHans} }

// Translator abstracts "look up a message". The UI depends only on this, which
// keeps it easy to fake in tests.
type Translator interface {
	// T returns the message for key, formatting args with fmt.Sprintf.
	T(key Key, args ...any) string
	// Locale returns the active language.
	Locale() Locale
}

// Bundle is the set of messages for one language. The zero value is not
// usable; construct one with New.
type Bundle struct {
	locale Locale
	msgs   map[Key]string
}

// New builds the bundle for locale, falling back to Default when unknown.
func New(locale Locale) *Bundle {
	l, ok := Normalize(string(locale))
	if !ok {
		l = Default
	}
	msgs := catalogs[l]
	if msgs == nil {
		msgs = catalogs[Default]
	}
	return &Bundle{locale: l, msgs: msgs}
}

// Locale returns the active language.
func (b *Bundle) Locale() Locale { return b.locale }

// T returns the message for key, falling back to the default locale and then to
// the key itself when the translation is missing.
func (b *Bundle) T(key Key, args ...any) string {
	if b == nil {
		return string(key)
	}
	format, ok := b.msgs[key]
	if !ok {
		if format, ok = catalogs[Default][key]; !ok {
			return string(key)
		}
	}
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// LocaleName returns the name of the active language, written in that language
// (an endonym, so every locale names itself).
func (b *Bundle) LocaleName() string { return b.T(KeyLocaleName) }

// LocaleName returns a language's endonym, provided by its own bundle.
func LocaleName(l Locale) string { return New(l).T(KeyLocaleName) }

// Normalize maps an arbitrary language tag ("en_US.UTF-8", "zh-Hans-CN") to a
// supported locale. The boolean reports whether the tag was understood.
func Normalize(tag string) (Locale, bool) {
	t := strings.ToLower(strings.TrimSpace(tag))
	// Drop encoding and modifier suffixes: zh_cn.utf-8 → zh_cn
	if i := strings.IndexAny(t, ".@"); i >= 0 {
		t = t[:i]
	}
	switch {
	case t == "" || t == "c" || t == "posix":
		return "", false
	case strings.HasPrefix(t, "zh"):
		return ZhHans, true
	case strings.HasPrefix(t, "en"):
		return En, true
	}
	return "", false
}

// Detect infers the language from environment variables, in the usual order:
// LC_ALL > LC_MESSAGES > LANG. getenv is normally os.Getenv; tests pass a stub.
func Detect(getenv func(string) string) Locale {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if l, ok := Normalize(getenv(name)); ok {
			return l
		}
	}
	return Default
}

// Validate checks that every locale defines exactly the same key set and
// returns a description of each problem. It exists so that adding a message
// without translating it fails the test suite.
func Validate() []string {
	keys := Keys()
	var problems []string
	for _, l := range Available() {
		msgs := catalogs[l]
		if msgs == nil {
			problems = append(problems, fmt.Sprintf("locale %s has no catalog", l))
			continue
		}
		for _, k := range keys {
			if strings.TrimSpace(msgs[k]) == "" {
				problems = append(problems, fmt.Sprintf("locale %s is missing message %q", l, k))
			}
		}
		for k := range msgs {
			if !knownKeys[k] {
				problems = append(problems, fmt.Sprintf("locale %s has undeclared key %q", l, k))
			}
		}
	}
	sort.Strings(problems)
	return problems
}
