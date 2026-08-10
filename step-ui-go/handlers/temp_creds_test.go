package handlers

import (
	"sync"
	"testing"
	"time"
)

// TestTempCredStore_SingleUse is the display-once contract that replaced the
// cleartext cookie: the second read of a token finds nothing, so refreshing the
// result page cannot show the credential again (V7).
func TestTempCredStore_SingleUse(t *testing.T) {
	s := newTempCredStore()
	token := s.put("guest-ab12cd", "s3cret-p4ssw0rd")

	cred, ok := s.take(token)
	if !ok {
		t.Fatal("first take: got nothing, want the stored credential")
	}
	if cred.Username != "guest-ab12cd" || cred.Password != "s3cret-p4ssw0rd" {
		t.Errorf("first take: got %+v", cred)
	}
	if _, ok := s.take(token); ok {
		t.Error("second take returned the credential again")
	}
	if len(s.creds) != 0 {
		t.Errorf("store retained %d entries after the read", len(s.creds))
	}
}

func TestTempCredStore_UnknownAndEmptyToken(t *testing.T) {
	s := newTempCredStore()
	s.put("guest-ab12cd", "s3cret-p4ssw0rd")
	for _, token := range []string{"", "not-a-real-token"} {
		if _, ok := s.take(token); ok {
			t.Errorf("take(%q): got a credential, want none", token)
		}
	}
	if len(s.creds) != 1 {
		t.Errorf("a failed lookup disturbed the store: %d entries want 1", len(s.creds))
	}
}

// TestTempCredStore_ExpiredIsRefusedAndEvicted keeps an uncollected credential
// from living in memory, or being handed over, past its TTL.
func TestTempCredStore_ExpiredIsRefusedAndEvicted(t *testing.T) {
	s := newTempCredStore()
	stale := s.put("guest-stale0", "stale-password")
	s.mu.Lock()
	entry := s.creds[stale]
	entry.expires = time.Now().Add(-time.Second)
	s.creds[stale] = entry
	s.mu.Unlock()

	if _, ok := s.take(stale); ok {
		t.Error("an expired credential was handed over")
	}

	// A second expired entry must be evicted by an unrelated access rather than
	// waiting for a reader that never comes.
	other := s.put("guest-stale1", "stale-password")
	s.mu.Lock()
	entry = s.creds[other]
	entry.expires = time.Now().Add(-time.Second)
	s.creds[other] = entry
	s.mu.Unlock()

	s.put("guest-fresh0", "fresh-password")
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, stillThere := s.creds[other]; stillThere {
		t.Error("an expired entry survived a later access: the map can grow without bound")
	}
}

func TestTempCredStore_TokensAreDistinct(t *testing.T) {
	s := newTempCredStore()
	seen := map[string]bool{}
	for range 100 {
		token := s.put("guest-ab12cd", "s3cret-p4ssw0rd")
		if token == "" {
			t.Fatal("put returned an empty token")
		}
		if seen[token] {
			t.Fatalf("put reused token %q", token)
		}
		seen[token] = true
	}
}

// TestTempCredStore_ConcurrentAccess runs under -race in CI.
func TestTempCredStore_ConcurrentAccess(t *testing.T) {
	s := newTempCredStore()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.take(s.put("guest-ab12cd", "s3cret-p4ssw0rd"))
		}()
	}
	wg.Wait()
	if len(s.creds) != 0 {
		t.Errorf("store retained %d entries", len(s.creds))
	}
}
