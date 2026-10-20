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
	imCredentialTTL      = 10 * time.Minute
	imCredentialFailTTL  = 5 * time.Minute
	imAccountPollTimeout = 60 * time.Second
	imTokenSafetyWindow  = 30 * time.Second
)

// IMClient is the Easemob protocol surface used by the poller. It is
// satisfied by *im.Client and lets tests drive token/retry paths directly.
type IMClient interface {
	Token(ctx context.Context, uid int64, password string) (im.Token, error)
	Discover(ctx context.Context, token im.Token) ([]im.Group, []im.SignActivity, error)
}

// IMAccountProvider reads the account's Easemob credentials from the upstream
// userInfo endpoint. It is satisfied by *xxt.Client.
type IMAccountProvider interface {
	GetIMAccount(mobile, password string) (xxt.IMAccount, error)
}

// IMSnapshot is the last poll result for one account. Activities contains
// only parsed sign-card fields; raw roaming messages are never cached.
type IMSnapshot struct {
	Enabled         bool
	IntervalSeconds int
	LastPollAt      time.Time
	LastSuccessAt   time.Time
	ErrorCode       string
	GroupCount      int
	ActivityCount   int
	Activities      []im.SignActivity
}

type imCredentialCache struct {
	password  string
	code      string
	expiresAt time.Time
}

// IMSignService polls Easemob course groups for sign cards. One account is
// never polled concurrently: RefreshAccount uses a per-uid TryLock and returns
// the previous snapshot when another poll is already running.
type IMSignService struct {
	db          *gorm.DB
	accounts    IMAccountProvider
	client      IMClient
	cc          *CredentialCrypto
	interval    time.Duration
	enabled     bool
	intervalSec int

	mu        sync.Mutex
	snapshots map[int64]IMSnapshot
	creds     map[int64]imCredentialCache
	tokens    map[int64]im.Token
	locks     map[int64]*sync.Mutex
	known     map[int64]map[int64]struct{}
}

func NewIMSignService(db *gorm.DB, accounts IMAccountProvider, client IMClient, cc *CredentialCrypto, interval time.Duration, enabled bool) *IMSignService {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	intervalSec := int(interval / time.Second)
	if intervalSec < 1 {
		intervalSec = 1
	}
	return &IMSignService{
		db:          db,
		accounts:    accounts,
		client:      client,
		cc:          cc,
		interval:    interval,
		enabled:     enabled,
		intervalSec: intervalSec,
		snapshots:   make(map[int64]IMSnapshot),
		creds:       make(map[int64]imCredentialCache),
		tokens:      make(map[int64]im.Token),
		locks:       make(map[int64]*sync.Mutex),
		known:       make(map[int64]map[int64]struct{}),
	}
}

func (s *IMSignService) Interval() time.Duration { return s.interval }

// Snapshot returns a copy of the last poll result for uid.
func (s *IMSignService) Snapshot(uid int64) IMSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots[uid]
	if !ok {
		return IMSnapshot{Enabled: s.enabled, IntervalSeconds: s.intervalSec}
	}
	snapshot.Enabled = s.enabled
	snapshot.IntervalSeconds = s.intervalSec
	snapshot.Activities = append([]im.SignActivity(nil), snapshot.Activities...)
	return snapshot
}

// Start launches the polling loop. It polls once immediately, then every
// configured interval. A slow cycle simply delays the next one; overlapping
// cycles are never started.
func (s *IMSignService) Start(ctx context.Context) {
	if !s.enabled {
		log.Printf("im poll disabled")
		return
	}
	go func() {
		s.pollAll(ctx)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.pollAll(ctx)
			}
		}
	}()
}

func (s *IMSignService) pollAll(ctx context.Context) {
	var uids []int64
	err := s.db.Table("users u").
		Joins("JOIN whitelists w ON w.mobile = u.mobile").
		Where("u.permission > 0 AND w.permission > 0 AND length(u.credential_cipher) > 0").
		Where("EXISTS (SELECT 1 FROM user_courses uc WHERE uc.user_uid = u.uid AND uc.is_selected = true)").
		Order("u.uid asc").
		Pluck("u.uid", &uids).Error
	if err != nil {
		log.Printf("im poll: source=%s error=%s", im.SourceIMGroup, im.CodeNetwork)
		return
	}
	for _, uid := range uids {
		if ctx.Err() != nil {
			return
		}
		accountCtx, cancel := context.WithTimeout(ctx, imAccountPollTimeout)
		s.RefreshAccount(accountCtx, uid)
		cancel()
	}
}

// RefreshAccount performs one complete poll for uid. It never returns an error
// to callers: failures are recorded in the snapshot's ErrorCode so the normal
// activity list and sign flow stay available.
func (s *IMSignService) RefreshAccount(ctx context.Context, uid int64) IMSnapshot {
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
	snapshot.LastPollAt = startedAt

	credentials, err := s.accountCredentials(uid)
	if err != nil {
		return s.finishPoll(uid, snapshot, 0, nil, imErrorCode(err))
	}
	token, err := s.accountToken(ctx, uid, credentials.password)
	if err != nil {
		return s.finishPoll(uid, snapshot, 0, nil, tokenErrorCode(err))
	}

	groups, activities, err := s.client.Discover(ctx, token)
	if errors.Is(err, im.ErrUnauthorized) {
		s.dropToken(uid)
		token, tokenErr := s.accountToken(ctx, uid, credentials.password)
		if tokenErr != nil {
			return s.finishPoll(uid, snapshot, 0, nil, tokenErrorCode(tokenErr))
		}
		groups, activities, err = s.client.Discover(ctx, token)
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
	snapshot.LastPollAt = now
	snapshot.GroupCount = groupCount
	snapshot.ActivityCount = len(activities)
	snapshot.ErrorCode = code
	if code == "" {
		snapshot.LastSuccessAt = now
		snapshot.Activities = append([]im.SignActivity(nil), activities...)
	}

	s.mu.Lock()
	previous := s.snapshots[uid]
	if code == "" {
		s.snapshots[uid] = snapshot
	} else {
		// Keep the last successful activity set so a transient IM failure does
		// not erase discovered sign cards from the list.
		snapshot.Activities = previous.Activities
		snapshot.ActivityCount = len(previous.Activities)
		snapshot.LastSuccessAt = previous.LastSuccessAt
		s.snapshots[uid] = snapshot
	}
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
		log.Printf("im poll: uid=%d source=%s groups=%d activities=%d result=ok", uid, im.SourceIMGroup, groupCount, len(activities))
	} else {
		log.Printf("im poll: uid=%d source=%s result=%s", uid, im.SourceIMGroup, code)
	}
	return snapshot
}

func (s *IMSignService) snapshotWithError(uid int64, code string) IMSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.snapshots[uid]
	previous.Enabled = s.enabled
	previous.IntervalSeconds = s.intervalSec
	previous.ErrorCode = code
	previous.LastPollAt = time.Now()
	previous.Activities = append([]im.SignActivity(nil), previous.Activities...)
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

func (s *IMSignService) accountCredentials(uid int64) (imCredentialCache, error) {
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
	account, err := s.accounts.GetIMAccount(user.Mobile, password)
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
	var imErr *im.Error
	if errors.As(err, &imErr) {
		switch imErr.Code {
		case im.CodeAuthFailed, im.CodeNotConfigured, im.CodeUnauthorized,
			im.CodeTokenFailed, im.CodeGroupsFailed, im.CodeMessagesFailed,
			im.CodeParseFailed, im.CodeNetwork:
			return imErr.Code
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return im.CodeNetwork
	}
	return im.CodeNetwork
}

func tokenErrorCode(err error) string {
	code := imErrorCode(err)
	switch code {
	case im.CodeAuthFailed, im.CodeNotConfigured:
		return code
	default:
		return im.CodeTokenFailed
	}
}
