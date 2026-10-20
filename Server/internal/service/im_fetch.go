package service

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"gorm.io/gorm"
	"xbt2/server/internal/im"
	"xbt2/server/internal/xxt"
)

const (
	imCredentialTTL       = 10 * time.Minute
	imCredentialFailTTL   = 5 * time.Minute
	imDefaultFetchTimeout = 5 * time.Second
	imTokenSafetyWindow   = 30 * time.Second
)

// IMClient is the Easemob protocol surface used by the on-demand fetcher. It
// is satisfied by *im.Client and lets tests drive token/retry paths directly.
type IMClient interface {
	Token(ctx context.Context, uid int64, password string) (im.Token, error)
	Discover(ctx context.Context, token im.Token) ([]im.Group, []im.SignActivity, error)
}

// IMAccountProvider reads the account's Easemob credentials from the upstream
// userInfo endpoint. It is satisfied by *xxt.Client.
type IMAccountProvider interface {
	GetIMAccountContext(ctx context.Context, mobile, password string) (xxt.IMAccount, error)
}

// IMSnapshot is the last fetch result for one account. Activities contains
// only parsed sign-card fields; raw roaming messages are never cached.
type IMSnapshot struct {
	Enabled             bool
	IntervalSeconds     int
	FetchTimeoutSeconds int
	LastPollAt          time.Time
	LastSuccessAt       time.Time
	ErrorCode           string
	GroupCount          int
	ActivityCount       int
	Activities          []im.SignActivity
}

type imCredentialCache struct {
	password  string
	code      string
	expiresAt time.Time
}

// IMSignService fetches Easemob course groups on demand while GET
// /sign/activities is served. There is no background loop and construction
// sends no upstream requests. One account is never fetched concurrently:
// RefreshIfStale uses a per-uid TryLock and returns the previous snapshot when
// another request is already fetching.
type IMSignService struct {
	db               *gorm.DB
	accounts         IMAccountProvider
	client           IMClient
	cc               *CredentialCrypto
	minFetchInterval time.Duration
	fetchTimeout     time.Duration
	enabled          bool
	intervalSec      int
	fetchTimeoutSec  int

	mu        sync.Mutex
	snapshots map[int64]IMSnapshot
	creds     map[int64]imCredentialCache
	tokens    map[int64]im.Token
	locks     map[int64]*sync.Mutex
	known     map[int64]map[int64]struct{}
}

func NewIMSignService(db *gorm.DB, accounts IMAccountProvider, client IMClient, cc *CredentialCrypto, minFetchInterval, fetchTimeout time.Duration, enabled bool) *IMSignService {
	if minFetchInterval < 0 {
		minFetchInterval = 0
	}
	if fetchTimeout <= 0 {
		fetchTimeout = imDefaultFetchTimeout
	}
	intervalSec := int(minFetchInterval / time.Second)
	if intervalSec < 0 {
		intervalSec = 0
	}
	return &IMSignService{
		db:               db,
		accounts:         accounts,
		client:           client,
		cc:               cc,
		minFetchInterval: minFetchInterval,
		fetchTimeout:     fetchTimeout,
		enabled:          enabled && minFetchInterval > 0,
		intervalSec:      intervalSec,
		fetchTimeoutSec:  int(fetchTimeout / time.Second),
		snapshots:        make(map[int64]IMSnapshot),
		creds:            make(map[int64]imCredentialCache),
		tokens:           make(map[int64]im.Token),
		locks:            make(map[int64]*sync.Mutex),
		known:            make(map[int64]map[int64]struct{}),
	}
}

// Snapshot returns a copy of the last fetch result for uid.
func (s *IMSignService) Snapshot(uid int64) IMSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots[uid]
	if !ok {
		return IMSnapshot{
			Enabled:             s.enabled,
			IntervalSeconds:     s.intervalSec,
			FetchTimeoutSeconds: s.fetchTimeoutSec,
		}
	}
	snapshot.Enabled = s.enabled
	snapshot.IntervalSeconds = s.intervalSec
	snapshot.FetchTimeoutSeconds = s.fetchTimeoutSec
	snapshot.Activities = append([]im.SignActivity(nil), snapshot.Activities...)
	return snapshot
}

// RefreshIfStale fetches once when the account's last attempt is older than
// the configured minimum interval. Concurrent callers never queue: a caller
// that loses the per-account TryLock immediately gets the previous snapshot.
func (s *IMSignService) RefreshIfStale(ctx context.Context, uid int64) IMSnapshot {
	if !s.enabled {
		return s.snapshotWithError(uid, im.CodeDisabled)
	}
	snapshot := s.Snapshot(uid)
	if !snapshot.LastPollAt.IsZero() && time.Since(snapshot.LastPollAt) < s.minFetchInterval {
		return snapshot
	}
	return s.refreshAccount(ctx, uid, false)
}

func (s *IMSignService) refreshAccount(ctx context.Context, uid int64, force bool) IMSnapshot {
	if !s.enabled {
		return s.snapshotWithError(uid, im.CodeDisabled)
	}
	lock := s.accountLock(uid)
	if !lock.TryLock() {
		return s.Snapshot(uid)
	}
	defer lock.Unlock()

	startedAt := time.Now()
	snapshot := s.Snapshot(uid)
	if !force && !snapshot.LastPollAt.IsZero() && startedAt.Sub(snapshot.LastPollAt) < s.minFetchInterval {
		return snapshot
	}
	// Mark the attempt before touching upstream. A request arriving while this
	// fetch is running (or after it times out) must not start a second fetch.
	snapshot.LastPollAt = startedAt
	s.storeSnapshot(uid, snapshot)

	fetchCtx, cancel := context.WithTimeout(ctx, s.fetchTimeout)
	defer cancel()

	credentials, err := s.accountCredentials(fetchCtx, uid)
	if err != nil {
		return s.finishPoll(uid, snapshot, 0, nil, imErrorCode(err))
	}
	token, err := s.accountToken(fetchCtx, uid, credentials.password)
	if err != nil {
		return s.finishPoll(uid, snapshot, 0, nil, tokenErrorCode(err))
	}

	groups, activities, err := s.client.Discover(fetchCtx, token)
	if errors.Is(err, im.ErrUnauthorized) {
		s.dropToken(uid)
		token, tokenErr := s.accountToken(fetchCtx, uid, credentials.password)
		if tokenErr != nil {
			return s.finishPoll(uid, snapshot, 0, nil, tokenErrorCode(tokenErr))
		}
		groups, activities, err = s.client.Discover(fetchCtx, token)
		if errors.Is(err, im.ErrUnauthorized) {
			return s.finishPoll(uid, snapshot, 0, nil, im.CodeUnauthorized)
		}
	}
	if err != nil {
		return s.finishPoll(uid, snapshot, 0, nil, imErrorCode(err))
	}
	return s.finishPoll(uid, snapshot, len(groups), activities, "")
}

func (s *IMSignService) finishPoll(uid int64, snapshot IMSnapshot, groupCount int, activities []im.SignActivity, code string) IMSnapshot {
	now := time.Now()
	durationMS := now.Sub(snapshot.LastPollAt).Milliseconds()
	snapshot.LastPollAt = now
	snapshot.GroupCount = groupCount
	snapshot.ErrorCode = code
	if code == "" {
		snapshot.LastSuccessAt = now
		snapshot.Activities = append([]im.SignActivity(nil), activities...)
		snapshot.ActivityCount = len(activities)
	}

	s.mu.Lock()
	previous := s.snapshots[uid]
	if code != "" {
		// Keep the last successful activity set so a transient IM failure does
		// not erase discovered sign cards from the list.
		snapshot.Activities = previous.Activities
		snapshot.ActivityCount = len(previous.Activities)
		snapshot.LastSuccessAt = previous.LastSuccessAt
		snapshot.GroupCount = previous.GroupCount
	}
	s.snapshots[uid] = snapshot
	var discovered []int64
	if code == "" {
		known := s.known[uid]
		if known == nil {
			known = make(map[int64]struct{})
			s.known[uid] = known
		}
		for _, activity := range activities {
			if _, ok := known[activity.ActiveID]; !ok {
				known[activity.ActiveID] = struct{}{}
				discovered = append(discovered, activity.ActiveID)
			}
		}
		if len(known) > 2000 {
			s.known[uid] = make(map[int64]struct{}, len(activities))
			for _, activity := range activities {
				s.known[uid][activity.ActiveID] = struct{}{}
			}
		}
	}
	s.mu.Unlock()

	for _, activeID := range discovered {
		log.Printf("im activity discovered: uid=%d active_id=%d source=%s result=ok", uid, activeID, im.SourceIMGroup)
	}
	if code == "" {
		log.Printf("im fetch: uid=%d source=%s groups=%d activities=%d result=ok duration_ms=%d", uid, im.SourceIMGroup, groupCount, len(activities), durationMS)
	} else {
		log.Printf("im fetch: uid=%d source=%s result=%s duration_ms=%d", uid, im.SourceIMGroup, code, durationMS)
	}
	return snapshot
}

func (s *IMSignService) storeSnapshot(uid int64, snapshot IMSnapshot) {
	s.mu.Lock()
	s.snapshots[uid] = snapshot
	s.mu.Unlock()
}

func (s *IMSignService) snapshotWithError(uid int64, code string) IMSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.snapshots[uid]
	previous.Enabled = s.enabled
	previous.IntervalSeconds = s.intervalSec
	previous.FetchTimeoutSeconds = s.fetchTimeoutSec
	previous.ErrorCode = code
	previous.Activities = append([]im.SignActivity(nil), previous.Activities...)
	s.snapshots[uid] = previous
	return previous
}

func (s *IMSignService) accountLock(uid int64) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.locks[uid]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[uid] = lock
	}
	return lock
}

func (s *IMSignService) accountCredentials(ctx context.Context, uid int64) (imCredentialCache, error) {
	now := time.Now()
	s.mu.Lock()
	cached, ok := s.creds[uid]
	s.mu.Unlock()
	if ok && now.Before(cached.expiresAt) {
		if cached.code != "" {
			return imCredentialCache{}, &im.Error{Code: cached.code}
		}
		return cached, nil
	}

	fail := func(code string) (imCredentialCache, error) {
		s.mu.Lock()
		s.creds[uid] = imCredentialCache{code: code, expiresAt: now.Add(imCredentialFailTTL)}
		s.mu.Unlock()
		return imCredentialCache{}, &im.Error{Code: code}
	}

	user, err := LoadActiveUser(s.db, uid)
	if err != nil {
		return fail(im.CodeAuthFailed)
	}
	password, err := s.cc.Decrypt(user.CredentialCipher)
	if err != nil {
		return fail(im.CodeAuthFailed)
	}
	account, err := s.getIMAccount(ctx, user.Mobile, password)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// Do not cache a transient timeout as a credential failure.
		return imCredentialCache{}, &im.Error{Code: imErrorCode(err)}
	}
	if errors.Is(err, xxt.ErrIMAccountMissing) {
		return fail(im.CodeNotConfigured)
	}
	if err != nil {
		return fail(im.CodeAuthFailed)
	}
	plain, err := im.DecryptPassword(account.Password)
	if err != nil {
		return fail(im.CodeAuthFailed)
	}
	credentials := imCredentialCache{password: plain, expiresAt: now.Add(imCredentialTTL)}
	s.mu.Lock()
	s.creds[uid] = credentials
	s.mu.Unlock()
	return credentials, nil
}

type imAccountResult struct {
	account xxt.IMAccount
	err     error
}

// getIMAccount bounds the userInfo call even when session establishment cannot
// observe ctx: the request path returns on timeout, and the detached call ends
// at the existing HTTP client timeout.
func (s *IMSignService) getIMAccount(ctx context.Context, mobile, password string) (xxt.IMAccount, error) {
	resultCh := make(chan imAccountResult, 1)
	go func() {
		account, err := s.accounts.GetIMAccountContext(ctx, mobile, password)
		resultCh <- imAccountResult{account: account, err: err}
	}()
	select {
	case <-ctx.Done():
		return xxt.IMAccount{}, ctx.Err()
	case result := <-resultCh:
		return result.account, result.err
	}
}

func (s *IMSignService) accountToken(ctx context.Context, uid int64, password string) (im.Token, error) {
	now := time.Now()
	s.mu.Lock()
	cached, ok := s.tokens[uid]
	s.mu.Unlock()
	if ok && now.Before(cached.ExpiresAt.Add(-imTokenSafetyWindow)) {
		return cached, nil
	}
	token, err := s.client.Token(ctx, uid, password)
	if err != nil {
		return im.Token{}, err
	}
	s.mu.Lock()
	s.tokens[uid] = token
	s.mu.Unlock()
	return token, nil
}

func (s *IMSignService) dropToken(uid int64) {
	s.mu.Lock()
	delete(s.tokens, uid)
	s.mu.Unlock()
}

func imErrorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return im.CodeTimeout
	}
	var imErr *im.Error
	if errors.As(err, &imErr) {
		switch imErr.Code {
		case im.CodeAuthFailed, im.CodeNotConfigured, im.CodeUnauthorized,
			im.CodeTokenFailed, im.CodeGroupsFailed, im.CodeMessagesFailed,
			im.CodeParseFailed, im.CodeNetwork, im.CodeTimeout:
			return imErr.Code
		}
	}
	return im.CodeNetwork
}

func tokenErrorCode(err error) string {
	code := imErrorCode(err)
	switch code {
	case im.CodeAuthFailed, im.CodeNotConfigured, im.CodeTimeout, im.CodeUnauthorized:
		return code
	default:
		return im.CodeTokenFailed
	}
}
