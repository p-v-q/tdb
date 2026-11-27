package ui

// async_state_regression_test.go covers the asynchronous session boundaries:
// metadata, connection opening and connection shutdown must all belong to the
// request that started them, never merely to the last message delivered.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/p-v-q/tdb/catalog"
	"github.com/p-v-q/tdb/driver"
	"github.com/p-v-q/tdb/settings"
)

func namedCatalog(name string) *catalog.Catalog {
	return &catalog.Catalog{Schemas: []*catalog.Schema{{
		Name: name,
		Tables: []*catalog.Table{{
			Schema: name, Name: "items", Type: catalog.TypeTable,
		}},
	}}}
}

func TestCatalogRefreshCannotOverwriteAnotherConnection(t *testing.T) {
	a := newFakeConn()
	a.cat = namedCatalog("alpha")
	b := newFakeConn()
	b.cat = namedCatalog("beta")

	m := sized("")
	m.connOpen = a
	m.conn = settings.Connection{Name: "alpha", Driver: fakeDriverName}
	m.cat = a.cat
	m.cfg.Provider = a

	// Refresh alpha, then switch to beta before alpha's response arrives.
	cmd := m.refreshCatalog()
	m.applyConnection(settings.Connection{Name: "beta", Driver: fakeDriverName}, b, b.cat)
	m = send(m, cmd())

	if m.cat != b.cat {
		t.Fatalf("late alpha metadata replaced beta's catalog: %#v", m.cat)
	}
}

type catalogSequenceConn struct {
	*fakeConn
	catalogs []*catalog.Catalog
}

func (c *catalogSequenceConn) Catalog(context.Context) (*catalog.Catalog, error) {
	if len(c.catalogs) == 0 {
		return nil, errors.New("no catalog prepared")
	}
	cat := c.catalogs[0]
	c.catalogs = c.catalogs[1:]
	return cat, nil
}

func TestNewestCatalogRefreshWinsOnTheSameConnection(t *testing.T) {
	conn := &catalogSequenceConn{
		fakeConn: newFakeConn(),
		catalogs: []*catalog.Catalog{namedCatalog("new"), namedCatalog("old")},
	}
	m := sized("")
	m.connOpen = conn
	m.conn = settings.Connection{Name: "alpha", Driver: fakeDriverName}

	oldCmd := m.refreshCatalog()
	newCmd := m.refreshCatalog()

	m = send(m, newCmd())
	newest := m.cat
	m = send(m, oldCmd())
	if m.cat != newest || m.cat.Schemas[0].Name != "new" {
		t.Fatalf("an older refresh replaced the newest snapshot: %#v", m.cat)
	}
}

type openGate struct {
	started chan string
	release chan struct{}
	once    sync.Once
}

func newOpenGate() *openGate {
	return &openGate{started: make(chan string, 2), release: make(chan struct{})}
}

func (g *openGate) unblock() { g.once.Do(func() { close(g.release) }) }

func waitOpened(t *testing.T, g *openGate, database string) {
	t.Helper()
	select {
	case got := <-g.started:
		if got != database {
			t.Fatalf("opened database %q, want %q", got, database)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("open %q did not start", database)
	}
}

func connectionMessage(t *testing.T, messages []tea.Msg) connMsg {
	t.Helper()
	for _, msg := range messages {
		if conn, ok := msg.(connMsg); ok {
			return conn
		}
	}
	t.Fatalf("messages = %v, want connMsg", messages)
	return connMsg{}
}

func TestLastIssuedConnectionWinsWhenResponsesArriveOutOfOrder(t *testing.T) {
	gateA, gateB := newOpenGate(), newOpenGate()
	t.Cleanup(gateA.unblock)
	t.Cleanup(gateB.unblock)
	connA, connB := newFakeConn(), newFakeConn()
	useFakeDriver(t, func(_ context.Context, cfg driver.Config) (driver.Connection, error) {
		database := cfg.Options["database"]
		gate := gateA
		conn := connA
		if database == "b" {
			gate, conn = gateB, connB
		}
		gate.started <- database
		// Deliberately finish even after cancellation: a driver that ignores its
		// context must still have its stale successful connection disposed of.
		<-gate.release
		return conn, nil
	})

	m := sized("")
	cmdA := m.connectToAs(settings.Connection{Name: "alpha", Driver: fakeDriverName, Database: "a"}, false, "")
	cmdB := m.connectToAs(settings.Connection{Name: "beta", Driver: fakeDriverName, Database: "b"}, false, "")
	messagesA, messagesB := make(chan []tea.Msg, 1), make(chan []tea.Msg, 1)
	go func() { messagesA <- collectCmds(cmdA) }()
	go func() { messagesB <- collectCmds(cmdB) }()
	waitOpened(t, gateA, "a")
	waitOpened(t, gateB, "b")

	// The newer request completes first and becomes live.
	gateB.unblock()
	select {
	case messages := <-messagesB:
		m = send(m, connectionMessage(t, messages))
	case <-time.After(2 * time.Second):
		t.Fatal("beta open did not finish")
	}
	if m.connOpen != connB || m.conn.Name != "beta" {
		t.Fatalf("live session = %q/%p, want beta/%p", m.conn.Name, m.connOpen, connB)
	}

	// Alpha completes later: it must be closed, not installed over beta.
	gateA.unblock()
	select {
	case messages := <-messagesA:
		next, closeCmd := m.Update(connectionMessage(t, messages))
		m = next.(Model)
		collectCmds(closeCmd)
	case <-time.After(2 * time.Second):
		t.Fatal("alpha open did not finish")
	}
	if m.connOpen != connB || m.conn.Name != "beta" {
		t.Fatalf("late alpha replaced beta: live = %q/%p", m.conn.Name, m.connOpen)
	}
	if connA.closeCount() != 1 {
		t.Fatalf("stale alpha connection was closed %d times, want once", connA.closeCount())
	}
}

func TestStaleReconnectFailureDoesNotDropTheNewSession(t *testing.T) {
	gateA, gateB := newOpenGate(), newOpenGate()
	t.Cleanup(gateA.unblock)
	t.Cleanup(gateB.unblock)
	connA, connB := newFakeConn(), newFakeConn()
	useFakeDriver(t, func(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
		database := cfg.Options["database"]
		gate, conn := gateA, connA
		if database == "b" {
			gate, conn = gateB, connB
		}
		gate.started <- database
		select {
		case <-gate.release:
			if database == "a" {
				return nil, errors.New("alpha disappeared")
			}
			return conn, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	m := sized("")
	cmdA := m.connectToAs(settings.Connection{Name: "alpha", Driver: fakeDriverName, Database: "a"}, true, "")
	cmdB := m.connectToAs(settings.Connection{Name: "beta", Driver: fakeDriverName, Database: "b"}, false, "")
	messagesA, messagesB := make(chan []tea.Msg, 1), make(chan []tea.Msg, 1)
	go func() { messagesA <- collectCmds(cmdA) }()
	go func() { messagesB <- collectCmds(cmdB) }()
	waitOpened(t, gateA, "a")
	waitOpened(t, gateB, "b")
	gateB.unblock()
	select {
	case messages := <-messagesB:
		m = send(m, connectionMessage(t, messages))
	case <-time.After(2 * time.Second):
		t.Fatal("beta open did not finish")
	}
	status := m.status.message

	gateA.unblock()
	select {
	case messages := <-messagesA:
		m = send(m, connectionMessage(t, messages))
	case <-time.After(2 * time.Second):
		t.Fatal("alpha failure did not arrive")
	}
	if m.connOpen != connB || m.cat == nil {
		t.Fatalf("stale reconnect failure dropped beta: conn=%p cat=%#v", m.connOpen, m.cat)
	}
	if m.status.message != status {
		t.Fatalf("stale failure changed status from %q to %q", status, m.status.message)
	}
}

func TestDisconnectInvalidatesAnOpeningConnection(t *testing.T) {
	gate := newOpenGate()
	t.Cleanup(gate.unblock)
	conn := newFakeConn()
	useFakeDriver(t, func(_ context.Context, cfg driver.Config) (driver.Connection, error) {
		gate.started <- cfg.Options["database"]
		<-gate.release
		return conn, nil
	})

	m := sized("")
	m.conn = settings.Connection{Name: "before", Driver: fakeDriverName}
	cmd := m.connectToAs(settings.Connection{Name: "alpha", Driver: fakeDriverName, Database: "a"}, false, "")
	messages := make(chan []tea.Msg, 1)
	go func() { messages <- collectCmds(cmd) }()
	waitOpened(t, gate, "a")

	disconnectCmd := m.disconnect()
	_ = disconnectCmd // closing a detached connection is deliberately asynchronous
	gate.unblock()
	select {
	case messages := <-messages:
		next, closeCmd := m.Update(connectionMessage(t, messages))
		m = next.(Model)
		collectCmds(closeCmd)
	case <-time.After(2 * time.Second):
		t.Fatal("open did not finish")
	}
	if m.connOpen != nil || m.conn.Name != "before" {
		t.Fatalf("a disconnected session was reopened by a stale response: %#v", m.conn)
	}
	if conn.closeCount() != 1 {
		t.Fatalf("the stale opened connection was closed %d times, want once", conn.closeCount())
	}
}

func TestNewConnectionRequestCancelsThePreviousHandshake(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	fallback := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(fallback) }) }
	t.Cleanup(unblock)
	useFakeDriver(t, func(ctx context.Context, cfg driver.Config) (driver.Connection, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			canceled <- struct{}{}
			return nil, ctx.Err()
		case <-fallback:
			return nil, errors.New("released by test")
		}
	})

	m := sized("")
	t.Cleanup(func() {
		if m.connCancel != nil {
			m.connCancel()
		}
	})
	cmd := m.connectToAs(settings.Connection{Name: "alpha", Driver: fakeDriverName, Database: "a"}, false, "")
	go func() { _ = collectCmds(cmd) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("open did not start")
	}

	m.connectToAs(settings.Connection{Name: "beta", Driver: fakeDriverName, Database: "b"}, false, "")
	select {
	case <-canceled:
	case <-time.After(500 * time.Millisecond):
		unblock()
		t.Fatal("the superseded handshake context was not canceled")
	}
}

type blockingCloseConn struct {
	*fakeConn
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingCloseConn() *blockingCloseConn {
	return &blockingCloseConn{
		fakeConn: newFakeConn(), started: make(chan struct{}), release: make(chan struct{}),
	}
}

func (c *blockingCloseConn) Close() error {
	c.once.Do(func() { close(c.started) })
	<-c.release
	return c.fakeConn.Close()
}

func TestDisconnectDoesNotBlockTheUIOnConnectionClose(t *testing.T) {
	conn := newBlockingCloseConn()
	t.Cleanup(func() {
		select {
		case <-conn.release:
		default:
			close(conn.release)
		}
	})
	m := sized("")
	m.connOpen = conn
	m.conn = settings.Connection{Name: "alpha", Driver: fakeDriverName}
	queryCtx, cancelQuery := context.WithCancel(context.Background())
	t.Cleanup(cancelQuery)
	m.queryCancel = cancelQuery
	m.queryRunning = true
	m.querySeq, m.queryGen = 1, 1

	type result struct {
		model Model
		cmd   tea.Cmd
	}
	returned := make(chan result, 1)
	go func() {
		next := m
		cmd := next.disconnect()
		returned <- result{model: next, cmd: cmd}
	}()

	select {
	case got := <-returned:
		m = got.model
		select {
		case <-queryCtx.Done():
		default:
			t.Fatal("disconnect did not cancel the running query before closing")
		}
		if m.queryRunning || m.queryGen == 1 {
			t.Fatalf("query state was not invalidated: running=%v gen=%d", m.queryRunning, m.queryGen)
		}
		if got.cmd == nil {
			t.Fatal("disconnect must return a background close command")
		}
		closed := make(chan struct{})
		go func() { _ = collectCmds(got.cmd); close(closed) }()
		select {
		case <-conn.started:
		case <-time.After(2 * time.Second):
			t.Fatal("background close did not start")
		}
		close(conn.release)
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Fatal("background close did not finish")
		}
	case <-time.After(2 * time.Second):
		close(conn.release)
		<-returned
		t.Fatal("disconnect blocked the UI goroutine while Close waited")
	}
}

func TestDisconnectMakesLateQueryOutcomeStale(t *testing.T) {
	conn := newFakeConn()
	m := sized("SELECT 1")
	m.connOpen = conn
	m.querySeq, m.queryGen = 7, 7
	before := m.session.result.stmt

	collectCmds(m.disconnect())
	m = send(m, queryMsg{
		gen: 7, sess: m.session, open: conn, stmt: "late result",
		res: &driver.Result{Columns: []driver.ResultColumn{{Name: "late"}}},
	})
	if m.session.result.stmt != before {
		t.Fatalf("a late query outcome changed the disconnected session: %q", m.session.result.stmt)
	}
}

type deadlineTxConn struct {
	*fakeConn
	deadline bool
}

func (c *deadlineTxConn) Begin(context.Context) error  { return nil }
func (c *deadlineTxConn) Commit(context.Context) error { return nil }
func (c *deadlineTxConn) InTx() bool                   { return false }
func (c *deadlineTxConn) Rollback(ctx context.Context) error {
	_, c.deadline = ctx.Deadline()
	return nil
}

func TestDisconnectRollbackUsesABoundedContext(t *testing.T) {
	conn := &deadlineTxConn{fakeConn: newFakeConn()}
	m := sized("")
	m.connOpen = conn
	m.conn = settings.Connection{Name: "alpha", Driver: fakeDriverName}
	m.txOpen = true

	cmd := m.disconnect()
	collectCmds(cmd)
	if !conn.deadline {
		t.Fatal("rollback during disconnect must have a deadline")
	}
	if conn.closeCount() != 1 {
		t.Fatalf("connection closed %d times, want once", conn.closeCount())
	}
}
