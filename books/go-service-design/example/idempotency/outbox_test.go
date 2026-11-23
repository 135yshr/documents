package idempotency

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// brokerFunc は、テストごとに振る舞いを変えられる Broker です。
type brokerFunc func(ctx context.Context, m Message) error

func (f brokerFunc) Publish(ctx context.Context, m Message) error { return f(ctx, m) }

// recordingBroker は、受け取ったメッセージの ID を記録して成功を返します。
type recordingBroker struct {
	mu  sync.Mutex
	ids []string
}

func (b *recordingBroker) Publish(_ context.Context, m Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ids = append(b.ids, m.ID)
	return nil
}

func (b *recordingBroker) received() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.ids...)
}

type permanentError struct{}

func (permanentError) Error() string   { return "スキーマ不正" }
func (permanentError) Permanent() bool { return true }

var errPublish = errors.New("ブローカーに接続できません")

// insertEvent は配送待ちのイベントを1件入れます。
func insertEvent(t *testing.T, id string) {
	t.Helper()
	body, err := json.Marshal(OrderConfirmedV1{EventID: id, OrderID: "order-" + id, CustomerID: "customer-1", TotalAmount: 1200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Exec(`
        INSERT INTO outbox (event_id, event_type, aggregate_id, payload)
        VALUES ($1, 'order.confirmed.v1', $2, $3::jsonb)`, id, "order-"+id, string(body)); err != nil {
		t.Fatal(err)
	}
}

type outboxRow struct {
	attempts    int
	leaseToken  sql.NullString
	leaseUntil  sql.NullTime
	published   sql.NullTime
	dead        sql.NullTime
	lastError   sql.NullString
	availableAt time.Time
}

func readOutbox(t *testing.T, id string) outboxRow {
	t.Helper()
	var r outboxRow
	err := testDB.QueryRow(`
        SELECT attempts, lease_token, lease_until, published_at, dead_at, last_error, available_at
        FROM outbox WHERE event_id = $1`, id).
		Scan(&r.attempts, &r.leaseToken, &r.leaseUntil, &r.published, &r.dead, &r.lastError, &r.availableAt)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// dbNow は DB の現在時刻です。時刻の判定をアプリケーション側の時計に依存させないために使います。
func dbNow(t *testing.T) time.Time {
	t.Helper()
	var now time.Time
	if err := testDB.QueryRow(`SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	return now
}

// waitReached は、ワーカーが目的の位置（Publish の中など）に到達するまで待ちます。
// 到達する前にワーカーが終了した場合は、そのエラーを報告します。
func waitReached(t *testing.T, reached <-chan struct{}, done <-chan error) {
	t.Helper()
	select {
	case <-reached:
	case err := <-done:
		t.Fatalf("ワーカーが目的の位置に到達する前に終了しました: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("ワーカーが目的の位置に到達しませんでした")
	}
}

// expireLease は、リースを持ったワーカーが停止して期限が切れた状態を作ります。
// 実際に30秒待つ代わりに、lease_until を過去にします。
func expireLease(t *testing.T, id string) {
	t.Helper()
	if _, err := testDB.Exec(`UPDATE outbox SET lease_until = now() - interval '1 second' WHERE event_id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

// 注文の確定と Outbox への記録が、同じトランザクションで確定することを確かめます。
func TestConfirmWritesOrderAndOutboxTogether(t *testing.T) {
	reset(t)
	ctx := context.Background()
	_, resp, err := CreateOrder(ctx, testDB, "user-1", "key-1", orderBody(t, "customer-1", 1200))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil {
		t.Fatal(err)
	}

	if err := Confirm(ctx, testDB, created.OrderID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := testDB.QueryRow(`SELECT status FROM orders WHERE id = $1`, created.OrderID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "confirmed" {
		t.Fatalf("status = %q; want confirmed", status)
	}
	var eventID, payloadEventID string
	if err := testDB.QueryRow(`SELECT event_id, payload->>'event_id' FROM outbox WHERE aggregate_id = $1`, created.OrderID).
		Scan(&eventID, &payloadEventID); err != nil {
		t.Fatal(err)
	}
	if eventID != payloadEventID {
		t.Fatalf("payload event_id = %q; want %q", payloadEventID, eventID)
	}

	// 2回目の確定は UPDATE が0行になり、Outbox にも記録しません。
	if err := Confirm(ctx, testDB, created.OrderID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second confirm err = %v; want sql.ErrNoRows", err)
	}
	if n := countRows(t, `SELECT count(*) FROM outbox`); n != 1 {
		t.Fatalf("outbox = %d; want 1", n)
	}
}

func TestDispatchOnePublishesAndMarksDelivered(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")
	broker := &recordingBroker{}

	ok, err := DispatchOne(ctx, testDB, broker)
	if err != nil || !ok {
		t.Fatalf("DispatchOne = (%v, %v); want (true, nil)", ok, err)
	}
	r := readOutbox(t, "evt-1")
	if !r.published.Valid || r.leaseToken.Valid || r.leaseUntil.Valid || r.attempts != 1 {
		t.Fatalf("row = %+v; want published, lease cleared, attempts 1", r)
	}
	// 配送済みの行は再び取り出しません。
	ok, err = DispatchOne(ctx, testDB, broker)
	if err != nil || ok {
		t.Fatalf("second DispatchOne = (%v, %v); want (false, nil)", ok, err)
	}
	if got := broker.received(); len(got) != 1 {
		t.Fatalf("published = %v; want 1 message", got)
	}
}

// 別のトランザクションが行ロックを持っている行は、待たずに飛ばします（FOR UPDATE SKIP LOCKED）。
func TestDispatchOneSkipsLockedRows(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")
	insertEvent(t, "evt-2")

	tx, err := testDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT event_id FROM outbox WHERE event_id = 'evt-1' FOR UPDATE`).Scan(&locked); err != nil {
		t.Fatal(err)
	}

	broker := &recordingBroker{}
	dispatchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	ok, err := DispatchOne(dispatchCtx, testDB, broker)
	if err != nil || !ok {
		t.Fatalf("DispatchOne = (%v, %v); want (true, nil)", ok, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("DispatchOne waited %v for the locked row", elapsed)
	}
	if got := broker.received(); len(got) != 1 || got[0] != "evt-2" {
		t.Fatalf("published = %v; want [evt-2]（ロック中の evt-1 は飛ばす）", got)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if ok, err := DispatchOne(ctx, testDB, broker); err != nil || !ok {
		t.Fatalf("DispatchOne after unlock = (%v, %v); want (true, nil)", ok, err)
	}
	if got := broker.received(); len(got) != 2 || got[1] != "evt-1" {
		t.Fatalf("published = %v; want [evt-2 evt-1]", got)
	}
}

// 複数のワーカーが同時に配送しても、障害がなければ各イベントを1回ずつ送ります。
func TestConcurrentDispatchersPublishEachEventOnce(t *testing.T) {
	reset(t)
	ctx := context.Background()
	const events = 30
	for i := range events {
		insertEvent(t, "evt-"+string(rune('a'+i/26))+string(rune('a'+i%26)))
	}
	broker := &recordingBroker{}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				ok, err := DispatchOne(ctx, testDB, broker)
				if err != nil {
					errs <- err
					return
				}
				if !ok {
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, id := range broker.received() {
		seen[id]++
	}
	if len(seen) != events {
		t.Fatalf("published %d distinct events; want %d", len(seen), events)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("event %s published %d times; want 1", id, n)
		}
	}
	if n := countRows(t, `SELECT count(*) FROM outbox WHERE published_at IS NULL`); n != 0 {
		t.Fatalf("unpublished = %d; want 0", n)
	}
}

// 送信中の行はリースされているため、別のワーカーは取り出しません。
func TestLeasedRowIsNotTakenByAnotherWorker(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")

	inPublish := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	// 途中で失敗しても、止めたワーカーを解放し、終了を待ってから次のテストへ進みます。
	t.Cleanup(func() { releaseOnce(); wg.Wait() })
	slow := brokerFunc(func(context.Context, Message) error {
		close(inPublish)
		<-release
		return nil
	})
	done := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := DispatchOne(ctx, testDB, slow)
		done <- err
	}()
	waitReached(t, inPublish, done)

	ok, err := DispatchOne(ctx, testDB, &recordingBroker{})
	if err != nil || ok {
		t.Fatalf("second worker = (%v, %v); want (false, nil)", ok, err)
	}
	releaseOnce()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// ワーカーが送信中に止まり、リースの期限が切れた場合です。
// 別のワーカーが同じイベントを取り直して送ります。旧ワーカーが後から戻っても状態は上書きできず、
// ErrLeaseLost を受け取ります。ただし旧ワーカーの送信自体は止められないため、ブローカーには2回届きます。
func TestExpiredLeaseIsRetakenAndOldWorkerLosesLease(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")

	var mu sync.Mutex
	var published []string
	record := func(m Message) {
		mu.Lock()
		defer mu.Unlock()
		published = append(published, m.ID)
	}
	inPublish := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	t.Cleanup(func() { releaseOnce(); wg.Wait() })
	stalled := brokerFunc(func(_ context.Context, m Message) error {
		close(inPublish)
		<-release
		record(m)
		return nil
	})
	oldDone := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := DispatchOne(ctx, testDB, stalled)
		oldDone <- err
	}()
	waitReached(t, inPublish, oldDone)
	expireLease(t, "evt-1")

	ok, err := DispatchOne(ctx, testDB, brokerFunc(func(_ context.Context, m Message) error {
		record(m)
		return nil
	}))
	if err != nil || !ok {
		t.Fatalf("new worker = (%v, %v); want (true, nil)", ok, err)
	}
	if r := readOutbox(t, "evt-1"); !r.published.Valid || r.attempts != 2 {
		t.Fatalf("row = %+v; want published with attempts 2", r)
	}

	releaseOnce()
	if err := <-oldDone; !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old worker err = %v; want ErrLeaseLost", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(published) != 2 || published[0] != "evt-1" || published[1] != "evt-1" {
		t.Fatalf("published = %v; want the same event twice (at-least-once)", published)
	}
}

// 旧ワーカーが戻ったとき、新しいワーカーがまだリースを持っている場合です。
// 旧ワーカーは配送済みにできず（リースのトークンが違うため）、行は新しいワーカーのリースのまま残ります。
func TestOldWorkerCannotMarkDeliveredWhileNewWorkerHoldsLease(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")

	oldIn, oldRelease := make(chan struct{}), make(chan struct{})
	newIn, newRelease := make(chan struct{}), make(chan struct{})
	oldOnce := sync.OnceFunc(func() { close(oldRelease) })
	newOnce := sync.OnceFunc(func() { close(newRelease) })
	var wg sync.WaitGroup
	t.Cleanup(func() { oldOnce(); newOnce(); wg.Wait() })

	oldDone := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := DispatchOne(ctx, testDB, brokerFunc(func(context.Context, Message) error {
			close(oldIn)
			<-oldRelease
			return nil
		}))
		oldDone <- err
	}()
	waitReached(t, oldIn, oldDone)
	expireLease(t, "evt-1")

	newDone := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := DispatchOne(ctx, testDB, brokerFunc(func(context.Context, Message) error {
			close(newIn)
			<-newRelease
			return nil
		}))
		newDone <- err
	}()
	waitReached(t, newIn, newDone)

	oldOnce()
	if err := <-oldDone; !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old worker err = %v; want ErrLeaseLost", err)
	}
	if r := readOutbox(t, "evt-1"); r.published.Valid || !r.leaseToken.Valid {
		t.Fatalf("row = %+v; want still leased by the new worker and not published", r)
	}

	newOnce()
	if err := <-newDone; err != nil {
		t.Fatalf("new worker err = %v; want nil", err)
	}
	if r := readOutbox(t, "evt-1"); !r.published.Valid || r.attempts != 2 {
		t.Fatalf("row = %+v; want published with attempts 2", r)
	}
}

// 送信中に呼び出し元の Context が終わった場合、リースを残したまま戻ります。
// リースの期限が切れるまでは取り出さず、切れた後に再試行します。
func TestCanceledDispatchLeavesLeaseUntilExpiry(t *testing.T) {
	reset(t)
	insertEvent(t, "evt-1")
	ctx, cancel := context.WithCancel(context.Background())
	waitCancel := brokerFunc(func(c context.Context, _ Message) error {
		cancel()
		<-c.Done()
		return c.Err()
	})
	ok, err := DispatchOne(ctx, testDB, waitCancel)
	if !ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("DispatchOne = (%v, %v); want (true, context.Canceled)", ok, err)
	}
	r := readOutbox(t, "evt-1")
	if !r.leaseToken.Valid || r.published.Valid || r.dead.Valid || r.attempts != 1 {
		t.Fatalf("row = %+v; want leased, not published, not dead, attempts 1", r)
	}

	broker := &recordingBroker{}
	if ok, err := DispatchOne(context.Background(), testDB, broker); err != nil || ok {
		t.Fatalf("before expiry = (%v, %v); want (false, nil)", ok, err)
	}
	expireLease(t, "evt-1")
	if ok, err := DispatchOne(context.Background(), testDB, broker); err != nil || !ok {
		t.Fatalf("after expiry = (%v, %v); want (true, nil)", ok, err)
	}
	if got := broker.received(); len(got) != 1 {
		t.Fatalf("published = %v; want 1 message", got)
	}
}

// 送信に失敗した場合、リースを外し、試行回数を記録し、backoff 後に再試行できる状態にします。
func TestPublishFailureSchedulesRetry(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")
	failing := brokerFunc(func(context.Context, Message) error { return errPublish })

	before := dbNow(t)
	ok, err := DispatchOne(ctx, testDB, failing)
	if err != nil || !ok {
		t.Fatalf("DispatchOne = (%v, %v); want (true, nil)", ok, err)
	}
	after := dbNow(t)
	r := readOutbox(t, "evt-1")
	if r.attempts != 1 || r.published.Valid || r.dead.Valid || r.leaseToken.Valid || r.leaseUntil.Valid {
		t.Fatalf("row = %+v; want attempts 1, not published, not dead, lease cleared", r)
	}
	if !r.lastError.Valid || r.lastError.String != "publish failed" {
		t.Fatalf("last_error = %v; want \"publish failed\"", r.lastError)
	}
	// 1回目の失敗後の待ち時間は 0〜1秒です。DispatchOne の前後の DB 時刻で挟み、
	// テストが読み取るまでの遅れに左右されないようにします。
	if r.availableAt.Before(before) || r.availableAt.After(after.Add(time.Second)) {
		t.Fatalf("available_at = %v; want within [%v, %v + 1s]", r.availableAt, before, after)
	}

	// 再試行の時刻になれば、もう一度取り出して送ります。
	if _, err := testDB.Exec(`UPDATE outbox SET available_at = now() WHERE event_id = 'evt-1'`); err != nil {
		t.Fatal(err)
	}
	broker := &recordingBroker{}
	if ok, err := DispatchOne(ctx, testDB, broker); err != nil || !ok {
		t.Fatalf("retry = (%v, %v); want (true, nil)", ok, err)
	}
	if r := readOutbox(t, "evt-1"); !r.published.Valid || r.attempts != 2 {
		t.Fatalf("row = %+v; want published with attempts 2", r)
	}
}

// 再試行の待ち時間の上限は1秒から倍々に増えて1分で止まり、実際の値は 0 から上限までの乱数です。
func TestDelayIsFullJitterWithExponentialCap(t *testing.T) {
	wantCap := func(attempt int) time.Duration {
		c := time.Second
		for i := 1; i < attempt; i++ {
			c *= 2
			if c >= time.Minute {
				return time.Minute
			}
		}
		return c
	}
	for attempt := 1; attempt <= 12; attempt++ {
		limit := wantCap(attempt)
		var maxSeen time.Duration
		for range 500 {
			d := delay(attempt)
			if d < 0 || d > limit {
				t.Fatalf("delay(%d) = %v; want 0..%v", attempt, d, limit)
			}
			maxSeen = max(maxSeen, d)
		}
		// 上限の半分を一度も超えない確率は 2^-500 で、乱数が上限付近まで広がっていることの確認です。
		if maxSeen <= limit/2 {
			t.Fatalf("delay(%d) max = %v; want values spread up to %v", attempt, maxSeen, limit)
		}
	}
	if got := wantCap(7); got != time.Minute {
		t.Fatalf("cap at attempt 7 = %v; want 1m", got)
	}
}

// 10回目の失敗で dead_at を設定し、配送待ちの対象から外します。9回目の失敗ではまだ外しません。
func TestDispatchOneMarksDeadAfterMaxAttempts(t *testing.T) {
	reset(t)
	ctx := context.Background()
	failing := brokerFunc(func(context.Context, Message) error { return errPublish })

	for _, tc := range []struct {
		priorAttempts int
		wantDead      bool
	}{
		{maxAttempts - 2, false},
		{maxAttempts - 1, true},
	} {
		reset(t)
		insertEvent(t, "evt-1")
		if _, err := testDB.Exec(`UPDATE outbox SET attempts = $1 WHERE event_id = 'evt-1'`, tc.priorAttempts); err != nil {
			t.Fatal(err)
		}
		if ok, err := DispatchOne(ctx, testDB, failing); err != nil || !ok {
			t.Fatalf("DispatchOne = (%v, %v); want (true, nil)", ok, err)
		}
		r := readOutbox(t, "evt-1")
		if r.attempts != tc.priorAttempts+1 || r.dead.Valid != tc.wantDead || r.published.Valid {
			t.Fatalf("prior %d: row = %+v; want attempts %d, dead %v", tc.priorAttempts, r, tc.priorAttempts+1, tc.wantDead)
		}
		if tc.wantDead {
			// 再試行の時刻が来ても、dead の行は取り出しません。
			if _, err := testDB.Exec(`UPDATE outbox SET available_at = now() - interval '1 second' WHERE event_id = 'evt-1'`); err != nil {
				t.Fatal(err)
			}
			if ok, err := DispatchOne(ctx, testDB, &recordingBroker{}); err != nil || ok {
				t.Fatalf("dead row taken = (%v, %v); want (false, nil)", ok, err)
			}
		}
	}
}

// 再試行しても解決しないエラーは、1回目の失敗で dead_at を設定します。
func TestPermanentErrorMarksDeadImmediately(t *testing.T) {
	reset(t)
	insertEvent(t, "evt-1")
	permanent := brokerFunc(func(context.Context, Message) error { return permanentError{} })
	if ok, err := DispatchOne(context.Background(), testDB, permanent); err != nil || !ok {
		t.Fatalf("DispatchOne = (%v, %v); want (true, nil)", ok, err)
	}
	if r := readOutbox(t, "evt-1"); !r.dead.Valid || r.attempts != 1 {
		t.Fatalf("row = %+v; want dead after 1 attempt", r)
	}
}

// 原因を直した後、担当者が dead_at を外して同じ ID で再投入すると、同じ ID のまま送ります。
// 再投入の SQL は本書の運用例を表すもので、DispatchOne の機能ではありません。
func TestDeadEventCanBeRequeuedWithSameID(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")
	if _, err := DispatchOne(ctx, testDB, brokerFunc(func(context.Context, Message) error { return permanentError{} })); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.Exec(`UPDATE outbox SET dead_at = NULL, available_at = now() WHERE event_id = 'evt-1'`); err != nil {
		t.Fatal(err)
	}
	broker := &recordingBroker{}
	if ok, err := DispatchOne(ctx, testDB, broker); err != nil || !ok {
		t.Fatalf("requeued = (%v, %v); want (true, nil)", ok, err)
	}
	if got := broker.received(); len(got) != 1 || got[0] != "evt-1" {
		t.Fatalf("published = %v; want [evt-1]", got)
	}
}

// Outbox から送ったイベントを Consumer が処理する流れで、同じイベントが2回届いても加算は1回です。
func TestOutboxToConsumerWithDuplicateDelivery(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")
	var mu sync.Mutex
	var delivered []Message
	consumer := brokerFunc(func(ctx context.Context, m Message) error {
		mu.Lock()
		delivered = append(delivered, m)
		mu.Unlock()
		e, err := decodeOrderConfirmed(m)
		if err != nil {
			return err
		}
		_, err = ApplyPoints(ctx, testDB, e)
		return err
	})
	if ok, err := DispatchOne(ctx, testDB, consumer); err != nil || !ok {
		t.Fatalf("DispatchOne = (%v, %v); want (true, nil)", ok, err)
	}
	// 成功 ACK が失われて再送された場合を、同じメッセージをもう一度渡して再現します。
	if err := consumer.Publish(ctx, delivered[0]); err != nil {
		t.Fatal(err)
	}
	var points int64
	if err := testDB.QueryRow(`SELECT points FROM point_accounts WHERE customer_id = 'customer-1'`).Scan(&points); err != nil {
		t.Fatal(err)
	}
	if points != 12 {
		t.Fatalf("points = %d; want 12（加算は1回）", points)
	}
}

func TestDecodeOrderConfirmedRejectsMismatchedID(t *testing.T) {
	body, _ := json.Marshal(OrderConfirmedV1{EventID: "evt-2", CustomerID: "customer-1", TotalAmount: 100})
	if _, err := decodeOrderConfirmed(Message{ID: "evt-1", Type: "order.confirmed.v1", Payload: body}); err == nil {
		t.Fatal("payload の event_id とメッセージ ID が違うイベントを受け入れました")
	}
	if _, err := decodeOrderConfirmed(Message{ID: "evt-2", Type: "order.cancelled.v1", Payload: body}); err == nil {
		t.Fatal("未対応のイベント種別を受け入れました")
	}
}

// 同じイベントを2つの Consumer が同時に処理する場合です。
// 先行が処理済みの記録と加算を commit する前に、後続が同じ記録の INSERT で待つことを確かめます。
// 先行が commit すると後続は0行になり、加算しません。
func TestConcurrentApplyPointsAppliesOnce(t *testing.T) {
	reset(t)
	ctx, cancel := context.WithCancel(context.Background())
	e := OrderConfirmedV1{EventID: "evt-1", CustomerID: "customer-1", TotalAmount: 1200}

	reached := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	t.Cleanup(func() {
		releaseOnce()
		cancel()
		wg.Wait()
	})
	type applyResult struct {
		applied bool
		err     error
	}
	aDone := make(chan applyResult, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		applied, err := applyPoints(ctx, testDB, applyHooks{beforeCommit: func() error {
			close(reached)
			<-release
			return nil
		}}, e)
		aDone <- applyResult{applied, err}
	}()
	select {
	case <-reached:
	case a := <-aDone:
		t.Fatalf("Consumer A が commit 前の位置に到達しませんでした: %v", a.err)
	}

	bDone := make(chan applyResult, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		applied, err := ApplyPoints(ctx, testDB, e)
		bDone <- applyResult{applied, err}
	}()
	waitForLockWaiter(t)
	select {
	case b := <-bDone:
		t.Fatalf("Consumer B が先行の commit 前に返りました: %+v", b)
	default:
	}

	releaseOnce()
	a, b := <-aDone, <-bDone
	if a.err != nil || !a.applied {
		t.Fatalf("A = %+v; want applied", a)
	}
	if b.err != nil || b.applied {
		t.Fatalf("B = %+v; want not applied (duplicate)", b)
	}
	var points int64
	if err := testDB.QueryRow(`SELECT points FROM point_accounts WHERE customer_id = 'customer-1'`).Scan(&points); err != nil {
		t.Fatal(err)
	}
	if points != 12 {
		t.Fatalf("points = %d; want 12", points)
	}
	if n := countRows(t, `SELECT count(*) FROM processed_events`); n != 1 {
		t.Fatalf("processed_events = %d; want 1", n)
	}
}

// 先行の Consumer がロールバックした場合、処理済みの記録も残らず、待っていた後続が加算します。
func TestConcurrentApplyPointsAfterRollbackAppliesFollower(t *testing.T) {
	reset(t)
	ctx, cancel := context.WithCancel(context.Background())
	e := OrderConfirmedV1{EventID: "evt-1", CustomerID: "customer-1", TotalAmount: 1200}

	reached := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	var wg sync.WaitGroup
	t.Cleanup(func() {
		releaseOnce()
		cancel()
		wg.Wait()
	})
	aErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := applyPoints(ctx, testDB, applyHooks{beforeCommit: func() error {
			close(reached)
			<-release
			return errors.New("先行の Consumer の処理が失敗した想定")
		}}, e)
		aErr <- err
	}()
	waitReached(t, reached, aErr)

	type applyResult struct {
		applied bool
		err     error
	}
	bDone := make(chan applyResult, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		applied, err := ApplyPoints(ctx, testDB, e)
		bDone <- applyResult{applied, err}
	}()
	waitForLockWaiter(t)

	releaseOnce()
	if err := <-aErr; err == nil {
		t.Fatal("Consumer A は失敗するはずです")
	}
	if b := <-bDone; b.err != nil || !b.applied {
		t.Fatalf("B = %+v; want applied", b)
	}
	var points int64
	if err := testDB.QueryRow(`SELECT points FROM point_accounts WHERE customer_id = 'customer-1'`).Scan(&points); err != nil {
		t.Fatal(err)
	}
	if points != 12 {
		t.Fatalf("points = %d; want 12", points)
	}
}

// 業務更新の後に失敗した場合、処理済みの記録も一緒にロールバックされ、再配信で処理できます。
func TestApplyPointsRollsBackProcessedRecordWithBusinessUpdate(t *testing.T) {
	reset(t)
	ctx := context.Background()
	e := OrderConfirmedV1{EventID: "evt-1", CustomerID: "customer-1", TotalAmount: 1200}
	_, err := applyPoints(ctx, testDB, applyHooks{beforeCommit: func() error {
		return errors.New("commit 前の失敗")
	}}, e)
	if err == nil {
		t.Fatal("失敗するはずです")
	}
	if n := countRows(t, `SELECT count(*) FROM processed_events`); n != 0 {
		t.Fatalf("processed_events = %d; want 0（業務更新と一緒にロールバック）", n)
	}
	if n := countRows(t, `SELECT count(*) FROM point_accounts`); n != 0 {
		t.Fatalf("point_accounts = %d; want 0", n)
	}
	if applied, err := ApplyPoints(ctx, testDB, e); err != nil || !applied {
		t.Fatalf("redelivery = (%v, %v); want (true, nil)", applied, err)
	}
}

// commit は成功したが、応答が呼び出し元に届かなかった場合です。
// アプリケーションの中で commit 後にエラーを返して再現します（ネットワークの切断そのものは再現していません）。
// 同じキーと本文で再試行すると、保存済みのレスポンスを返し、注文は増えません。
func TestCommittedButResponseLostRetryReturnsStoredResponse(t *testing.T) {
	reset(t)
	ctx := context.Background()
	body := orderBody(t, "customer-1", 1200)
	errResponseLost := errors.New("応答の送信前に接続が切れた想定")

	_, _, err := createOrder(ctx, testDB, createHooks{afterCommit: func() error { return errResponseLost }}, "user-1", "key-1", body)
	if !errors.Is(err, errResponseLost) {
		t.Fatalf("first err = %v; want the simulated response loss", err)
	}
	var stored []byte
	if err := testDB.QueryRow(`SELECT response_body FROM http_requests WHERE idem_key = 'key-1'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}

	status, resp, err := CreateOrder(ctx, testDB, "user-1", "key-1", body)
	if err != nil || status != 201 || string(resp) != string(stored) {
		t.Fatalf("retry = (%d, %s, %v); want (201, %s, nil)", status, resp, err, stored)
	}
	if n := countRows(t, `SELECT count(*) FROM orders`); n != 1 {
		t.Fatalf("orders = %d; want 1", n)
	}
	// 同じキーで本文が違えば、保存済みのハッシュと一致しないため拒否します。
	if _, _, err := CreateOrder(ctx, testDB, "user-1", "key-1", orderBody(t, "customer-1", 9999)); !errors.Is(err, ErrKeyReuse) {
		t.Fatalf("different body err = %v; want ErrKeyReuse", err)
	}
}

// commit されなかった場合（commit 前の失敗）は、同じキーで再試行すると新しく処理します。
func TestNotCommittedRetryProcessesRequest(t *testing.T) {
	reset(t)
	ctx := context.Background()
	body := orderBody(t, "customer-1", 1200)
	_, _, err := createOrder(ctx, testDB, createHooks{beforeCommit: func() error { return errors.New("commit 前の失敗") }}, "user-1", "key-1", body)
	if err == nil {
		t.Fatal("失敗するはずです")
	}
	if n := countRows(t, `SELECT count(*) FROM http_requests`); n != 0 {
		t.Fatalf("http_requests = %d; want 0（キーの予約もロールバック）", n)
	}
	status, _, err := CreateOrder(ctx, testDB, "user-1", "key-1", body)
	if err != nil || status != 201 {
		t.Fatalf("retry = (%d, %v); want (201, nil)", status, err)
	}
	if n := countRows(t, `SELECT count(*) FROM orders`); n != 1 {
		t.Fatalf("orders = %d; want 1", n)
	}
}

// リースが切れたまま試行回数の上限に達した行は、再送せずに dead_at を設定します。
// 結果が不明のまま、上限を超えて試行が続くことはありません。
func TestExpiredLeaseAtMaxAttemptsIsDeadLettered(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")
	if _, err := testDB.Exec(`
        UPDATE outbox SET attempts = $1, lease_token = 'stale', lease_until = now() - interval '1 second'
        WHERE event_id = 'evt-1'`, maxAttempts); err != nil {
		t.Fatal(err)
	}
	broker := &recordingBroker{}
	ok, err := DispatchOne(ctx, testDB, broker)
	if err != nil || ok {
		t.Fatalf("DispatchOne = (%v, %v); want (false, nil)", ok, err)
	}
	if got := broker.received(); len(got) != 0 {
		t.Fatalf("published = %v; want none（上限に達した行は再送しない）", got)
	}
	r := readOutbox(t, "evt-1")
	if !r.dead.Valid || r.leaseToken.Valid || r.attempts != maxAttempts || !r.lastError.Valid {
		t.Fatalf("row = %+v; want dead with the lease cleared and attempts unchanged", r)
	}
}

// 上限回数目の試行で呼び出し元の Context が終わり、結果を記録できなかった場合です。
// リースが切れた後に取り出す前に dead_at を設定し、11回目の試行を始めません。
func TestCanceledAtMaxAttemptsIsDeadLetteredAfterLeaseExpiry(t *testing.T) {
	reset(t)
	insertEvent(t, "evt-1")
	if _, err := testDB.Exec(`UPDATE outbox SET attempts = $1 WHERE event_id = 'evt-1'`, maxAttempts-1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waitCancel := brokerFunc(func(c context.Context, _ Message) error {
		cancel()
		<-c.Done()
		return c.Err()
	})
	if ok, err := DispatchOne(ctx, testDB, waitCancel); !ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("DispatchOne = (%v, %v); want (true, context.Canceled)", ok, err)
	}
	if r := readOutbox(t, "evt-1"); r.attempts != maxAttempts || r.dead.Valid || !r.leaseToken.Valid {
		t.Fatalf("row = %+v; want attempt %d recorded, leased, not dead yet", r, maxAttempts)
	}

	expireLease(t, "evt-1")
	broker := &recordingBroker{}
	if ok, err := DispatchOne(context.Background(), testDB, broker); err != nil || ok {
		t.Fatalf("after expiry = (%v, %v); want (false, nil)", ok, err)
	}
	if got := broker.received(); len(got) != 0 {
		t.Fatalf("published = %v; want none", got)
	}
	if r := readOutbox(t, "evt-1"); !r.dead.Valid || r.attempts != maxAttempts {
		t.Fatalf("row = %+v; want dead with attempts %d", r, maxAttempts)
	}
}

// 上限回数の途中でリースが切れた行は、これまでどおり取り直して再送します。
func TestExpiredLeaseBelowMaxAttemptsIsStillRetaken(t *testing.T) {
	reset(t)
	insertEvent(t, "evt-1")
	if _, err := testDB.Exec(`
        UPDATE outbox SET attempts = $1, lease_token = 'stale', lease_until = now() - interval '1 second'
        WHERE event_id = 'evt-1'`, maxAttempts-2); err != nil {
		t.Fatal(err)
	}
	broker := &recordingBroker{}
	if ok, err := DispatchOne(context.Background(), testDB, broker); err != nil || !ok {
		t.Fatalf("DispatchOne = (%v, %v); want (true, nil)", ok, err)
	}
	if r := readOutbox(t, "evt-1"); !r.published.Valid || r.dead.Valid || r.attempts != maxAttempts-1 {
		t.Fatalf("row = %+v; want published with attempts %d", r, maxAttempts-1)
	}
}

// 再投入で dead_at を外しても attempts は戻らないため、次の失敗でもう一度 dead_at が設定されます。
// 再び試行の余地を与えるかどうかは、運用の判断です（attempts を 0 に戻す更新は別に行います）。
func TestRequeuedEventKeepsAttemptsUnlessResetByOperator(t *testing.T) {
	reset(t)
	ctx := context.Background()
	insertEvent(t, "evt-1")
	if _, err := testDB.Exec(`UPDATE outbox SET attempts = $1, dead_at = now() WHERE event_id = 'evt-1'`, maxAttempts); err != nil {
		t.Fatal(err)
	}
	requeue := func(resetAttempts bool) {
		t.Helper()
		if _, err := testDB.Exec(`
            UPDATE outbox SET dead_at = NULL, available_at = now(),
                              attempts = CASE WHEN $1 THEN 0 ELSE attempts END
            WHERE event_id = 'evt-1'`, resetAttempts); err != nil {
			t.Fatal(err)
		}
	}
	failing := brokerFunc(func(context.Context, Message) error { return errPublish })

	requeue(false)
	if ok, err := DispatchOne(ctx, testDB, failing); err != nil || !ok {
		t.Fatalf("DispatchOne = (%v, %v); want (true, nil)", ok, err)
	}
	if r := readOutbox(t, "evt-1"); !r.dead.Valid || r.attempts != maxAttempts+1 {
		t.Fatalf("row = %+v; want dead again after one more failed attempt", r)
	}

	requeue(true)
	if ok, err := DispatchOne(ctx, testDB, failing); err != nil || !ok {
		t.Fatalf("DispatchOne = (%v, %v); want (true, nil)", ok, err)
	}
	if r := readOutbox(t, "evt-1"); r.dead.Valid || r.attempts != 1 {
		t.Fatalf("row = %+v; want a fresh retry budget (attempts 1, not dead)", r)
	}
}
