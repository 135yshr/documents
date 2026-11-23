package idempotency

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/lib/pq"
)

// testDB は実 PostgreSQL への接続です。
//
// IDEMPOTENCY_TEST_DSN が設定されていればその DB を使います。
// 設定されていなければ embedded-postgres で一時的な PostgreSQL を起動します。
//
// どちらの場合も、テスト専用のスキーマを作ってその中だけで作業し、終了時に削除します。
// テストは各ケースの前にテーブルを TRUNCATE するため、指定された DB に同名のテーブルが
// あっても、その行を消さないようにするためです。
var testDB *sql.DB

func TestMain(m *testing.M) {
	os.Exit(runWithDatabase(m))
}

func runWithDatabase(m *testing.M) int {
	dsn := os.Getenv("IDEMPOTENCY_TEST_DSN")
	if dsn == "" {
		port, err := freePort()
		if err != nil {
			panic(err)
		}
		dir, err := os.MkdirTemp("", "idempotency-pg-")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)

		cfg := embeddedpostgres.DefaultConfig().
			Version(embeddedpostgres.V16).
			Port(port).
			Database("idempotency").
			Username("postgres").
			Password("postgres").
			RuntimePath(filepath.Join(dir, "runtime")).
			DataPath(filepath.Join(dir, "data")).
			CachePath(filepath.Join(os.TempDir(), "idempotency-pg-cache"))
		pg := embeddedpostgres.NewDatabase(cfg)
		if err := pg.Start(); err != nil {
			panic(err)
		}
		defer pg.Stop()
		dsn = withSSLDisabled(cfg.GetConnectionURL())
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		panic(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("idempotency_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		panic(err)
	}
	defer admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)

	db, err := sql.Open("postgres", withSearchPath(dsn, schema))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	if _, err := db.Exec(Schema + ConsumerSchema + OutboxSchema); err != nil {
		panic(err)
	}
	testDB = db
	return m.Run()
}

// withSSLDisabled は、ローカルの一時 PostgreSQL に SSL なしで接続するように URL を補います。
// lib/pq は sslmode を省略すると require として扱います。
func withSSLDisabled(url string) string {
	if strings.Contains(url, "sslmode=") {
		return url
	}
	if strings.Contains(url, "?") {
		return url + "&sslmode=disable"
	}
	return url + "?sslmode=disable"
}

// withSearchPath は、接続が使うスキーマをテスト専用のものに限定します。
// search_path に public を含めないため、未修飾のテーブル名は専用スキーマだけを指します。
func withSearchPath(dsn, schema string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "search_path=" + schema
	}
	return dsn + " search_path=" + schema
}

func freePort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

func reset(t *testing.T) {
	t.Helper()
	if _, err := testDB.Exec(`TRUNCATE http_requests, orders, processed_events, point_accounts, outbox`); err != nil {
		t.Fatal(err)
	}
}

func orderBody(t *testing.T, customer string, amount int64) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"customer_id": customer, "total_amount": amount})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testDB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// waitForLockWaiter は、別の接続がロック待ちになるまで待ちます。
// 並行リクエストが先行トランザクションの終了を待っていることを確認するために使います。
func waitForLockWaiter(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		n := countRows(t, `SELECT count(*) FROM pg_stat_activity
            WHERE datname = current_database() AND wait_event_type = 'Lock'`)
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("並行リクエストがロック待ちになりませんでした")
}

type createResult struct {
	status int
	body   []byte
	err    error
}

func TestSameKeySamePayloadReturnsStoredResponse(t *testing.T) {
	reset(t)
	ctx := context.Background()
	body := orderBody(t, "customer-1", 1200)

	s1, r1, err := CreateOrder(ctx, testDB, "user-1", "key-1", body)
	if err != nil {
		t.Fatal(err)
	}
	// 1回目のレスポンスがクライアントに届かなかった想定で、同じキーで再試行します。
	s2, r2, err := CreateOrder(ctx, testDB, "user-1", "key-1", body)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != 201 || s2 != 201 {
		t.Fatalf("status = %d, %d; want 201, 201", s1, s2)
	}
	if !bytes.Equal(r1, r2) {
		t.Fatalf("retry body = %s; want %s", r2, r1)
	}
	if n := countRows(t, `SELECT count(*) FROM orders`); n != 1 {
		t.Fatalf("orders = %d; want 1", n)
	}
}

func TestSameKeyDifferentPayloadIsRejected(t *testing.T) {
	reset(t)
	ctx := context.Background()
	if _, _, err := CreateOrder(ctx, testDB, "user-1", "key-1", orderBody(t, "customer-1", 1200)); err != nil {
		t.Fatal(err)
	}
	_, _, err := CreateOrder(ctx, testDB, "user-1", "key-1", orderBody(t, "customer-1", 9999))
	if !errors.Is(err, ErrKeyReuse) {
		t.Fatalf("err = %v; want ErrKeyReuse", err)
	}
	if n := countRows(t, `SELECT count(*) FROM orders`); n != 1 {
		t.Fatalf("orders = %d; want 1", n)
	}
}

func TestSameKeyDifferentPrincipalIsIndependent(t *testing.T) {
	reset(t)
	ctx := context.Background()
	body := orderBody(t, "customer-1", 1200)
	if _, _, err := CreateOrder(ctx, testDB, "user-1", "key-1", body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CreateOrder(ctx, testDB, "user-2", "key-1", body); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, `SELECT count(*) FROM orders`); n != 2 {
		t.Fatalf("orders = %d; want 2", n)
	}
}

// 先行リクエストが commit した後に、同じキーの並行リクエストが届く場合です。
// 後続は先行の commit を待ち、保存済みのレスポンスを返すはずです。
func TestConcurrentSameKeyAfterCommitReturnsStoredResponse(t *testing.T) {
	reset(t)
	ctx, cancel := context.WithCancel(context.Background())
	body := orderBody(t, "customer-1", 1200)

	reached := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	// 途中で失敗しても、commit 前で止めた先行リクエストとそのトランザクションを残しません。
	// 残ると、次のテストの TRUNCATE がロック待ちになります。
	t.Cleanup(func() {
		releaseOnce()
		cancel()
		wg.Wait()
	})
	aDone := make(chan createResult, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		h := createHooks{beforeCommit: func() error {
			close(reached)
			<-release
			return nil
		}}
		s, r, err := createOrder(ctx, testDB, h, "user-1", "key-1", body)
		aDone <- createResult{s, r, err}
	}()
	select {
	case <-reached:
	case a := <-aDone:
		t.Fatalf("先行リクエストが commit 前の位置に到達しませんでした: %v", a.err)
	}

	bDone := make(chan createResult, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		s, r, err := CreateOrder(ctx, testDB, "user-1", "key-1", body)
		bDone <- createResult{s, r, err}
	}()
	waitForLockWaiter(t)
	select {
	case <-bDone:
		t.Fatal("先行トランザクションの commit 前に後続が返りました")
	default:
	}

	releaseOnce()
	a := <-aDone
	b := <-bDone
	if a.err != nil || b.err != nil {
		t.Fatalf("errors: a=%v b=%v", a.err, b.err)
	}
	if !bytes.Equal(a.body, b.body) || b.status != 201 {
		t.Fatalf("b = (%d, %s); want the stored response of a (201, %s)", b.status, b.body, a.body)
	}
	if n := countRows(t, `SELECT count(*) FROM orders`); n != 1 {
		t.Fatalf("orders = %d; want 1", n)
	}
}

// 先行リクエストが rollback した場合、後続は同じキーを使って新しく処理できます。
func TestConcurrentSameKeyAfterRollbackProcessesFollower(t *testing.T) {
	reset(t)
	ctx, cancel := context.WithCancel(context.Background())
	body := orderBody(t, "customer-1", 1200)

	reached := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	// 途中で失敗しても、commit 前で止めた先行リクエストとそのトランザクションを残しません。
	// 残ると、次のテストの TRUNCATE がロック待ちになります。
	t.Cleanup(func() {
		releaseOnce()
		cancel()
		wg.Wait()
	})
	aDone := make(chan createResult, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		h := createHooks{beforeCommit: func() error {
			close(reached)
			<-release
			return errors.New("先行リクエストの業務処理が失敗した想定")
		}}
		s, r, err := createOrder(ctx, testDB, h, "user-1", "key-1", body)
		aDone <- createResult{s, r, err}
	}()
	select {
	case <-reached:
	case a := <-aDone:
		t.Fatalf("先行リクエストが commit 前の位置に到達しませんでした: %v", a.err)
	}

	bDone := make(chan createResult, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		s, r, err := CreateOrder(ctx, testDB, "user-1", "key-1", body)
		bDone <- createResult{s, r, err}
	}()
	waitForLockWaiter(t)

	releaseOnce()
	a := <-aDone
	b := <-bDone
	if a.err == nil {
		t.Fatal("先行リクエストは失敗するはずです")
	}
	if b.err != nil || b.status != 201 {
		t.Fatalf("follower = (%d, %v); want (201, nil)", b.status, b.err)
	}
	if n := countRows(t, `SELECT count(*) FROM orders`); n != 1 {
		t.Fatalf("orders = %d; want 1（先行の注文はロールバックされるはず）", n)
	}
}

// READ COMMITTED と REPEATABLE READ で、先行の commit 後に届いた同じキーの INSERT ... ON CONFLICT DO NOTHING が
// どう振る舞うかを比べます。本書の処理は READ COMMITTED を前提としています。
func TestDuplicateKeyInsertAfterCommitByIsolationLevel(t *testing.T) {
	cases := []struct {
		name      string
		isolation sql.IsolationLevel
		// wantSerializationFailure が true なら、INSERT が SQLSTATE 40001 で失敗することを期待します。
		wantSerializationFailure bool
	}{
		{"READ COMMITTED", sql.LevelReadCommitted, false},
		{"REPEATABLE READ", sql.LevelRepeatableRead, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reset(t)
			ctx := context.Background()
			hash := []byte("hash")

			txA, err := testDB.BeginTx(ctx, &sql.TxOptions{Isolation: tc.isolation})
			if err != nil {
				t.Fatal(err)
			}
			bFinished := make(chan struct{})
			bStarted := false
			// 途中で失敗しても txA を残しません。commit 済みなら Rollback は何もしません。
			// txA を終えると待機中の B も進むため、B の終了も待ちます。
			defer func() {
				_ = txA.Rollback()
				if bStarted {
					<-bFinished
				}
			}()
			if _, err := txA.ExecContext(ctx, `
                INSERT INTO http_requests (principal, operation, idem_key, request_hash, response_status, response_body)
                VALUES ('user-1', 'create-order', 'key-1', $1, 201, '{"order_id":"a"}')`, hash); err != nil {
				t.Fatal(err)
			}

			type readResult struct {
				status int
				rows   int64
				insErr error
				selErr error
			}
			bDone := make(chan readResult, 1)
			bStarted = true
			go func() {
				defer close(bFinished)
				txB, err := testDB.BeginTx(ctx, &sql.TxOptions{Isolation: tc.isolation})
				if err != nil {
					bDone <- readResult{insErr: err}
					return
				}
				defer txB.Rollback()
				// B の最初の文は INSERT です。REPEATABLE READ のスナップショットはこの文の開始時に取られます。
				res, err := txB.ExecContext(ctx, `
                    INSERT INTO http_requests (principal, operation, idem_key, request_hash)
                    VALUES ('user-1', 'create-order', 'key-1', $1) ON CONFLICT DO NOTHING`, hash)
				if err != nil {
					bDone <- readResult{insErr: err}
					return
				}
				n, _ := res.RowsAffected()
				var status int
				err = txB.QueryRowContext(ctx, `
                    SELECT response_status FROM http_requests
                    WHERE principal = 'user-1' AND operation = 'create-order' AND idem_key = 'key-1'`).Scan(&status)
				bDone <- readResult{status: status, rows: n, selErr: err}
			}()

			waitForLockWaiter(t)
			if err := txA.Commit(); err != nil {
				t.Fatal(err)
			}
			r := <-bDone

			if tc.wantSerializationFailure {
				var pqErr *pq.Error
				if !errors.As(r.insErr, &pqErr) || pqErr.Code != "40001" {
					t.Fatalf("insert err = %v; want SQLSTATE 40001 (serialization failure)", r.insErr)
				}
				return
			}
			if r.insErr != nil {
				t.Fatalf("insert err = %v; want nil", r.insErr)
			}
			if r.rows != 0 {
				t.Fatalf("insert rows = %d; want 0 (conflict)", r.rows)
			}
			if r.selErr != nil || r.status != 201 {
				t.Fatalf("read = (%d, %v); want (201, nil)", r.status, r.selErr)
			}
		})
	}
}

// 同じ order.confirmed.v1 を2回受け取っても、ポイントは一度だけ加算されます。
func TestApplyPointsIgnoresRedeliveredEvent(t *testing.T) {
	reset(t)
	ctx := context.Background()
	e := OrderConfirmedV1{EventID: "evt-1", CustomerID: "customer-1", TotalAmount: 1200}

	applied, err := ApplyPoints(ctx, testDB, e)
	if err != nil || !applied {
		t.Fatalf("first delivery = (%v, %v); want (true, nil)", applied, err)
	}
	applied, err = ApplyPoints(ctx, testDB, e)
	if err != nil || applied {
		t.Fatalf("redelivery = (%v, %v); want (false, nil)", applied, err)
	}
	var points int64
	if err := testDB.QueryRow(`SELECT points FROM point_accounts WHERE customer_id = 'customer-1'`).Scan(&points); err != nil {
		t.Fatal(err)
	}
	if points != 12 {
		t.Fatalf("points = %d; want 12", points)
	}
}

func TestApplyPointsRejectsInvalidEvent(t *testing.T) {
	reset(t)
	_, err := ApplyPoints(context.Background(), testDB, OrderConfirmedV1{CustomerID: "customer-1"})
	if err == nil {
		t.Fatal("event id が空のイベントを受け入れました")
	}
}
