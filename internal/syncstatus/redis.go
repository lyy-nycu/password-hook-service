package syncstatus

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/google/uuid"
	entraidentity "github.com/redis/go-redis-entraid/identity"
	"github.com/redis/go-redis-entraid/manager"
	"github.com/redis/go-redis-entraid/shared"
	"github.com/redis/go-redis-entraid/token"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/auth"
)

const redisTimeLayout = "2006-01-02T15:04:05.000000000Z"

var transitionScript = redis.NewScript(`
local current_source = redis.call('HGET', KEYS[1], 'source_enqueued_at')
if current_source then
  if ARGV[3] < current_source then
    return 0
  end
  if ARGV[3] == current_source then
    local current_status = redis.call('HGET', KEYS[1], 'status')
    local precedence = {sync_pending = 1, sync_failed = 2, synced = 3}
    if (precedence[ARGV[1]] or 0) <= (precedence[current_status] or 0) then
      return 0
    end
  end
end
redis.call('HSET', KEYS[1],
  'status', ARGV[1],
  'updated_at', ARGV[2],
  'source_enqueued_at', ARGV[3])
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return 1
`)

// RedisOptions configures the production Azure Managed Redis client.
type RedisOptions struct {
	Host                    string
	Port                    int
	KeyPrefix               string
	PendingTTL              time.Duration
	TerminalTTL             time.Duration
	ManagedIdentityClientID string
	PingTimeout             time.Duration
}

// RedisStore is a shared Store backed by Redis. The production constructor
// uses a ClusterClient because Azure Managed Redis is configured with the
// OSSCluster policy.
type RedisStore struct {
	client      redis.UniversalClient
	keyPrefix   string
	pendingTTL  time.Duration
	terminalTTL time.Duration
	now         func() time.Time
	credentials interface{ Close() error }
}

// NewRedisStore builds a store around an injected Redis client. Production
// code should use NewManagedIdentityRedisStore.
func NewRedisStore(client redis.UniversalClient, keyPrefix string, pendingTTL, terminalTTL time.Duration) (*RedisStore, error) {
	if client == nil {
		return nil, errors.New("redis client is required")
	}
	if err := validateRedisStoreOptions(keyPrefix, pendingTTL, terminalTTL); err != nil {
		return nil, err
	}
	return &RedisStore{
		client:      client,
		keyPrefix:   keyPrefix,
		pendingTTL:  pendingTTL,
		terminalTTL: terminalTTL,
		now:         time.Now,
	}, nil
}

// NewManagedIdentityRedisStore creates an Entra-authenticated TLS cluster
// client and verifies connectivity with a bounded PING before returning.
func NewManagedIdentityRedisStore(ctx context.Context, opts RedisOptions) (*RedisStore, error) {
	if err := validateRedisOptions(opts); err != nil {
		return nil, err
	}
	credential, err := azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
		ID: azidentity.ClientID(strings.TrimSpace(opts.ManagedIdentityClientID)),
	})
	if err != nil {
		return nil, fmt.Errorf("create Redis managed identity credential: %w", err)
	}
	provider, err := newStreamingCredentialsProvider(credential)
	if err != nil {
		return nil, fmt.Errorf("create Redis streaming credentials provider: %w", err)
	}
	client := redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:                        []string{net.JoinHostPort(strings.TrimSpace(opts.Host), strconv.Itoa(opts.Port))},
		StreamingCredentialsProvider: provider,
		TLSConfig:                    &tls.Config{MinVersion: tls.VersionTLS12, ServerName: strings.TrimSpace(opts.Host)},
	})
	store, err := NewRedisStore(client, opts.KeyPrefix, opts.PendingTTL, opts.TerminalTTL)
	if err != nil {
		return nil, errors.Join(err, client.Close(), provider.Close())
	}
	store.credentials = provider
	pingCtx, cancel := context.WithTimeout(ctx, opts.PingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("ping Redis: %w", err), store.Close(context.Background()))
	}
	return store, nil
}

func validateRedisOptions(opts RedisOptions) error {
	if err := validateRedisStoreOptions(opts.KeyPrefix, opts.PendingTTL, opts.TerminalTTL); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(opts.Host) == "":
		return errors.New("Redis host is required")
	case strings.Contains(opts.Host, "://"):
		return errors.New("Redis host must not include a URL scheme")
	case strings.ContainsAny(opts.Host, "/@:"):
		return errors.New("Redis host must not include a path, user info, or port")
	case opts.Port <= 0 || opts.Port > 65535:
		return errors.New("Redis port must be between 1 and 65535")
	case strings.TrimSpace(opts.ManagedIdentityClientID) == "":
		return errors.New("Redis managed identity client ID is required")
	case uuid.Validate(strings.TrimSpace(opts.ManagedIdentityClientID)) != nil:
		return errors.New("Redis managed identity client ID must be a valid UUID")
	case opts.PingTimeout <= 0:
		return errors.New("Redis ping timeout must be positive")
	default:
		return nil
	}
}

func validateRedisStoreOptions(keyPrefix string, pendingTTL, terminalTTL time.Duration) error {
	switch {
	case strings.TrimSpace(keyPrefix) == "":
		return errors.New("redis key prefix is required")
	case pendingTTL < time.Millisecond:
		return errors.New("pending TTL must be at least 1ms")
	case terminalTTL < time.Millisecond:
		return errors.New("terminal TTL must be at least 1ms")
	default:
		return nil
	}
}

type managedIdentityProvider struct {
	credential azcore.TokenCredential
}

func (p managedIdentityProvider) RequestToken(ctx context.Context) (shared.IdentityProviderResponse, error) {
	token, err := p.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{entraidentity.RedisScopeDefault}})
	if err != nil {
		return nil, err
	}
	return shared.NewIDPResponse(shared.ResponseTypeAccessToken, &token)
}

func newStreamingCredentialsProvider(credential azcore.TokenCredential) (*ownedCredentialsProvider, error) {
	tokenManager, err := manager.NewTokenManager(managedIdentityProvider{credential: credential}, manager.TokenManagerOptions{})
	if err != nil {
		return nil, err
	}
	return newOwnedCredentialsProvider(tokenManager)
}

// ownedCredentialsProvider keeps the token manager lifetime tied to the
// RedisStore. go-redis subscriptions may come and go as cluster connections
// change, but the manager is stopped exactly once when the store closes.
type ownedCredentialsProvider struct {
	tokenManager manager.TokenManager
	stop         manager.StopFunc

	mu        sync.RWMutex
	listeners []auth.CredentialsListener
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

var _ auth.StreamingCredentialsProvider = (*ownedCredentialsProvider)(nil)
var _ manager.TokenListener = (*ownedCredentialsProvider)(nil)

func newOwnedCredentialsProvider(tokenManager manager.TokenManager) (*ownedCredentialsProvider, error) {
	if tokenManager == nil {
		return nil, errors.New("token manager is required")
	}
	provider := &ownedCredentialsProvider{tokenManager: tokenManager}
	stop, err := tokenManager.Start(provider)
	if err != nil {
		return nil, fmt.Errorf("start token manager: %w", err)
	}
	provider.stop = stop
	return provider, nil
}

func (p *ownedCredentialsProvider) Subscribe(listener auth.CredentialsListener) (auth.Credentials, auth.UnsubscribeFunc, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, nil, errors.New("credentials provider is closed")
	}
	current, err := p.tokenManager.GetToken(false)
	if err != nil {
		return nil, nil, fmt.Errorf("get Redis credentials: %w", err)
	}
	alreadySubscribed := false
	for _, existing := range p.listeners {
		if existing == listener {
			alreadySubscribed = true
			break
		}
	}
	if !alreadySubscribed {
		p.listeners = append(p.listeners, listener)
	}
	return current, func() error {
		p.mu.Lock()
		defer p.mu.Unlock()
		for i, existing := range p.listeners {
			if existing == listener {
				p.listeners = append(p.listeners[:i], p.listeners[i+1:]...)
				break
			}
		}
		return nil
	}, nil
}

func (p *ownedCredentialsProvider) OnNext(next *token.Token) {
	p.mu.RLock()
	listeners := append([]auth.CredentialsListener(nil), p.listeners...)
	p.mu.RUnlock()
	for _, listener := range listeners {
		listener.OnNext(next)
	}
}

func (p *ownedCredentialsProvider) OnError(err error) {
	p.mu.RLock()
	listeners := append([]auth.CredentialsListener(nil), p.listeners...)
	p.mu.RUnlock()
	for _, listener := range listeners {
		listener.OnError(err)
	}
}

func (p *ownedCredentialsProvider) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.listeners = nil
		stop := p.stop
		p.mu.Unlock()
		if stop != nil {
			p.closeErr = stop()
		}
	})
	return p.closeErr
}

func (s *RedisStore) Get(ctx context.Context, upn string) (Record, error) {
	fields, err := s.client.HGetAll(ctx, s.key(upn)).Result()
	if err != nil {
		return Record{}, err
	}
	if len(fields) == 0 {
		return Record{}, nil
	}
	status := Status(fields["status"])
	if statusPrecedence(status) == 0 {
		return Record{}, fmt.Errorf("invalid Redis sync status %q", status)
	}
	updatedAt, err := time.Parse(redisTimeLayout, fields["updated_at"])
	if err != nil {
		return Record{}, fmt.Errorf("parse Redis updated_at: %w", err)
	}
	sourceEnqueuedAt, err := time.Parse(redisTimeLayout, fields["source_enqueued_at"])
	if err != nil {
		return Record{}, fmt.Errorf("parse Redis source_enqueued_at: %w", err)
	}
	return Record{Status: status, UpdatedAt: updatedAt, SourceEnqueuedAt: sourceEnqueuedAt}, nil
}

func (s *RedisStore) MarkPending(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error {
	return s.set(ctx, upn, StatusPending, sourceEnqueuedAt, s.pendingTTL)
}

func (s *RedisStore) MarkSynced(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error {
	return s.set(ctx, upn, StatusSynced, sourceEnqueuedAt, s.terminalTTL)
}

func (s *RedisStore) MarkFailed(ctx context.Context, upn string, sourceEnqueuedAt time.Time) error {
	return s.set(ctx, upn, StatusFailed, sourceEnqueuedAt, s.terminalTTL)
}

func (s *RedisStore) set(ctx context.Context, upn string, status Status, sourceEnqueuedAt time.Time, ttl time.Duration) error {
	return transitionScript.Run(ctx, s.client, []string{s.key(upn)},
		string(status),
		s.now().UTC().Format(redisTimeLayout),
		sourceEnqueuedAt.UTC().Format(redisTimeLayout),
		ttl.Milliseconds(),
	).Err()
}

func (s *RedisStore) key(upn string) string {
	// SHA-256 here hashes only the normalized UPN (an account identifier),
	// never the password field, to build a one-way, non-reversible Redis
	// key digest. CodeQL's go/weak-sensitive-data-hashing heuristic flags
	// this call as insecure password hashing because the value originates
	// from a struct/type whose name contains "password"
	// (migration.PasswordSyncMessage), not because password material
	// actually reaches this hash. A password-strength KDF (bcrypt/PBKDF2/
	// etc.) is unnecessary and inappropriate for a deterministic cache-key
	// digest. The resulting alert is dismissed as a false positive; see
	// docs/ADR/2026-07-16-codeql-weak-sensitive-data-hashing-false-positive.md
	// for the full analysis and alternatives considered.
	digest := sha256.Sum256([]byte(normalizeUPN(upn)))
	return s.keyPrefix + hex.EncodeToString(digest[:])
}

func (s *RedisStore) Close(context.Context) error {
	clientErr := s.client.Close()
	var credentialsErr error
	if s.credentials != nil {
		credentialsErr = s.credentials.Close()
	}
	return errors.Join(clientErr, credentialsErr)
}
