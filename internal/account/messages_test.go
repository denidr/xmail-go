package account

import (
	"context"
	"fmt"
	"testing"

	"xmail/internal/mailer"
)

// newTestCache returns a Repository (for accounts, to satisfy foreign
// keys) and a MessageCache over the same database.
func newTestCache(t *testing.T) (*Repository, *MessageCache) {
	t.Helper()
	repo := newTestRepo(t)
	return repo, NewMessageCache(repo.db)
}

func TestMessageCache_UpsertAndList(t *testing.T) {
	ctx := context.Background()
	repo, cache := newTestCache(t)

	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	key := CacheKey{AccountID: created.ID, Protocol: "imap", Folder: "INBOX"}

	msgs := []mailer.Message{
		{UID: "1", Folder: "INBOX", Subject: "First", From: "a@x.com", IsRead: true},
		{UID: "2", Folder: "INBOX", Subject: "Second", From: "b@x.com", IsRead: false},
	}
	if err := cache.Upsert(ctx, key, msgs, 0); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	got, err := cache.list(ctx, key, Window{Limit: 10})
	if err != nil {
		t.Fatalf("list() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list() len = %d, want 2", len(got))
	}
	if got[0].UID != "1" || got[1].UID != "2" {
		t.Fatalf("list() order = [%s, %s], want [1, 2] (msgs[] order must be preserved — see TestMessageCache_UpsertPreservesOrder)", got[0].UID, got[1].UID)
	}

	// Upserting the same UID again with a changed field must update in
	// place, not duplicate.
	msgs[1].Subject = "Second (updated)"
	msgs[1].IsRead = true
	if err := cache.Upsert(ctx, key, msgs, 0); err != nil {
		t.Fatalf("Upsert() (update) error = %v", err)
	}
	got, err = cache.list(ctx, key, Window{Limit: 10})
	if err != nil {
		t.Fatalf("list() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("list() after re-upsert len = %d, want 2 (no duplicates)", len(got))
	}
}

// TestMessageCache_UpsertPreservesOrder is the regression test for a
// real bug found by external code review: Upsert shared one
// `fetched_at` timestamp across an entire batch, so
// "ORDER BY fetched_at DESC, rowid DESC" fell back to rowid DESC to
// break same-batch ties — but SQLite assigns rowids in insertion
// order, and msgs is inserted newest-first (matching Fetch's own
// order), so the newest message got the *smallest* rowid in the
// batch and rowid DESC put it *last*. Net effect: GET /messages was
// newest-first only on a cold cache, and silently flipped to
// oldest-first as soon as the cache was warm. Fixed via an explicit
// sort_rank column (migrations/0003_message_sort_rank.sql) set from
// each message's position in msgs, independent of rowid/insert order.
func TestMessageCache_UpsertPreservesOrder(t *testing.T) {
	ctx := context.Background()
	repo, cache := newTestCache(t)

	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	key := CacheKey{AccountID: created.ID, Protocol: "imap", Folder: "INBOX"}

	// Newest-first, exactly as mailer.Fetcher.Fetch returns it.
	newestFirst := []mailer.Message{
		{UID: "3", Folder: "INBOX", Subject: "Newest"},
		{UID: "2", Folder: "INBOX", Subject: "Middle"},
		{UID: "1", Folder: "INBOX", Subject: "Oldest"},
	}
	if err := cache.Upsert(ctx, key, newestFirst, 0); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	assertOrder := func(t *testing.T, label string) {
		t.Helper()
		got, err := cache.list(ctx, key, Window{Limit: 10})
		if err != nil {
			t.Fatalf("%s: list() error = %v", label, err)
		}
		if len(got) != 3 {
			t.Fatalf("%s: len = %d, want 3", label, len(got))
		}
		gotUIDs := []string{got[0].UID, got[1].UID, got[2].UID}
		wantUIDs := []string{"3", "2", "1"}
		for i := range wantUIDs {
			if gotUIDs[i] != wantUIDs[i] {
				t.Errorf("%s: order = %v, want %v (newest-first)", label, gotUIDs, wantUIDs)
				break
			}
		}
	}
	assertOrder(t, "after first insert (cold cache)")

	// Re-upsert the exact same batch (simulating a second live Fetch
	// hitting existing rows via ON CONFLICT DO UPDATE) — order must
	// survive this too, which is exactly what the old rowid-based
	// tie-break got wrong.
	if err := cache.Upsert(ctx, key, newestFirst, 0); err != nil {
		t.Fatalf("Upsert() (2nd, warm cache) error = %v", err)
	}
	assertOrder(t, "after second upsert (warm cache)")
}

func TestMessageCache_ExistingUIDs(t *testing.T) {
	ctx := context.Background()
	repo, cache := newTestCache(t)

	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	key := CacheKey{AccountID: created.ID, Protocol: "imap", Folder: "INBOX"}
	if err := cache.Upsert(ctx, key, []mailer.Message{
		{UID: "1", Folder: "INBOX"}, {UID: "2", Folder: "INBOX"},
	}, 0); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	existing, err := cache.ExistingUIDs(ctx, key)
	if err != nil {
		t.Fatalf("ExistingUIDs() error = %v", err)
	}
	if !existing["1"] || !existing["2"] || existing["3"] {
		t.Errorf("ExistingUIDs() = %v, want {1,2}", existing)
	}
}

// TestMessageCache_Coverage locks in the Coverage/Exhausted semantics
// behind the cache-window fix, exercised through Get's served result —
// the predicate that used to live inline in Service.FetchMessages and
// could only be observed by driving the whole Service (see
// CODE_REVIEW.md round 5).
func TestMessageCache_Coverage(t *testing.T) {
	ctx := context.Background()
	repo, cache := newTestCache(t)

	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	key := CacheKey{AccountID: created.ID, Protocol: "imap", Folder: "INBOX"}

	served := func(label string, w Window) bool {
		t.Helper()
		_, ok, err := cache.Get(ctx, key, w)
		if err != nil {
			t.Fatalf("%s: Get() error = %v", label, err)
		}
		return ok
	}

	// Nothing cached yet: the cache covers no window.
	if served("fresh", Window{Limit: 20}) {
		t.Error("fresh cache served a window, want it to dial")
	}

	// Cache the newest 20 messages, then record that they were covered by
	// a full page (a fetch that returned as many as it asked for).
	rows := make([]mailer.Message, 20)
	for i := range rows {
		rows[i] = mailer.Message{UID: fmt.Sprintf("u%d", i+1), Folder: "INBOX"}
	}
	if err := cache.Upsert(ctx, key, rows, 0); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	// A full page grows Coverage without Exhausting.
	if err := cache.Record(ctx, key, Window{Limit: 20}, 20); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if !served("after full page", Window{Limit: 20}) {
		t.Error("window inside coverage not served, want served")
	}
	// A larger window must not be served from the smaller cache.
	if served("larger window", Window{Limit: 50}) {
		t.Error("window past coverage served, want it to dial")
	}

	// A short page latches Exhausted; any window is then answerable.
	if err := cache.Record(ctx, key, Window{Limit: 50}, 30); err != nil {
		t.Fatalf("Record() (short page) error = %v", err)
	}
	if !served("exhausted", Window{Limit: 100}) {
		t.Error("window after exhaustion not served, want served")
	}

	// Coverage/Exhausted never regress on a smaller later page.
	if err := cache.Record(ctx, key, Window{Limit: 10}, 10); err != nil {
		t.Fatalf("Record() (smaller page) error = %v", err)
	}
	coverage, exhausted, err := cache.state(ctx, key)
	if err != nil {
		t.Fatalf("state() error = %v", err)
	}
	if coverage != 30 || !exhausted {
		t.Errorf("state = (coverage=%d, exhausted=%v), want (30, true)", coverage, exhausted)
	}

	// State is per folder.
	if _, ok, err := cache.Get(ctx, CacheKey{AccountID: created.ID, Protocol: "imap", Folder: "Archive"}, Window{Limit: 10}); err != nil || ok {
		t.Errorf("Get(Archive) served = %v, err = %v; want fresh state", ok, err)
	}
}

// mockFetcherChecker is a test double for mailer.FetcherChecker,
// standing in for a real imap.Client without any network I/O.
// fetchCalls counts Fetch invocations so tests can assert the cache
// was (or wasn't) hit instead of dialing live.
type mockFetcherChecker struct {
	fetchResult []mailer.Message
	fetchErr    error
	// fetchFn, when set, lets a test vary the result by the requested
	// (limit, offset) — needed to exercise the cache-coverage logic.
	fetchFn    func(limit, offset int) []mailer.Message
	unread     int
	checkErr   error
	fetchCalls *int
}

func (m mockFetcherChecker) Fetch(ctx context.Context, folder string, limit, offset int) ([]mailer.Message, error) {
	if m.fetchCalls != nil {
		*m.fetchCalls++
	}
	if m.fetchFn != nil {
		return m.fetchFn(limit, offset), m.fetchErr
	}
	return m.fetchResult, m.fetchErr
}
func (m mockFetcherChecker) TestConnection(ctx context.Context) error { return nil }
func (m mockFetcherChecker) Check(ctx context.Context, folder string) (int, error) {
	return m.unread, m.checkErr
}

func TestService_FetchMessages_CachesResult(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	fixture := []mailer.Message{{UID: "u1", Folder: "INBOX", Subject: "Hi"}}
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchResult: fixture}
	})

	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 10, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages() error = %v", err)
	}
	if len(msgs) != 1 || msgs[0].UID != "u1" {
		t.Errorf("FetchMessages() = %v, want fixture", msgs)
	}

	cached, err := svc.cache.list(ctx, CacheKey{AccountID: created.ID, Protocol: "imap", Folder: "INBOX"}, Window{Limit: 10})
	if err != nil {
		t.Fatalf("list() error = %v", err)
	}
	if len(cached) != 1 {
		t.Errorf("cache after FetchMessages len = %d, want 1", len(cached))
	}
}

// TestService_FetchMessages_ServesFromCacheOnSecondCall is the
// regression test for CODE_REVIEW.md "message cache write-only": the
// second FetchMessages call for the same (account, protocol, folder)
// must be served from the Message cache, not by dialing the mail server
// again — proven here by a Fetch call counter, not just by inspecting
// the cache table (which was already populated even in the old,
// buggy always-live-fetch behavior).
func TestService_FetchMessages_ServesFromCacheOnSecondCall(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	calls := 0
	fixture := []mailer.Message{{UID: "u1", Folder: "INBOX", Subject: "Hi"}}
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchResult: fixture, fetchCalls: &calls}
	})

	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 10, 0, false); err != nil {
		t.Fatalf("FetchMessages() (1st) error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("fetchCalls after 1st FetchMessages = %d, want 1 (cache empty, must dial)", calls)
	}

	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 10, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages() (2nd) error = %v", err)
	}
	if calls != 1 {
		t.Errorf("fetchCalls after 2nd FetchMessages = %d, want still 1 (should serve from cache)", calls)
	}
	if len(msgs) != 1 || msgs[0].UID != "u1" {
		t.Errorf("2nd FetchMessages() = %v, want cached fixture", msgs)
	}

	// refresh=true must force a live dial even though the cache is warm.
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 10, 0, true); err != nil {
		t.Fatalf("FetchMessages() (refresh) error = %v", err)
	}
	if calls != 2 {
		t.Errorf("fetchCalls after refresh=true call = %d, want 2", calls)
	}
}

// TestService_FetchMessages_LargerWindowRefetches is the regression test
// for a real bug found by code review (round 5): FetchMessages served
// the cache whenever it held *any* row, so a later, larger request was
// silently truncated to the older, smaller cached page instead of
// dialing for the bigger window.
func TestService_FetchMessages_LargerWindowRefetches(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// A 30-message mailbox, newest-first, sliced by the fetch window so a
	// fetch returns a full page (== limit) for limits <= 30 and a short
	// page for limits > 30.
	all := make([]mailer.Message, 30)
	for i := range all {
		all[i] = mailer.Message{UID: fmt.Sprintf("u%d", 30-i), Folder: "INBOX"}
	}

	calls := 0
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{
			fetchCalls: &calls,
			fetchFn: func(limit, offset int) []mailer.Message {
				if offset >= len(all) {
					return nil
				}
				end := offset + limit
				if end > len(all) {
					end = len(all)
				}
				return all[offset:end]
			},
		}
	})

	// First call caches a 20-message window.
	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(limit=20) error = %v", err)
	}
	if len(msgs) != 20 {
		t.Fatalf("FetchMessages(limit=20) len = %d, want 20", len(msgs))
	}
	if calls != 1 {
		t.Fatalf("fetchCalls after limit=20 = %d, want 1", calls)
	}

	// A larger request must NOT be served from the 20-row cache.
	msgs, err = svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 50, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(limit=50) error = %v", err)
	}
	if len(msgs) != 30 {
		t.Errorf("FetchMessages(limit=50) len = %d, want 30 (must dial for the bigger window)", len(msgs))
	}
	if calls != 2 {
		t.Errorf("fetchCalls after limit=50 = %d, want 2 (cache only covered 20)", calls)
	}

	// The mailbox is now known to be fully cached (the limit=50 fetch
	// returned a short page), so an even larger window is answerable
	// without another dial.
	msgs, err = svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 100, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(limit=100) error = %v", err)
	}
	if len(msgs) != 30 {
		t.Errorf("FetchMessages(limit=100) len = %d, want 30", len(msgs))
	}
	if calls != 2 {
		t.Errorf("fetchCalls after limit=100 = %d, want still 2 (mailbox exhausted, cache covers all)", calls)
	}
}

// TestService_FetchMessages_ZeroLimitUsesDefault locks in that limit <= 0
// is defaulted centrally in account.Service (shared by REST and MCP) —
// REST's explicit limit=0 used to return nothing while MCP's returned
// the default page, an adapter divergence.
func TestService_FetchMessages_ZeroLimitUsesDefault(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var gotLimit int
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchFn: func(limit, offset int) []mailer.Message {
			gotLimit = limit
			return nil
		}}
	})

	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 0, 0, false); err != nil {
		t.Fatalf("FetchMessages(limit=0) error = %v", err)
	}
	if gotLimit != DefaultFetchLimit {
		t.Errorf("fetcher saw limit = %d, want DefaultFetchLimit (%d)", gotLimit, DefaultFetchLimit)
	}
}

func TestService_CheckNew(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// First check: nothing cached yet, both messages are "new".
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{
			unread: 3,
			fetchResult: []mailer.Message{
				{UID: "u1", Folder: "INBOX"},
				{UID: "u2", Folder: "INBOX"},
			},
		}
	})
	unread, newCount, err := svc.CheckNew(ctx, created.ID, "imap", "INBOX")
	if err != nil {
		t.Fatalf("CheckNew() error = %v", err)
	}
	if unread != 3 {
		t.Errorf("unread = %d, want 3", unread)
	}
	if newCount != 2 {
		t.Errorf("newCount = %d, want 2 (both messages uncached)", newCount)
	}

	// Second check: same two UIDs again, now cached from the first call
	// -> zero new.
	unread, newCount, err = svc.CheckNew(ctx, created.ID, "imap", "INBOX")
	if err != nil {
		t.Fatalf("CheckNew() (2nd) error = %v", err)
	}
	if newCount != 0 {
		t.Errorf("newCount (2nd call) = %d, want 0 (already cached)", newCount)
	}
}

func TestService_FetchMessages_UnknownProtocol(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.FetchMessages(ctx, created.ID, "ftp", "INBOX", 10, 0, false); err == nil {
		t.Error("FetchMessages() error = nil, want error for unknown protocol")
	}
}

// folderCapturingFetcher wraps mockFetcherChecker, recording the folder
// it was asked to fetch — used to prove account.Service hands a
// folder-less protocol its only folder (INBOX).
type folderCapturingFetcher struct {
	mockFetcherChecker
	gotFolder *string
}

func (f folderCapturingFetcher) Fetch(ctx context.Context, folder string, limit, offset int) ([]mailer.Message, error) {
	*f.gotFolder = folder
	return f.fetchResult, nil
}

// TestService_FetchMessages_POP3FolderCanonicalized locks in that a
// folder-less protocol is keyed under its only folder (INBOX) no matter
// what the caller asked for — so the Message cache key and the folder
// recorded per message agree (see the architecture review's candidate
// C; POP3 used to store under one folder while the key said another).
func TestService_FetchMessages_POP3FolderCanonicalized(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	acct := sampleAccount()
	acct.POP3 = &ConnectionConfig{Host: "pop.example.com", Port: 995, TLSMode: TLSModeTLS}
	created, err := svc.Create(ctx, acct, "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var gotFolder string
	registerPOP3(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return folderCapturingFetcher{
			mockFetcherChecker: mockFetcherChecker{fetchResult: []mailer.Message{{UID: "u1", Folder: "INBOX"}}},
			gotFolder:          &gotFolder,
		}
	})

	// A caller asking for a folder POP3 doesn't have still lands in INBOX.
	if _, err := svc.FetchMessages(ctx, created.ID, "pop3", "Archive", 10, 0, false); err != nil {
		t.Fatalf("FetchMessages() error = %v", err)
	}
	if gotFolder != "INBOX" {
		t.Errorf("fetcher was handed folder %q, want INBOX (POP3 is folder-less)", gotFolder)
	}

	key := CacheKey{AccountID: created.ID, Protocol: ProtocolPOP3, Folder: "INBOX"}
	got, err := svc.cache.list(ctx, key, Window{Limit: 10})
	if err != nil {
		t.Fatalf("list(INBOX) error = %v", err)
	}
	if len(got) != 1 || got[0].UID != "u1" {
		t.Errorf("cache under INBOX = %v, want the fetched message", got)
	}

	// Nothing is recorded under the caller's folder, which POP3 has no
	// concept of.
	other := CacheKey{AccountID: created.ID, Protocol: ProtocolPOP3, Folder: "Archive"}
	none, err := svc.cache.list(ctx, other, Window{Limit: 10})
	if err != nil {
		t.Fatalf("list(Archive) error = %v", err)
	}
	if len(none) != 0 {
		t.Errorf("cache under Archive = %v, want empty (POP3 is folder-less)", none)
	}
}

// TestMessageCache_Record_OffsetDoesNotInflateCoverage: a fetch that
// starts past the region already known to be contiguous from the top
// proves nothing about the rows above it, so Record must leave coverage
// alone rather than inflating it (which used to let a later offset=0
// window be served from a truncated cache) or latching Exhausted.
func TestMessageCache_Record_OffsetDoesNotInflateCoverage(t *testing.T) {
	ctx := context.Background()
	repo, cache := newTestCache(t)
	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	key := CacheKey{AccountID: created.ID, Protocol: ProtocolIMAP, Folder: "INBOX"}

	// A deep, short page with nothing cached above it records nothing.
	if err := cache.Record(ctx, key, Window{Limit: 20, Offset: 100}, 5); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if coverage, exhausted, err := cache.state(ctx, key); err != nil || coverage != 0 || exhausted {
		t.Errorf("state after deep page = (coverage=%d, exhausted=%v, err=%v), want (0, false, nil)", coverage, exhausted, err)
	}

	// Cache the top 20, then a deep page: coverage stays 20, not exhausted.
	if err := cache.Record(ctx, key, Window{Limit: 20}, 20); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := cache.Record(ctx, key, Window{Limit: 20, Offset: 100}, 5); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if coverage, exhausted, err := cache.state(ctx, key); err != nil || coverage != 20 || exhausted {
		t.Errorf("state after top+deep = (coverage=%d, exhausted=%v, err=%v), want (20, false, nil)", coverage, exhausted, err)
	}
}

// TestService_FetchMessages_OffsetDoesNotTruncateLaterWindow is the
// REST-path regression test for the truncation bug above: a deep
// ?offset= page must not make a later, larger offset=0 request serve a
// truncated cache.
func TestService_FetchMessages_OffsetDoesNotTruncateLaterWindow(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	all := make([]mailer.Message, 200)
	for i := range all {
		all[i] = mailer.Message{UID: fmt.Sprintf("u%d", i), Folder: "INBOX"}
	}
	calls := 0
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{
			fetchCalls: &calls,
			fetchFn: func(limit, offset int) []mailer.Message {
				if offset >= len(all) {
					return nil
				}
				end := offset + limit
				if end > len(all) {
					end = len(all)
				}
				return all[offset:end]
			},
		}
	})

	// A deep page (offset 100) must not be recorded as coverage of the top.
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 100, false); err != nil {
		t.Fatalf("FetchMessages(offset=100) error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("fetchCalls after offset=100 = %d, want 1", calls)
	}

	// The top window is not covered, so it must dial and return all 120.
	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 120, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(limit=120) error = %v", err)
	}
	if len(msgs) != 120 {
		t.Errorf("FetchMessages(limit=120) len = %d, want 120 (must not be truncated by the deep page)", len(msgs))
	}
	if calls != 2 {
		t.Errorf("fetchCalls after limit=120 = %d, want 2 (cache didn't cover the top)", calls)
	}
}

// TestMessageCache_ListOrdersAcrossBatches locks in position-based
// display order: pages fetched at different times (and inserted here out
// of order) must come back in mailbox order, not batch-recency order.
func TestMessageCache_ListOrdersAcrossBatches(t *testing.T) {
	ctx := context.Background()
	repo, cache := newTestCache(t)
	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	key := CacheKey{AccountID: created.ID, Protocol: ProtocolIMAP, Folder: "INBOX"}

	page := func(first, n int) []mailer.Message {
		out := make([]mailer.Message, n)
		for i := range out {
			out[i] = mailer.Message{UID: fmt.Sprintf("u%d", first+i), Folder: "INBOX"}
		}
		return out
	}
	// Page normally: the top page, then the next page down.
	if err := cache.Upsert(ctx, key, page(0, 20), 0); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if err := cache.Upsert(ctx, key, page(20, 20), 20); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	got, err := cache.list(ctx, key, Window{Limit: 40})
	if err != nil {
		t.Fatalf("list() error = %v", err)
	}
	if len(got) != 40 {
		t.Fatalf("list() len = %d, want 40", len(got))
	}
	for i := range got {
		if want := fmt.Sprintf("u%d", i); got[i].UID != want {
			t.Errorf("list()[%d].UID = %q, want %q (position order, not batch order)", i, got[i].UID, want)
			break
		}
	}
}

// TestService_FetchMessages_AdjacentPagesServeInMailboxOrder is the
// regression test for a served window coming back in the wrong order:
// two adjacent pages fetched at different times used to return batched
// (e.g. [u20..u39, u0..u19]), and could even drop rows.
func TestService_FetchMessages_AdjacentPagesServeInMailboxOrder(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	all := make([]mailer.Message, 40)
	for i := range all {
		all[i] = mailer.Message{UID: fmt.Sprintf("u%d", i), Folder: "INBOX"}
	}
	calls := 0
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{
			fetchCalls: &calls,
			fetchFn: func(limit, offset int) []mailer.Message {
				if offset >= len(all) {
					return nil
				}
				end := offset + limit
				if end > len(all) {
					end = len(all)
				}
				return all[offset:end]
			},
		}
	})

	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false); err != nil {
		t.Fatalf("FetchMessages(20,0) error = %v", err)
	}
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 20, false); err != nil {
		t.Fatalf("FetchMessages(20,20) error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("fetchCalls = %d, want 2 (two live pages)", calls)
	}

	// The 40-message window is now covered, so it is served from cache —
	// and must be in mailbox order.
	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 40, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(40,0) error = %v", err)
	}
	if calls != 2 {
		t.Errorf("fetchCalls = %d, want still 2 (window covered by the two pages)", calls)
	}
	if len(msgs) != 40 {
		t.Fatalf("FetchMessages(40,0) len = %d, want 40", len(msgs))
	}
	for i := range msgs {
		if want := fmt.Sprintf("u%d", i); msgs[i].UID != want {
			t.Errorf("msgs[%d].UID = %q, want %q (served window must be in mailbox order)", i, msgs[i].UID, want)
			break
		}
	}
}

// TestService_FetchMessages_ExhaustedCacheRecoversWhenMailboxGrows locks
// in that the Exhausted latch is not permanent: once the mailbox has
// grown, a full page (CheckNew always dials) clears it, so later windows
// dial again instead of being served truncated forever.
func TestService_FetchMessages_ExhaustedCacheRecoversWhenMailboxGrows(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	size := 10
	calls := 0
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{
			fetchCalls: &calls,
			fetchFn: func(limit, offset int) []mailer.Message {
				if offset >= size {
					return nil
				}
				end := offset + limit
				if end > size {
					end = size
				}
				out := make([]mailer.Message, 0, end-offset)
				for i := offset; i < end; i++ {
					out = append(out, mailer.Message{UID: fmt.Sprintf("u%d", i), Folder: "INBOX"})
				}
				return out
			},
		}
	})

	// A 10-message mailbox: limit=20 returns a short page, latching Exhausted.
	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(20,0) error = %v", err)
	}
	if len(msgs) != 10 {
		t.Fatalf("FetchMessages(20,0) len = %d, want 10", len(msgs))
	}

	// The mailbox grows to 100 messages.
	size = 100

	// CheckNew always dials; its full 50-message page clears the stale latch.
	if _, _, err := svc.CheckNew(ctx, created.ID, "imap", "INBOX"); err != nil {
		t.Fatalf("CheckNew() error = %v", err)
	}

	// A window past the now-known 50 must dial and return live data, not
	// the truncated 10-row cache.
	msgs, err = svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 80, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(80,0) error = %v", err)
	}
	if len(msgs) != 80 {
		t.Errorf("FetchMessages(80,0) len = %d, want 80 (stale Exhausted must not serve the 10-row cache)", len(msgs))
	}
}

// TestService_FetchMessages_NewMailAtTopDoesNotServeStaleRows is the
// regression test for stale mailbox positions: once new mail arrives at
// the top, every cached row's sort_rank no longer reflects its position,
// so a later served window must not mix those rows in.
func TestService_FetchMessages_NewMailAtTopDoesNotServeStaleRows(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mailbox := make([]string, 200) // mailbox[0] is the newest
	for i := range mailbox {
		mailbox[i] = fmt.Sprintf("u%d", i)
	}
	fetch := func(limit, offset int) []mailer.Message {
		if offset >= len(mailbox) {
			return nil
		}
		end := offset + limit
		if end > len(mailbox) {
			end = len(mailbox)
		}
		out := make([]mailer.Message, 0, end-offset)
		for i := offset; i < end; i++ {
			out = append(out, mailer.Message{UID: mailbox[i], Folder: "INBOX"})
		}
		return out
	}
	calls := 0
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchCalls: &calls, fetchFn: fetch}
	})

	// Cache the newest 60 of the original mailbox.
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 60, 0, false); err != nil {
		t.Fatalf("FetchMessages(60,0) error = %v", err)
	}

	// Five new messages arrive at the top; every cached row shifts down.
	mailbox = append([]string{"n0", "n1", "n2", "n3", "n4"}, mailbox...)

	// Refresh just the first page — a realistic "any new mail?" action.
	// The cached rows below it keep their pre-shift positions.
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, true); err != nil {
		t.Fatalf("FetchMessages(20,0,refresh=true) error = %v", err)
	}

	// A 60-message window must not be answered from rows whose recorded
	// position predates the new mail.
	before := calls
	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 60, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(60,0) (2nd) error = %v", err)
	}
	if len(msgs) != 60 {
		t.Fatalf("len = %d, want 60", len(msgs))
	}
	for i, want := range mailbox[:60] {
		if msgs[i].UID != want {
			t.Errorf("msgs[%d].UID = %q, want %q (rows cached before the new mail must not be served; calls %d->%d)", i, msgs[i].UID, want, before, calls)
			break
		}
	}
}

// TestService_FetchMessages_OffsetPageAfterNewMailDoesNotServeStaleRows is
// the offset>0 counterpart: a deeper page fetched after the mailbox
// shifted records positions relative to the new mailbox, so it must not
// extend a cache whose earlier rows predate the new mail.
func TestService_FetchMessages_OffsetPageAfterNewMailDoesNotServeStaleRows(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mailbox := make([]string, 200) // mailbox[0] is the newest
	for i := range mailbox {
		mailbox[i] = fmt.Sprintf("u%d", i)
	}
	fetch := func(limit, offset int) []mailer.Message {
		if offset >= len(mailbox) {
			return nil
		}
		end := offset + limit
		if end > len(mailbox) {
			end = len(mailbox)
		}
		out := make([]mailer.Message, 0, end-offset)
		for i := offset; i < end; i++ {
			out = append(out, mailer.Message{UID: mailbox[i], Folder: "INBOX"})
		}
		return out
	}
	calls := 0
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchCalls: &calls, fetchFn: fetch}
	})

	// Cache the newest 20 of the original mailbox.
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false); err != nil {
		t.Fatalf("FetchMessages(20,0) error = %v", err)
	}

	// Five new messages arrive at the top; every cached row shifts down.
	mailbox = append([]string{"n0", "n1", "n2", "n3", "n4"}, mailbox...)

	// A deeper page — its positions are relative to the new mailbox.
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 20, false); err != nil {
		t.Fatalf("FetchMessages(20,20) error = %v", err)
	}

	// The top 30 must not be served from the incoherent mix that page
	// would leave behind.
	before := calls
	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 30, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(30,0) error = %v", err)
	}
	if len(msgs) != 30 {
		t.Fatalf("len = %d, want 30", len(msgs))
	}
	for i, want := range mailbox[:30] {
		if msgs[i].UID != want {
			t.Errorf("msgs[%d].UID = %q, want %q (a deeper page written after the shift must not extend a stale cache; calls %d->%d)", i, msgs[i].UID, want, before, calls)
			break
		}
	}
}

// TestService_FetchMessages_DeepPageAfterLargeShiftDoesNotHoleTheCache is
// the counterpart where the shifted page's first message is brand new
// (not cached) and its rank is empty, but the page still overlaps older
// cached messages further down: writing it moves those rows out of the
// covered prefix and leaves a rank hole there, yet coverage is unchanged.
func TestService_FetchMessages_DeepPageAfterLargeShiftDoesNotHoleTheCache(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mailbox := make([]string, 200) // mailbox[0] is the newest
	for i := range mailbox {
		mailbox[i] = fmt.Sprintf("u%d", i)
	}
	fetch := func(limit, offset int) []mailer.Message {
		if offset >= len(mailbox) {
			return nil
		}
		end := offset + limit
		if end > len(mailbox) {
			end = len(mailbox)
		}
		out := make([]mailer.Message, 0, end-offset)
		for i := offset; i < end; i++ {
			out = append(out, mailer.Message{UID: mailbox[i], Folder: "INBOX"})
		}
		return out
	}
	calls := 0
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchCalls: &calls, fetchFn: fetch}
	})

	// Cache the newest 10 of the original mailbox.
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 10, 0, false); err != nil {
		t.Fatalf("FetchMessages(10,0) error = %v", err)
	}

	// Twenty new messages arrive at the top.
	mailbox = append([]string{
		"n0", "n1", "n2", "n3", "n4", "n5", "n6", "n7", "n8", "n9",
		"n10", "n11", "n12", "n13", "n14", "n15", "n16", "n17", "n18", "n19",
	}, mailbox...)

	// A deep page past the covered prefix: its top is new mail (uncached,
	// empty rank) but it runs into the old cached messages below.
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 10, 15, false); err != nil {
		t.Fatalf("FetchMessages(10,15) error = %v", err)
	}

	// The covered prefix must still be served correctly (or dialed) —
	// never a hole-filled mix.
	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 10, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(10,0) (2nd) error = %v", err)
	}
	if len(msgs) != 10 {
		t.Fatalf("len = %d, want 10", len(msgs))
	}
	for i, want := range mailbox[:10] {
		if msgs[i].UID != want {
			t.Errorf("msgs[%d].UID = %q, want %q (a deep page must not hole the covered prefix)", i, msgs[i].UID, want)
			break
		}
	}
}

// TestService_FetchMessages_MidMailboxDeletionDoesNotLeaveDuplicateRanks is
// the regression test for a shift the page cannot see at its first rank: a
// message deleted inside the fetched page moves every later cached row onto
// a rank already held by its neighbour, which an unordered read would then
// interleave.
func TestService_FetchMessages_MidMailboxDeletionDoesNotLeaveDuplicateRanks(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mailbox := make([]string, 200) // mailbox[0] is the newest
	for i := range mailbox {
		mailbox[i] = fmt.Sprintf("u%d", i)
	}
	fetch := func(limit, offset int) []mailer.Message {
		if offset >= len(mailbox) {
			return nil
		}
		end := offset + limit
		if end > len(mailbox) {
			end = len(mailbox)
		}
		out := make([]mailer.Message, 0, end-offset)
		for i := offset; i < end; i++ {
			out = append(out, mailer.Message{UID: mailbox[i], Folder: "INBOX"})
		}
		return out
	}
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchFn: fetch}
	})

	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false); err != nil {
		t.Fatalf("FetchMessages(20,0) error = %v", err)
	}

	// A message is deleted in the middle of the cached page; the top is unchanged.
	mailbox = append(mailbox[:5], mailbox[6:]...)

	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, true)
	if err != nil {
		t.Fatalf("FetchMessages(20,0,refresh) error = %v", err)
	}
	if len(msgs) != 20 {
		t.Fatalf("len = %d, want 20", len(msgs))
	}
	for i, want := range mailbox[:20] {
		if msgs[i].UID != want {
			t.Errorf("msgs[%d].UID = %q, want %q (a mid-page deletion must not leave stale/duplicate ranks)", i, msgs[i].UID, want)
			break
		}
	}

	// The next read must be consistent too (no duplicate-rank interleave).
	msgs, err = svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(20,0) (2nd) error = %v", err)
	}
	for i, want := range mailbox[:20] {
		if msgs[i].UID != want {
			t.Errorf("2nd msgs[%d].UID = %q, want %q (duplicate-rank interleave)", i, msgs[i].UID, want)
			break
		}
	}
}

// TestService_FetchMessages_EmptyMailboxDoesNotServeStaleRows: an empty
// mailbox must not keep serving what it used to hold — a top fetch that
// returns nothing proves the cached rows are gone.
func TestService_FetchMessages_EmptyMailboxDoesNotServeStaleRows(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mailbox := make([]string, 20)
	for i := range mailbox {
		mailbox[i] = fmt.Sprintf("c%d", i)
	}
	fetch := func(limit, offset int) []mailer.Message {
		if offset >= len(mailbox) {
			return nil
		}
		end := offset + limit
		if end > len(mailbox) {
			end = len(mailbox)
		}
		out := make([]mailer.Message, 0, end-offset)
		for i := offset; i < end; i++ {
			out = append(out, mailer.Message{UID: mailbox[i], Folder: "INBOX"})
		}
		return out
	}
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchFn: fetch}
	})

	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false); err != nil {
		t.Fatalf("FetchMessages(20,0) error = %v", err)
	}

	// The mailbox is emptied.
	mailbox = nil

	// CheckNew always dials; it sees an empty mailbox.
	if _, _, err := svc.CheckNew(ctx, created.ID, "imap", "INBOX"); err != nil {
		t.Fatalf("CheckNew() error = %v", err)
	}

	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages() error = %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("len = %d, want 0 (an emptied mailbox must not serve cached rows); first = %q", len(msgs), msgs[0].UID)
	}
}

// TestService_FetchMessages_DeepPagePastPrefixIsNotServedAsOrphans pins the
// second half of the coherence fix: a page that starts past the cached
// prefix is not stored, because nothing could extend the prefix to reach it
// and it would leak into a served window once exhausted latches.
func TestService_FetchMessages_DeepPagePastPrefixIsNotServedAsOrphans(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mailbox := make([]string, 200) // mailbox[0] is the newest
	for i := range mailbox {
		mailbox[i] = fmt.Sprintf("u%d", i)
	}
	fetch := func(limit, offset int) []mailer.Message {
		if offset >= len(mailbox) {
			return nil
		}
		end := offset + limit
		if end > len(mailbox) {
			end = len(mailbox)
		}
		out := make([]mailer.Message, 0, end-offset)
		for i := offset; i < end; i++ {
			out = append(out, mailer.Message{UID: mailbox[i], Folder: "INBOX"})
		}
		return out
	}
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchFn: fetch}
	})

	// Cache the newest 20, then fetch a deep page far past them.
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false); err != nil {
		t.Fatalf("FetchMessages(20,0) error = %v", err)
	}
	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 100, false); err != nil {
		t.Fatalf("FetchMessages(20,100) error = %v", err)
	}

	key := CacheKey{AccountID: created.ID, Protocol: ProtocolIMAP, Folder: "INBOX"}
	rows, err := svc.cache.list(ctx, key, Window{Limit: 200})
	if err != nil {
		t.Fatalf("list() error = %v", err)
	}
	if len(rows) != 20 {
		t.Fatalf("cache holds %d rows, want 20 (a deep page past the prefix must not be stored as orphans)", len(rows))
	}
	for i, want := range mailbox[:20] {
		if rows[i].UID != want {
			t.Errorf("cache row %d = %q, want %q", i, rows[i].UID, want)
			break
		}
	}
}

// TestService_FetchMessages_FullyReplacedMailboxDoesNotServeDuplicateRanks
// pins the check that a cached rank must hold the page's message: when the
// mailbox is replaced wholesale (and no empty period is ever observed), every
// message in the new page is uncached, yet rank 0 is still held by a deleted
// message — so only that check can detect the shift.
func TestService_FetchMessages_FullyReplacedMailboxDoesNotServeDuplicateRanks(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	mailbox := make([]string, 20)
	for i := range mailbox {
		mailbox[i] = fmt.Sprintf("u%d", i)
	}
	fetch := func(limit, offset int) []mailer.Message {
		if offset >= len(mailbox) {
			return nil
		}
		end := offset + limit
		if end > len(mailbox) {
			end = len(mailbox)
		}
		out := make([]mailer.Message, 0, end-offset)
		for i := offset; i < end; i++ {
			out = append(out, mailer.Message{UID: mailbox[i], Folder: "INBOX"})
		}
		return out
	}
	registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{fetchFn: fetch}
	})

	if _, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false); err != nil {
		t.Fatalf("FetchMessages(20,0) error = %v", err)
	}

	// The mailbox is replaced by a shorter one, with no observed empty state.
	mailbox = []string{"w0", "w1", "w2", "w3", "w4"}

	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, true)
	if err != nil {
		t.Fatalf("FetchMessages(20,0,refresh) error = %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("len = %d, want 5", len(msgs))
	}
	for i, want := range mailbox {
		if msgs[i].UID != want {
			t.Errorf("msgs[%d].UID = %q, want %q", i, msgs[i].UID, want)
			break
		}
	}

	// A cached read must not interleave the deleted messages.
	msgs, err = svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 20, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages(20,0) (2nd) error = %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("2nd len = %d, want 5 (deleted messages must not survive)", len(msgs))
	}
	for i, want := range mailbox {
		if msgs[i].UID != want {
			t.Errorf("2nd msgs[%d].UID = %q, want %q (duplicate-rank interleave)", i, msgs[i].UID, want)
			break
		}
	}
}
