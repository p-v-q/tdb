package mysql

// tx_control_test.go pins idempotent transaction-control routing without a
// server: once the driver owns a transaction, another raw BEGIN must be handled
// locally rather than sent through the transaction to MySQL/MariaDB.

import (
	"context"
	"database/sql"
	"testing"
)

func TestTransactionControlBeginIsIdempotent(t *testing.T) {
	ctx := context.Background()
	// A non-nil transaction is enough for this path: no transaction method may
	// run, because a second BEGIN is a local no-op. A nil pool makes any accidental
	// server round trip fail immediately instead of reaching a real database.
	tx := &sql.Tx{}
	c := &conn{tx: tx}
	if err := c.Begin(ctx); err != nil {
		t.Fatalf("Begin on an open transaction: %v", err)
	}
	for _, statement := range []string{"BEGIN", "START TRANSACTION"} {
		res, handled, err := c.transactionControl(ctx, statement)
		if err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
		if !handled || res == nil {
			t.Fatalf("%s: handled/res = %v/%+v, want a locally handled success", statement, handled, res)
		}
		if c.currentTx() != tx || !c.InTx() {
			t.Fatalf("%s replaced or closed the existing transaction", statement)
		}
	}
}
