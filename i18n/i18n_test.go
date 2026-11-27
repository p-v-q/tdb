package i18n

import (
	"strings"
	"testing"
)

func TestValidateCatalogsAreComplete(t *testing.T) {
	if problems := Validate(); len(problems) > 0 {
		t.Fatalf("catalogs are out of sync (%d problem(s)):\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
}

func TestTReplacesPlaceholders(t *testing.T) {
	if got := New(En).T(KeyMsgSaved, "a.sql", 3); got != "Saved a.sql (3 lines)" {
		t.Fatalf("T(en) = %q", got)
	}
	if got := New(ZhHans).T(KeyMsgSaved, "a.sql", 3); got != "已保存 a.sql（3 行）" {
		t.Fatalf("T(zh) = %q", got)
	}
}

func TestDefaultLocaleIsEnglish(t *testing.T) {
	if Default != En {
		t.Fatalf("Default = %q, want %q", Default, En)
	}
	if got := New(Default).Locale(); got != En {
		t.Fatalf("New(Default).Locale() = %q", got)
	}
}

func TestTMissingKeyFallsBackToKey(t *testing.T) {
	if got := New(En).T(Key("no.such.key")); got != "no.such.key" {
		t.Fatalf("missing key should render itself, got %q", got)
	}
}

func TestUnknownLocaleFallsBackToDefault(t *testing.T) {
	if got := New(Locale("fr-FR")).Locale(); got != Default {
		t.Fatalf("Locale = %v, want %v", got, Default)
	}
}

func TestLocaleNamesAreEndonyms(t *testing.T) {
	if got := LocaleName(En); got != "English" {
		t.Fatalf("LocaleName(en) = %q", got)
	}
	if got := LocaleName(ZhHans); got != "中文" {
		t.Fatalf("LocaleName(zh-Hans) = %q", got)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]struct {
		want Locale
		ok   bool
	}{
		"en_US.UTF-8":      {En, true},
		"en":               {En, true},
		"EN_GB":            {En, true},
		"  en_GB.utf8  ":   {En, true},
		"en_US.UTF-8@euro": {En, true},
		"zh_CN.UTF-8":      {ZhHans, true},
		"zh-Hans-CN":       {ZhHans, true},
		"ZH":               {ZhHans, true},
		"C":                {"", false},
		"POSIX":            {"", false},
		"":                 {"", false},
		"fr_FR.UTF-8":      {"", false},
	}
	for in, want := range cases {
		got, ok := Normalize(in)
		if got != want.want || ok != want.ok {
			t.Errorf("Normalize(%q) = (%v, %v), want (%v, %v)", in, got, ok, want.want, want.ok)
		}
	}
}

func TestDetectPriority(t *testing.T) {
	env := map[string]string{"LANG": "en_US.UTF-8", "LC_MESSAGES": "zh_CN.UTF-8", "LC_ALL": ""}
	if got := Detect(func(k string) string { return env[k] }); got != ZhHans {
		t.Fatalf("LC_MESSAGES must win over LANG, got %v", got)
	}
	env["LC_ALL"] = "C" // unusable, keep falling back
	if got := Detect(func(k string) string { return env[k] }); got != ZhHans {
		t.Fatalf("LC_ALL=C must keep falling back, got %v", got)
	}
	env["LC_MESSAGES"] = ""
	if got := Detect(func(k string) string { return env[k] }); got != En {
		t.Fatalf("must fall back to LANG, got %v", got)
	}
	if got := Detect(func(string) string { return "" }); got != Default {
		t.Fatalf("no environment must fall back to Default, got %v", got)
	}
	if got := Detect(func(string) string { return "fr_FR.UTF-8" }); got != Default {
		t.Fatalf("unsupported locale must fall back to Default, got %v", got)
	}
}

func TestKeysAreSortedAndUnique(t *testing.T) {
	keys := Keys()
	seen := map[Key]bool{}
	for i, k := range keys {
		if seen[k] {
			t.Fatalf("duplicate key %q", k)
		}
		seen[k] = true
		if i > 0 && keys[i-1] > k {
			t.Fatalf("keys are not sorted: %q came after %q", k, keys[i-1])
		}
	}
	if len(keys) != len(allKeys) {
		t.Fatalf("Keys() returned %d keys, allKeys has %d", len(keys), len(allKeys))
	}
}

func TestKeysReturnsCopy(t *testing.T) {
	first := Keys()
	first[0] = Key("mutated")
	if Keys()[0] == Key("mutated") {
		t.Fatal("Keys() must not expose the internal slice")
	}
}

var _ Translator = (*Bundle)(nil)
