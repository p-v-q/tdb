package postgres

// tx_control_test.go pins idempotent transaction-control routing without a
// server: a raw BEGIN inside a driver-managed transaction is handled locally.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// existingTx satisfies pgx.Tx without a server. The embedded interface is never
// called: an idempotent BEGIN only observes that a transaction already exists.
type existingTx struct{ pgx.Tx }

func TestTransactionControlBeginIsIdempotent(t *testing.T) {
	ctx := context.Background()
	tx := &existingTx{}
	c := &conn{tx: tx}
	if err := c.Begin(ctx); err != nil {
		t.Fatalf("Begin on an open transaction: %v", err)
	}
	res, handled, err := c.transactionControl(ctx, "BEGIN")
	if err != nil {
		t.Fatalf("BEGIN: %v", err)
	}
	if !handled || res == nil {
		t.Fatalf("handled/res = %v/%+v, want a locally handled success", handled, res)
	}
	if c.currentTx() != tx || !c.InTx() {
		t.Fatal("BEGIN replaced or closed the existing transaction")
	}
}
