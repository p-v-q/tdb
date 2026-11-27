package driver

import "testing"

// TestTransactionControl pins the shared transaction-control classifier that
// every SQL driver routes user-typed BEGIN/COMMIT/ROLLBACK through. Getting this
// wrong is a data-safety bug: a statement the classifier misses runs through the
// connection pool, where the transaction it belongs to is rolled back between
// statements and every later statement silently commits.
func TestTransactionControl(t *testing.T) {
	cases := []struct {
		name      string
		statement string
		want      TxAction
	}{
		{"empty", "", TxNone},
		{"whitespace", "  \t\r\n ", TxNone},
		{"begin", "BEGIN", TxBegin},
		{"begin lowercase", "begin", TxBegin},
		{"begin semicolon", "BEGIN;", TxBegin},
		{"begin work", "BEGIN WORK", TxBegin},
		{"begin transaction", "BEGIN TRANSACTION", TxBegin},
		{"begin isolation level", "BEGIN ISOLATION LEVEL SERIALIZABLE", TxBegin},
		{"begin preceded by line comment", "-- start a transaction\nBEGIN;", TxBegin},
		{"begin preceded by hash comment", "# start a transaction\nBEGIN;", TxBegin},
		{"begin preceded by block comment", "/* lead */ BEGIN", TxBegin},
		{"begin preceded by nested comment", "/* a /* b */ c */ BEGIN", TxBegin},
		{"begin preceded by several comments", " -- one\n /* two */\t# three\n begin ; ", TxBegin},
		{"start transaction", "START TRANSACTION", TxBegin},
		{"start transaction lower", "start transaction read only", TxBegin},
		{"start transaction with comment", "START /* x */ TRANSACTION", TxBegin},
		{"commit", "COMMIT", TxCommit},
		{"commit semicolon", "commit;", TxCommit},
		{"commit work", "COMMIT WORK", TxCommit},
		{"end", "END", TxCommit},
		{"end transaction", "END TRANSACTION;", TxCommit},
		{"rollback", "ROLLBACK", TxRollback},
		{"rollback work", "ROLLBACK WORK", TxRollback},
		{"abort", "ABORT", TxRollback},
		{"rollback to savepoint is not a rollback", "ROLLBACK TO SAVEPOINT keep", TxNone},
		{"rollback to is not a rollback", "rollback to keep", TxNone},
		{"savepoint", "SAVEPOINT keep", TxSavepoint},
		{"savepoint semicolon", "SAVEPOINT keep;", TxSavepoint},
		{"release savepoint", "RELEASE SAVEPOINT keep", TxRelease},
		{"release", "RELEASE keep", TxRelease},
		{"select literally begin", "SELECT 'BEGIN'", TxNone},
		{"select", "SELECT 1;", TxNone},
		{"with cte", "WITH x AS (SELECT 1) SELECT * FROM x", TxNone},
		{"beginning is not begin", "BEGINNING", TxNone},
		{"committed is not commit", "COMMITTED", TxNone},
		{"start without transaction", "START", TxNone},
		{"unterminated comment", "/* BEGIN", TxNone},
		{"comment only", "/* BEGIN */", TxNone},
		{"update", "UPDATE t SET x = 1", TxNone},
		{"insert", "INSERT INTO t VALUES (1)", TxNone},
		{"create table", "CREATE TABLE t (id int)", TxNone},
	}
	for _, tc := range cases {
		if got := TransactionControl(tc.statement); got != tc.want {
			t.Errorf("%s: TransactionControl(%q) = %s, want %s",
				tc.name, tc.statement, got, tc.want)
		}
	}
}

func TestTxActionString(t *testing.T) {
	want := map[TxAction]string{
		TxNone:      "none",
		TxBegin:     "begin",
		TxCommit:    "commit",
		TxRollback:  "rollback",
		TxSavepoint: "savepoint",
		TxRelease:   "release",
	}
	for action, name := range want {
		if got := action.String(); got != name {
			t.Errorf("TxAction(%d).String() = %q, want %q", int(action), got, name)
		}
	}
}
