package syncstatus

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis-entraid/manager"
	"github.com/redis/go-redis-entraid/token"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/auth"
)

func newTestRedisStore(t *testing.T, pendingTTL, terminalTTL time.Duration) (*RedisStore, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	store, err := NewRedisStore(client, "test:sync-status:", pendingTTL, terminalTTL)
	if err != nil {
		t.Fatalf("NewRedisStore() error = %v", err)
	}
	return store, server, client
}

func TestRedisStoreMissingAndRoundTrip(t *testing.T) {
	store, _, _ := newTestRedisStore(t, time.Minute, time.Hour)
	ctx := context.Background()

	rec, err := store.Get(ctx, "missing@example.edu")
	if err != nil || rec != (Record{}) {
		t.Fatalf("missing Get() = %+v, err=%v, want zero record", rec, err)
	}
	now := time.Date(2026, 7, 14, 10, 0, 0, 123456789, time.FixedZone("test", 8*60*60))
	source := now.Add(-time.Minute)
	store.now = func() time.Time { return now }
	if err := store.MarkPending(ctx, " User@Example.EDU ", source); err != nil {
		t.Fatalf("MarkPending() error = %v", err)
	}
	rec, err = store.Get(ctx, "user@example.edu")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusPending || !rec.UpdatedAt.Equal(now) || !rec.SourceEnqueuedAt.Equal(source) {
		t.Fatalf("round trip = %+v", rec)
	}
	if rec.UpdatedAt.Location() != time.UTC || rec.SourceEnqueuedAt.Location() != time.UTC {
		t.Fatalf("timestamps are not UTC: %+v", rec)
	}
}

func TestRedisStorePendingAndTerminalExpiration(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name string
		mark func(*RedisStore) error
		ttl  time.Duration
	}{
		{"pending", func(s *RedisStore) error { return s.MarkPending(ctx, "user@example.edu", time.Now()) }, 5 * time.Second},
		{"failed", func(s *RedisStore) error { return s.MarkFailed(ctx, "user@example.edu", time.Now()) }, 20 * time.Second},
		{"synced", func(s *RedisStore) error { return s.MarkSynced(ctx, "user@example.edu", time.Now()) }, 20 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, server, _ := newTestRedisStore(t, 5*time.Second, 20*time.Second)
			if err := tt.mark(store); err != nil {
				t.Fatal(err)
			}
			server.FastForward(tt.ttl - time.Millisecond)
			if rec, _ := store.Get(ctx, "user@example.edu"); rec.Status == StatusUnsynced {
				t.Fatal("record expired before configured TTL")
			}
			server.FastForward(time.Millisecond)
			if rec, _ := store.Get(ctx, "user@example.edu"); rec != (Record{}) {
				t.Fatalf("record after TTL = %+v, want zero", rec)
			}
		})
	}
}

func TestRedisStoreRejectsStaleWrites(t *testing.T) {
	store, _, _ := newTestRedisStore(t, time.Minute, time.Hour)
	ctx := context.Background()
	newer := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	if err := store.MarkSynced(ctx, "user@example.edu", newer); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkFailed(ctx, "user@example.edu", newer.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	rec, _ := store.Get(ctx, "user@example.edu")
	if rec.Status != StatusSynced || !rec.SourceEnqueuedAt.Equal(newer) {
		t.Fatalf("record = %+v, want newer synced", rec)
	}
}

func TestRedisStoreEqualTimestampPrecedence(t *testing.T) {
	ctx := context.Background()
	source := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		first, next Status
		want        Status
	}{
		{"pending to failed", StatusPending, StatusFailed, StatusFailed},
		{"pending to synced", StatusPending, StatusSynced, StatusSynced},
		{"pending idempotent", StatusPending, StatusPending, StatusPending},
		{"failed to synced", StatusFailed, StatusSynced, StatusSynced},
		{"failed rejects pending", StatusFailed, StatusPending, StatusFailed},
		{"failed idempotent", StatusFailed, StatusFailed, StatusFailed},
		{"synced rejects pending", StatusSynced, StatusPending, StatusSynced},
		{"synced rejects failed", StatusSynced, StatusFailed, StatusSynced},
		{"synced idempotent", StatusSynced, StatusSynced, StatusSynced},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _, _ := newTestRedisStore(t, time.Minute, time.Hour)
			if err := store.set(ctx, "user@example.edu", tt.first, source, time.Hour); err != nil {
				t.Fatal(err)
			}
			if err := store.set(ctx, "user@example.edu", tt.next, source, time.Hour); err != nil {
				t.Fatal(err)
			}
			rec, _ := store.Get(ctx, "user@example.edu")
			if rec.Status != tt.want {
				t.Fatalf("status = %q, want %q", rec.Status, tt.want)
			}
		})
	}
}

func TestValidateRedisOptions(t *testing.T) {
	t.Parallel()

	base := RedisOptions{
		Host:                    "cache.example.redis.azure.net",
		Port:                    10000,
		KeyPrefix:               "password-hook:sync-status:",
		PendingTTL:              5 * time.Minute,
		TerminalTTL:             90 * 24 * time.Hour,
		ManagedIdentityClientID: "00000000-0000-0000-0000-000000000003",
		PingTimeout:             time.Second,
	}
	tests := []struct {
		name string
		edit func(*RedisOptions)
	}{
		{name: "missing host", edit: func(o *RedisOptions) { o.Host = "" }},
		{name: "host scheme", edit: func(o *RedisOptions) { o.Host = "rediss://cache.example" }},
		{name: "host port", edit: func(o *RedisOptions) { o.Host = "cache.example:10000" }},
		{name: "invalid port", edit: func(o *RedisOptions) { o.Port = 0 }},
		{name: "missing key prefix", edit: func(o *RedisOptions) { o.KeyPrefix = "" }},
		{name: "invalid pending TTL", edit: func(o *RedisOptions) { o.PendingTTL = 0 }},
		{name: "invalid terminal TTL", edit: func(o *RedisOptions) { o.TerminalTTL = 0 }},
		{name: "missing client ID", edit: func(o *RedisOptions) { o.ManagedIdentityClientID = "" }},
		{name: "invalid client ID", edit: func(o *RedisOptions) { o.ManagedIdentityClientID = "not-a-uuid" }},
		{name: "invalid ping timeout", edit: func(o *RedisOptions) { o.PingTimeout = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := base
			tt.edit(&opts)
			if err := validateRedisOptions(opts); err == nil {
				t.Fatal("validateRedisOptions returned nil error")
			}
		})
	}
}

type fakeTokenManager struct {
	token     *token.Token
	startErr  error
	stopErr   error
	stopCalls int
}

func (m *fakeTokenManager) GetToken(bool) (*token.Token, error) {
	return m.token, nil
}

func (m *fakeTokenManager) Start(manager.TokenListener) (manager.StopFunc, error) {
	if m.startErr != nil {
		return nil, m.startErr
	}
	return func() error {
		m.stopCalls++
		return m.stopErr
	}, nil
}

type fakeCredentialsListener struct{}

func (*fakeCredentialsListener) OnNext(auth.Credentials) {}
func (*fakeCredentialsListener) OnError(error)           {}

func TestOwnedCredentialsProviderStopsTokenManagerExactlyOnce(t *testing.T) {
	now := time.Now()
	tokenManager := &fakeTokenManager{token: token.New("user", "password", "raw", now.Add(time.Hour), now, time.Hour.Milliseconds())}
	provider, err := newOwnedCredentialsProvider(tokenManager)
	if err != nil {
		t.Fatal(err)
	}
	listener := &fakeCredentialsListener{}
	_, unsubscribeFirst, err := provider.Subscribe(listener)
	if err != nil {
		t.Fatal(err)
	}
	_, unsubscribeSecond, err := provider.Subscribe(listener)
	if err != nil {
		t.Fatal(err)
	}
	if err := unsubscribeFirst(); err != nil {
		t.Fatal(err)
	}
	if err := unsubscribeSecond(); err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	if tokenManager.stopCalls != 1 {
		t.Fatalf("token manager stop calls = %d, want 1", tokenManager.stopCalls)
	}
	if _, _, err := provider.Subscribe(listener); err == nil {
		t.Fatal("Subscribe after Close returned nil error")
	}
}

func TestOwnedCredentialsProviderPropagatesStartAndStopErrors(t *testing.T) {
	startErr := errors.New("start failed")
	if _, err := newOwnedCredentialsProvider(&fakeTokenManager{startErr: startErr}); !errors.Is(err, startErr) {
		t.Fatalf("newOwnedCredentialsProvider error = %v, want %v", err, startErr)
	}
	now := time.Now()
	stopErr := errors.New("stop failed")
	tokenManager := &fakeTokenManager{
		token:   token.New("user", "password", "raw", now.Add(time.Hour), now, time.Hour.Milliseconds()),
		stopErr: stopErr,
	}
	provider, err := newOwnedCredentialsProvider(tokenManager)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Close(); !errors.Is(err, stopErr) {
		t.Fatalf("Close error = %v, want %v", err, stopErr)
	}
}

func TestRedisStoreIdempotentWritePreservesUpdatedAtAndTTL(t *testing.T) {
	store, server, client := newTestRedisStore(t, 10*time.Second, time.Hour)
	ctx := context.Background()
	source := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	firstNow := source.Add(time.Minute)
	store.now = func() time.Time { return firstNow }
	if err := store.MarkPending(ctx, "user@example.edu", source); err != nil {
		t.Fatal(err)
	}
	server.FastForward(3 * time.Second)
	before, err := client.PTTL(ctx, store.key("user@example.edu")).Result()
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return firstNow.Add(time.Hour) }
	if err := store.MarkPending(ctx, "user@example.edu", source); err != nil {
		t.Fatal(err)
	}
	after, _ := client.PTTL(ctx, store.key("user@example.edu")).Result()
	rec, _ := store.Get(ctx, "user@example.edu")
	if after != before {
		t.Fatalf("TTL changed from %v to %v", before, after)
	}
	if !rec.UpdatedAt.Equal(firstNow) {
		t.Fatalf("UpdatedAt = %v, want %v", rec.UpdatedAt, firstNow)
	}
}

func TestRedisStoreKeyAndValueDoNotContainUPN(t *testing.T) {
	store, server, client := newTestRedisStore(t, time.Minute, time.Hour)
	upn := "Sensitive.User@Example.EDU"
	if err := store.MarkSynced(context.Background(), upn, time.Now()); err != nil {
		t.Fatal(err)
	}
	keys := server.Keys()
	if len(keys) != 1 || strings.Contains(strings.ToLower(keys[0]), strings.ToLower(upn)) {
		t.Fatalf("Redis keys expose UPN: %#v", keys)
	}
	values, err := client.HGetAll(context.Background(), keys[0]).Result()
	if err != nil {
		t.Fatal(err)
	}
	for field, value := range values {
		if strings.Contains(strings.ToLower(field+value), strings.ToLower(upn)) {
			t.Fatalf("Redis hash exposes UPN: %q=%q", field, value)
		}
	}
}

func TestRedisStorePropagatesContextAndClientErrors(t *testing.T) {
	store, _, client := newTestRedisStore(t, time.Minute, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Get(ctx, "user@example.edu"); err != context.Canceled {
		t.Fatalf("Get error = %v, want context.Canceled", err)
	}
	if err := store.MarkPending(ctx, "user@example.edu", time.Now()); err != context.Canceled {
		t.Fatalf("MarkPending error = %v, want context.Canceled", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), "user@example.edu"); err == nil {
		t.Fatal("Get on closed client returned nil error")
	}
	if err := store.MarkSynced(context.Background(), "user@example.edu", time.Now()); err == nil {
		t.Fatal("MarkSynced on closed client returned nil error")
	}
}

func TestRedisStoreConcurrentOutOfOrderWritesAcrossClients(t *testing.T) {
	server := miniredis.RunT(t)
	clients := []*redis.Client{
		redis.NewClient(&redis.Options{Addr: server.Addr()}),
		redis.NewClient(&redis.Options{Addr: server.Addr()}),
	}
	stores := make([]*RedisStore, len(clients))
	for i, client := range clients {
		var err error
		stores[i], err = NewRedisStore(client, "test:sync-status:", time.Minute, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	statuses := []Status{StatusPending, StatusFailed, StatusSynced}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = stores[i%len(stores)].set(context.Background(), "user@example.edu", statuses[i%len(statuses)], base.Add(time.Duration(i)*time.Second), time.Hour)
		}(i)
	}
	wg.Wait()
	rec, err := stores[0].Get(context.Background(), "user@example.edu")
	if err != nil {
		t.Fatal(err)
	}
	if wantSource := base.Add(29 * time.Second); !rec.SourceEnqueuedAt.Equal(wantSource) || rec.Status != StatusSynced {
		t.Fatalf("final record = %+v, want source %v synced", rec, wantSource)
	}
}
