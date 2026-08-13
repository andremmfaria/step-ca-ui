package handlers

import (
	"sync"
	"time"

	"step-ui/security"
)

// tempCredTTL only has to cover a redirect, so it is short.
const tempCredTTL = 2 * time.Minute

// tempCred is one generated temporary-user credential awaiting its single view.
type tempCred struct {
	Username string
	Password string
	expires  time.Time
}

// tempCredStore carries a generated credential from the POST that made it to
// the redirected GET that displays it, keeping the secret out of a cookie and
// out of the database (V7). Deliberately process-local and unpersisted: losing
// an uncollected credential to a restart is the right direction to fail.
type tempCredStore struct {
	mu    sync.Mutex
	creds map[string]tempCred
}

// tempCreds is the process-global store, in the shape of security.RL.
var tempCreds = newTempCredStore()

// newTempCredStore returns an initialised store.
func newTempCredStore() *tempCredStore {
	return &tempCredStore{creds: make(map[string]tempCred)}
}

// put files a credential under a fresh unguessable token and returns it.
func (s *tempCredStore) put(username, password string) string {
	token := security.GenerateToken()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictExpired(time.Now())
	s.creds[token] = tempCred{Username: username, Password: password, expires: time.Now().Add(tempCredTTL)}
	return token
}

// take returns the credential for token and deletes it, so a refresh or a
// second reader finds nothing.
func (s *tempCredStore) take(token string) (tempCred, bool) {
	if token == "" {
		return tempCred{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.evictExpired(now)
	cred, ok := s.creds[token]
	delete(s.creds, token)
	if !ok || now.After(cred.expires) {
		return tempCred{}, false
	}
	return cred, true
}

// evictExpired drops timed-out entries. Every access calls it, so a credential
// nobody collects cannot accumulate. The caller holds s.mu.
func (s *tempCredStore) evictExpired(now time.Time) {
	for token, cred := range s.creds {
		if now.After(cred.expires) {
			delete(s.creds, token)
		}
	}
}
