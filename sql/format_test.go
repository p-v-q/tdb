package sql

import (
	"reflect"
	"strings"
	"testing"
)

func formatKeywords() Keywords {
	return NewKeywords(
		"select", "distinct", "all", "from", "where", "and", "or", "not",
		"order", "by", "asc", "desc", "group", "having", "limit", "offset", "join", "left",
		"right", "inner", "outer", "full", "cross", "natural", "on", "using",
		"insert", "into", "values", "update", "set", "delete", "returning",
		"union", "intersect", "except", "as", "true", "false", "null", "case",
		"when", "then", "else", "end", "create", "table", "primary", "key",
		"int", "text", "boolean", "with", "between",
	)
}

func TestFormatSelect(t *testing.T) {
	in := "select id, name from users where id = 1 and active = true order by name desc;"
	want := "SELECT\n  id,\n  name\nFROM users\nWHERE id = 1\n  AND active = TRUE\nORDER BY name DESC;"
	if got := Format(in, formatKeywords()); got != want {
		t.Fatalf("Format() =\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatJoinsAndGrouping(t *testing.T) {
	in := "select u.id, count(*) as n from users u left join orders o on o.user_id = u.id group by u.id order by n desc limit 10"
	want := "SELECT\n  u.id,\n  count(*) AS n\nFROM users u\nLEFT JOIN orders o\n  ON o.user_id = u.id\nGROUP BY u.id\nORDER BY n DESC\nLIMIT 10"
	if got := Format(in, formatKeywords()); got != want {
		t.Fatalf("Format() =\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatUpdateInsertDelete(t *testing.T) {
	update := "update users set name = 'x', active = false where id = 1"
	wantUpdate := "UPDATE users\nSET name = 'x',\n  active = FALSE\nWHERE id = 1"
	if got := Format(update, formatKeywords()); got != wantUpdate {
		t.Fatalf("update =\n%s\nwant:\n%s", got, wantUpdate)
	}

	insert := "insert into users (id, name) values (1, 'a'), (2, 'b')"
	wantInsert := "INSERT INTO users(id, name)\nVALUES (1, 'a'), (2, 'b')"
	if got := Format(insert, formatKeywords()); got != wantInsert {
		t.Fatalf("insert =\n%s\nwant:\n%s", got, wantInsert)
	}

	del := "delete from users where active = false"
	wantDel := "DELETE FROM users\nWHERE active = FALSE"
	if got := Format(del, formatKeywords()); got != wantDel {
		t.Fatalf("delete =\n%s\nwant:\n%s", got, wantDel)
	}
}

func TestFormatPreservesStringsIdentifiersAndComments(t *testing.T) {
	in := `select 'from where', "select" as s -- keep this
from t /* block */ where x <> 1`
	want := "SELECT\n  'from where',\n  \"select\" AS s -- keep this\nFROM t /* block */\nWHERE x <> 1"
	if got := Format(in, formatKeywords()); got != want {
		t.Fatalf("Format() =\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatKeepsArraysAndBetweenTogether(t *testing.T) {
	in := "select arr[1, 2], x from t where x between 1 and 3 and y = 2"
	want := "SELECT\n  arr[1, 2],\n  x\nFROM t\nWHERE x BETWEEN 1 AND 3\n  AND y = 2"
	if got := Format(in, formatKeywords()); got != want {
		t.Fatalf("Format() =\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatIsIdempotent(t *testing.T) {
	in := "select a,b from t where a=1 and b between 1 and 2 union all select c,d from u;"
	once := Format(in, formatKeywords())
	twice := Format(once, formatKeywords())
	if once != twice {
		t.Fatalf("formatting twice changed the text:\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
}

func TestFormatKeepsTrailingNewline(t *testing.T) {
	in := "select 1;\n"
	got := Format(in, formatKeywords())
	if got != "SELECT\n  1;\n" {
		t.Fatalf("Format() = %q", got)
	}
}

// ---- compound statements ----

// compoundKeywords extends the plain SELECT/UPDATE set with the words a
// compound statement needs in its head.
func compoundKeywords() Keywords {
	kw := formatKeywords()
	for _, w := range []string{
		"trigger", "after", "before", "begin", "function", "procedure",
		"returns", "return", "language", "for", "each", "row", "do",
	} {
		kw[strings.ToUpper(w)] = true
	}
	return kw
}

const triggerScript = `create trigger users_audit after insert on users begin
  insert into audit_log(msg) values (new.email);
  update stats set n = n + 1;
end;`

func TestFormatKeepsTriggerBodyVerbatim(t *testing.T) {
	want := "CREATE TRIGGER users_audit AFTER INSERT\n" +
		"  ON users BEGIN\n" +
		"  insert into audit_log(msg) values (new.email);\n" +
		"  update stats set n = n + 1;\n" +
		"END;"
	if got := Format(triggerScript, compoundKeywords()); got != want {
		t.Fatalf("Format() =\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatKeepsDollarQuotedBodyVerbatim(t *testing.T) {
	in := "create function f() returns int as $$\nselect 1;\nselect 2;\n$$ language sql;"
	want := "CREATE FUNCTION f() RETURNS INT AS $$\nselect 1;\nselect 2;\n$$ LANGUAGE sql;"
	if got := Format(in, compoundKeywords()); got != want {
		t.Fatalf("Format() =\n%s\nwant:\n%s", got, want)
	}

	in = "do $$ begin insert into t values ('a;b'); end $$;"
	want = "DO $$ begin insert into t values ('a;b'); end $$;"
	if got := Format(in, compoundKeywords()); got != want {
		t.Fatalf("Format() = %q, want %q", got, want)
	}
}

func TestFormatKeepsUnterminatedBlockBodyRaw(t *testing.T) {
	// A block whose END is not typed yet cannot be delimited, so everything
	// after BEGIN is left exactly as the user wrote it.
	in := "create trigger t after insert on users begin insert into a values (1);"
	want := "CREATE TRIGGER t AFTER INSERT\n" +
		"  ON users BEGIN insert into a values (1);"
	if got := Format(in, compoundKeywords()); got != want {
		t.Fatalf("Format() =\n%s\nwant:\n%s", got, want)
	}
}

// TestFormatMysqlEndCaseKeepsBodyVerbatim is the formatter counterpart of the
// splitter's END CASE regression: matchBlockEnd must treat the CASE of
// `END CASE` as the closer's tail, pair the procedure's END correctly, keep the
// body verbatim (including the inner `end case;`) and format the statement
// after the body normally.
func TestFormatMysqlEndCaseKeepsBodyVerbatim(t *testing.T) {
	in := "create procedure p()\nbegin\n  case x\n    when 1 then select 1;\n  end case;\n  select 2;\nend;\nselect 3;"
	got := Format(in, compoundKeywords())
	if !strings.Contains(got, "end case;") {
		t.Fatalf("body lost END CASE:\n%s", got)
	}
	if !strings.HasSuffix(got, "SELECT\n  3;") {
		t.Fatalf("trailing statement not formatted outside the body:\n%s", got)
	}
	before := Statements(in, compoundKeywords())
	after := Statements(got, compoundKeywords())
	if len(before) != 2 || len(after) != 2 {
		t.Fatalf("statement count changed: before=%d after=%d (%+v)", len(before), len(after), after)
	}
}

func TestFormatCompoundIsIdempotent(t *testing.T) {
	fixtures := []string{
		triggerScript,
		"create function f() returns int as $$\nbegin\n  return 1;\nend;\n$$ language plpgsql;",
		"do $$ begin insert into t values ('a;b'); end $$;",
		"create procedure p() begin if x then insert into a values (1); end if; end;",
		"select case when a then 1 else 2 end from t;",
	}
	for _, in := range fixtures {
		once := Format(in, compoundKeywords())
		twice := Format(once, compoundKeywords())
		if once != twice {
			t.Errorf("formatting twice changed the text:\nfirst:\n%s\nsecond:\n%s", once, twice)
		}
	}
}

// ---- multi-byte characters ----

// TestFormatMultibyteBlockBodyVerbatim pins that the verbatim copy of a
// compound-statement body slices runes, not bytes: the body below contains a
// Chinese literal and sits after three lines whose byte offsets drift away
// from their rune offsets. A byte-based slice would overshoot the END token
// and copy "end;" (or part of the trailing SELECT) into the body.
func TestFormatMultibyteBlockBodyVerbatim(t *testing.T) {
	script := "create trigger t after insert on users begin\n" +
		"  insert into audit_log(msg) values ('中文🚀');\n" +
		"  update stats set n = n + 1;\n" +
		"end;\nselect 1"
	want := "CREATE TRIGGER t AFTER INSERT\n" +
		"  ON users BEGIN\n" +
		"  insert into audit_log(msg) values ('中文🚀');\n" +
		"  update stats set n = n + 1;\n" +
		"END;\n\n" +
		"SELECT\n  1"
	got := Format(script, compoundKeywords())
	if got != want {
		t.Fatalf("Format() =\n%s\nwant:\n%s", got, want)
	}
	if twice := Format(got, compoundKeywords()); twice != want {
		t.Fatalf("formatting the result again changed it:\n%s", twice)
	}
}

// TestFormatMultibyteLiteralsAndComments covers the non-verbatim path: a
// multi-byte string literal in the select list and a multi-byte line comment
// must survive formatting unchanged.
func TestFormatMultibyteLiteralsAndComments(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "chinese and emoji string literal",
			in:   "select '中文🚀', id from t",
			want: "SELECT\n  '中文🚀',\n  id\nFROM t",
		},
		{
			name: "chinese line comment preserved",
			in:   "select 1 -- 中文注释\nfrom t",
			want: "SELECT\n  1 -- 中文注释\nFROM t",
		},
	}
	for _, tc := range cases {
		if got := Format(tc.in, compoundKeywords()); got != tc.want {
			t.Errorf("%s: Format() =\n%s\nwant:\n%s", tc.name, got, tc.want)
		}
	}
}

// sigWords reduces a statement to the upper-cased text of its significant
// tokens — the part of a statement formatting is allowed to change.
func sigWords(text string) []string {
	var out []string
	for _, tok := range Lex(text, compoundKeywords()) {
		if !tok.Kind.IsSignificant() {
			continue
		}
		out = append(out, strings.ToUpper(tok.Text))
	}
	return out
}

func TestFormatPreservesStatementBoundaries(t *testing.T) {
	// Formatting must not move a semicolon across a statement boundary:
	// re-splitting the formatted text yields the same statements, token for
	// token, as splitting the original.
	fixtures := []string{
		triggerScript,
		"create function f() returns int as $$\nbegin\n  return 1;\nend;\n$$ language plpgsql;\nselect 1;",
		"do $$ begin insert into t values ('a;b'); end $$;\nselect 2;",
		"create procedure p() begin while x do insert into b values (2); end while; end;\nselect 3",
		"select case when a then 1 else 2 end from t;\nselect 4;",
		"begin; insert into t values (1); commit;",
	}
	for _, in := range fixtures {
		before := Statements(in, compoundKeywords())
		after := Statements(Format(in, compoundKeywords()), compoundKeywords())
		if len(before) != len(after) {
			t.Errorf("statement count changed:\nin:\n%s\nbefore: %d, after: %d (%+v)",
				in, len(before), len(after), after)
			continue
		}
		for i := range before {
			if !reflect.DeepEqual(sigWords(before[i].Text), sigWords(after[i].Text)) {
				t.Errorf("statement %d changed tokens:\nbefore: %s\nafter:  %s",
					i, before[i].Text, after[i].Text)
			}
		}
	}
}
