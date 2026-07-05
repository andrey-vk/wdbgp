//nolint:errcheck // test file, errors in cleanup intentionally ignored
package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestConcurrentReadDoesNotBlockOnInFlightWriteTransaction guards against
// MaxOpenConns(1) on the main DB (issue #24: the ipranges feed's 100k+ row
// sync transaction held the single shared connection for minutes, queuing
// every other request — dashboard, login, any DB-touching admin/user page —
// behind it in Go's connection pool). WAL mode allows a reader to proceed
// concurrently with an in-flight writer, but only if there's actually a
// second connection available to serve it.
func TestConcurrentReadDoesNotBlockOnInFlightWriteTransaction(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	writeDone := make(chan error, 1)

	go func() {
		writeDone <- s.Transaction(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO users(name, peer_ip, peer_asn) VALUES ('concurrent-write-test', '10.99.99.1', 65099)"); err != nil {
				return err
			}
			close(writeStarted)
			<-releaseWrite // hold the transaction open — simulates a long-running feed sync
			return nil
		})
	}()

	select {
	case <-writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("write transaction never started")
	}

	readDone := make(chan error, 1)
	go func() {
		var count int
		readDone <- s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
	}()

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("concurrent read failed: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		close(releaseWrite) // unblock the writer so the test doesn't hang forever
		t.Fatal("concurrent read blocked on an in-flight write transaction — MaxOpenConns is likely back down to 1")
	}

	close(releaseWrite)
	if err := <-writeDone; err != nil {
		t.Fatalf("write transaction failed: %v", err)
	}
}
