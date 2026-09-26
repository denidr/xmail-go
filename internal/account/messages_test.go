package account

import (
	"context"
	"testing"

	"xmail/internal/mailer"
)

func TestRepository_UpsertAndListMessages(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	msgs := []mailer.Message{
		{UID: "1", Folder: "INBOX", Subject: "First", From: "a@x.com", IsRead: true},
		{UID: "2", Folder: "INBOX", Subject: "Second", From: "b@x.com", IsRead: false},
	}
	if err := repo.UpsertMessages(ctx, created.ID, "imap", msgs); err != nil {
		t.Fatalf("UpsertMessages() error = %v", err)
	}

	got, err := repo.ListMessages(ctx, created.ID, "imap", "INBOX", 10, 0)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListMessages() len = %d, want 2", len(got))
	}
	if got[0].UID != "1" || got[1].UID != "2" {
		t.Fatalf("ListMessages() order = [%s, %s], want [1, 2] (msgs[] order must be preserved — see TestRepository_UpsertMessages_PreservesOrder)", got[0].UID, got[1].UID)
	}

	// Upserting the same UID again with a changed field must update in
	// place, not duplicate.
	msgs[1].Subject = "Second (updated)"
	msgs[1].IsRead = true
	if err := repo.UpsertMessages(ctx, created.ID, "imap", msgs); err != nil {
		t.Fatalf("UpsertMessages() (update) error = %v", err)
	}
	got, err = repo.ListMessages(ctx, created.ID, "imap", "INBOX", 10, 0)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListMessages() after re-upsert len = %d, want 2 (no duplicates)", len(got))
	}
}

// TestRepository_UpsertMessages_PreservesOrder is the regression test
// for a real bug found by external code review: UpsertMessages shared
// one `fetched_at` timestamp across an entire batch, so
// "ORDER BY fetched_at DESC, rowid DESC" fell back to rowid DESC to
// break same-batch ties — but SQLite assigns rowids in insertion
// order, and msgs is inserted newest-first (matching Fetch's own
// order), so the newest message got the *smallest* rowid in the
// batch and rowid DESC put it *last*. Net effect: GET /messages was
// newest-first only on a cold cache, and silently flipped to
// oldest-first as soon as the cache was warm. Fixed via an explicit
// sort_rank column (migrations/0003_message_sort_rank.sql) set from
// each message's position in msgs, independent of rowid/insert order.
func TestRepository_UpsertMessages_PreservesOrder(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Newest-first, exactly as mailer.Fetcher.Fetch returns it.
	newestFirst := []mailer.Message{
		{UID: "3", Folder: "INBOX", Subject: "Newest"},
		{UID: "2", Folder: "INBOX", Subject: "Middle"},
		{UID: "1", Folder: "INBOX", Subject: "Oldest"},
	}
	if err := repo.UpsertMessages(ctx, created.ID, "imap", newestFirst); err != nil {
		t.Fatalf("UpsertMessages() error = %v", err)
	}

	assertOrder := func(t *testing.T, label string) {
		t.Helper()
		got, err := repo.ListMessages(ctx, created.ID, "imap", "INBOX", 10, 0)
		if err != nil {
			t.Fatalf("%s: ListMessages() error = %v", label, err)
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
	if err := repo.UpsertMessages(ctx, created.ID, "imap", newestFirst); err != nil {
		t.Fatalf("UpsertMessages() (2nd, warm cache) error = %v", err)
	}
	assertOrder(t, "after second upsert (warm cache)")
}

func TestRepository_ExistingUIDs(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo(t)

	created, err := repo.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := repo.UpsertMessages(ctx, created.ID, "imap", []mailer.Message{
		{UID: "1", Folder: "INBOX"}, {UID: "2", Folder: "INBOX"},
	}); err != nil {
		t.Fatalf("UpsertMessages() error = %v", err)
	}

	existing, err := repo.ExistingUIDs(ctx, created.ID, "imap", "INBOX")
	if err != nil {
		t.Fatalf("ExistingUIDs() error = %v", err)
	}
	if !existing["1"] || !existing["2"] || existing["3"] {
		t.Errorf("ExistingUIDs() = %v, want {1,2}", existing)
	}
}

// mockFetcherChecker is a test double for mailer.FetcherChecker,
// standing in for a real imap.Client without any network I/O.
// fetchCalls counts Fetch invocations so tests can assert the cache
// was (or wasn't) hit instead of dialing live.
type mockFetcherChecker struct {
	fetchResult []mailer.Message
	fetchErr    error
	unread      int
	checkErr    error
	fetchCalls  *int
}

func (m mockFetcherChecker) Fetch(ctx context.Context, folder string, limit, offset int) ([]mailer.Message, error) {
	if m.fetchCalls != nil {
		*m.fetchCalls++
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
	svc.SetIMAPFactory(func(cfg ConnectionConfig, username, secret string) mailer.FetcherChecker {
		return mockFetcherChecker{fetchResult: fixture}
	})

	msgs, err := svc.FetchMessages(ctx, created.ID, "imap", "INBOX", 10, 0, false)
	if err != nil {
		t.Fatalf("FetchMessages() error = %v", err)
	}
	if len(msgs) != 1 || msgs[0].UID != "u1" {
		t.Errorf("FetchMessages() = %v, want fixture", msgs)
	}

	cached, err := svc.repo.ListMessages(ctx, created.ID, "imap", "INBOX", 10, 0)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(cached) != 1 {
		t.Errorf("cache after FetchMessages len = %d, want 1", len(cached))
	}
}

// TestService_FetchMessages_ServesFromCacheOnSecondCall is the
// regression test for CODE_REVIEW.md "message cache write-only": the
// second FetchMessages call for the same (account, protocol, folder)
// must be served from messages_cache, not by dialing the mail server
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
	svc.SetIMAPFactory(func(cfg ConnectionConfig, username, secret string) mailer.FetcherChecker {
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

func TestService_CheckNew(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// First check: nothing cached yet, both messages are "new".
	svc.SetIMAPFactory(func(cfg ConnectionConfig, username, secret string) mailer.FetcherChecker {
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
