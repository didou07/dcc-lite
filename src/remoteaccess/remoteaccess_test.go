package remoteaccess

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type memStore struct {
	salt, hash string
	iterations int
	saves      int
}

func (m *memStore) PasswordRecord() (string, string, int) { return m.salt, m.hash, m.iterations }
func (m *memStore) SavePasswordRecord(s, h string, i int) error {
	m.salt, m.hash, m.iterations = s, h, i
	m.saves++
	return nil
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword(strings.Repeat("a", MinPasswordLength-1)) == nil {
		t.Error("short password accepted")
	}
	if ValidatePassword(strings.Repeat("a", MinPasswordLength)) != nil {
		t.Error("minimum length rejected")
	}
	if ValidatePassword(strings.Repeat("a", maxPasswordLength+1)) == nil {
		t.Error("overlong password accepted")
	}
}

func TestCredentialsSetVerifyAndPersist(t *testing.T) {
	store := &memStore{}
	c, err := OpenCredentials(store)
	if err != nil {
		t.Fatal(err)
	}
	if c.Configured() || c.Verify("anything-at-all") {
		t.Fatal("empty credentials must reject everything")
	}
	if c.Set("short") == nil || store.saves != 0 {
		t.Fatal("a weak password was saved")
	}
	if err := c.Set("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if !c.Configured() || !c.Verify("correct horse battery") || c.Verify("correct horse batterY") || c.Verify("") {
		t.Fatal("verification wrong")
	}
	if strings.Contains(store.hash, "correct") || store.iterations != pbkdf2Iterations {
		t.Fatalf("stored record looks wrong: %+v", store)
	}
	again, err := OpenCredentials(store)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Verify("correct horse battery") {
		t.Fatal("password does not survive a restart")
	}
	if again.Verify(strings.Repeat("x", maxPasswordLength+1)) {
		t.Fatal("overlong guess accepted")
	}
}

func TestOpenCredentialsRejectsDamagedRecords(t *testing.T) {
	good := &memStore{}
	c, _ := OpenCredentials(good)
	if err := c.Set("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	bad := []memStore{
		{salt: good.salt, hash: "", iterations: pbkdf2Iterations},
		{salt: good.salt, hash: good.hash, iterations: 1000},
		{salt: "!!!notbase64", hash: good.hash, iterations: pbkdf2Iterations},
		{salt: good.salt, hash: "AAAA", iterations: pbkdf2Iterations},
	}
	for i := range bad {
		if _, err := OpenCredentials(&bad[i]); err == nil {
			t.Errorf("record %d accepted", i)
		}
	}
}

func TestGenerateProducesStrongUsablePassword(t *testing.T) {
	store := &memStore{}
	c, _ := OpenCredentials(store)
	p1, err := c.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != 23 || strings.Count(p1, "-") != 3 || ValidatePassword(p1) != nil || !c.Verify(p1) {
		t.Fatalf("generated password %q is unusable", p1)
	}
	if strings.ContainsAny(p1, "0O1lI") {
		t.Errorf("ambiguous characters in %q", p1)
	}
	p2, _ := c.Generate()
	if p1 == p2 {
		t.Fatal("two generated passwords are identical")
	}
	if c.Verify(p1) {
		t.Fatal("the old password still works after regenerating")
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestAuth(t *testing.T, password string) (*Auth, *clock) {
	t.Helper()
	c, err := OpenCredentials(&memStore{})
	if err != nil {
		t.Fatal(err)
	}
	if password != "" {
		if err := c.Set(password); err != nil {
			t.Fatal(err)
		}
	}
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	a := NewAuth(c)
	a.now = clk.now
	a.sleep = func(time.Duration) {}
	return a, clk
}

func request(remote string) *http.Request {
	r := httptest.NewRequest("POST", "https://box.example/login", nil)
	r.RemoteAddr = remote
	return r
}

func TestLoginLockoutAndRecovery(t *testing.T) {
	a, clk := newTestAuth(t, "")
	r := request("203.0.113.7:5555")
	for i := 0; i < freeAttempts; i++ {
		if err := a.Login(httptest.NewRecorder(), r, "guess"); !errors.Is(err, ErrBadPassword) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var throttled ErrThrottled
	err := a.Login(httptest.NewRecorder(), r, "guess")
	if !errors.As(err, &throttled) || throttled.RetryAfter <= 0 || throttled.RetryAfter > baseLockout {
		t.Fatalf("expected throttling, got %v", err)
	}
	// A different client is not affected.
	if err := a.Login(httptest.NewRecorder(), request("198.51.100.9:1"), "guess"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("other client: %v", err)
	}
	clk.t = clk.t.Add(baseLockout + time.Second)
	if err := a.Login(httptest.NewRecorder(), r, "guess"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("after the lockout expired: %v", err)
	}
}

func TestLockoutGrowsButIsCapped(t *testing.T) {
	a, clk := newTestAuth(t, "")
	r := request("203.0.113.7:1")
	var last time.Duration
	for i := 0; i < 40; i++ {
		_ = a.Login(httptest.NewRecorder(), r, "x")
		var th ErrThrottled
		if errors.As(a.Login(httptest.NewRecorder(), r, "x"), &th) {
			last = th.RetryAfter
			clk.t = clk.t.Add(th.RetryAfter + time.Millisecond)
		}
	}
	if last <= 0 || last > maxLockout {
		t.Fatalf("lockout = %v, must be within (0, %v]", last, maxLockout)
	}
}

func TestLoginSessionLifecycle(t *testing.T) {
	a, clk := newTestAuth(t, "correct horse battery")
	w := httptest.NewRecorder()
	if err := a.Login(w, request("192.0.2.1:1"), "wrong password!"); !errors.Is(err, ErrBadPassword) {
		t.Fatal(err)
	}
	if err := a.Login(w, request("192.0.2.1:1"), "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}
	ck := cookies[0]
	if ck.Name != CookieName || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" || len(ck.Value) < 40 {
		t.Fatalf("insecure cookie: %+v", ck)
	}
	withCookie := func() *http.Request {
		r := request("192.0.2.1:1")
		r.AddCookie(ck)
		return r
	}
	if !a.Authenticated(withCookie()) {
		t.Fatal("fresh session rejected")
	}
	if a.Authenticated(request("192.0.2.1:1")) {
		t.Fatal("request without a cookie accepted")
	}
	forged := request("192.0.2.1:1")
	forged.AddCookie(&http.Cookie{Name: CookieName, Value: strings.Repeat("A", 43)})
	if a.Authenticated(forged) {
		t.Fatal("forged cookie accepted")
	}

	clk.t = clk.t.Add(sessionIdleTimeout - time.Hour)
	if !a.Authenticated(withCookie()) { // activity refreshes the idle timer
		t.Fatal("session expired too early")
	}
	clk.t = clk.t.Add(sessionIdleTimeout + time.Minute)
	if a.Authenticated(withCookie()) {
		t.Fatal("idle session did not expire")
	}
}

func TestSessionMaxAgeLogoutAndRevokeAll(t *testing.T) {
	a, clk := newTestAuth(t, "correct horse battery")
	login := func() *http.Cookie {
		w := httptest.NewRecorder()
		if err := a.Login(w, request("192.0.2.1:1"), "correct horse battery"); err != nil {
			t.Fatal(err)
		}
		return w.Result().Cookies()[0]
	}
	req := func(c *http.Cookie) *http.Request {
		r := request("192.0.2.1:1")
		r.AddCookie(c)
		return r
	}
	c1 := login()
	for i := 0; i < 12; i++ {
		clk.t = clk.t.Add(sessionMaxAge / 10)
		if !a.Authenticated(req(c1)) {
			break
		}
	}
	if a.Authenticated(req(c1)) {
		t.Fatal("session outlived its maximum age")
	}

	c2, c3 := login(), login()
	a.Logout(httptest.NewRecorder(), req(c2))
	if a.Authenticated(req(c2)) || !a.Authenticated(req(c3)) {
		t.Fatal("Logout must end only its own session")
	}
	a.RevokeAll()
	if a.Authenticated(req(c3)) {
		t.Fatal("RevokeAll left a session alive")
	}
}

func TestSessionCapEvictsOldest(t *testing.T) {
	a, clk := newTestAuth(t, "")
	for i := 0; i < maxSessions+5; i++ {
		clk.t = clk.t.Add(time.Second)
		a.mu.Lock()
		if len(a.sessions) >= maxSessions {
			a.evictOldestLocked()
		}
		a.sessions[[32]byte{byte(i), byte(i >> 8)}] = &session{created: clk.t, seen: clk.t}
		a.mu.Unlock()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions) != maxSessions {
		t.Fatalf("%d sessions, want %d", len(a.sessions), maxSessions)
	}
	if _, ok := a.sessions[[32]byte{0, 0}]; ok {
		t.Fatal("the oldest session survived eviction")
	}
}

func TestSameOrigin(t *testing.T) {
	mk := func(host, origin string) *http.Request {
		r := httptest.NewRequest("POST", "https://"+host+"/x", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	good := [][2]string{{"box.example", "https://box.example"}, {"Box.Example:8443", "https://box.example:8443"}}
	bad := [][2]string{
		{"box.example", ""}, {"box.example", "http://box.example"}, {"box.example", "https://evil.example"},
		{"box.example", "https://box.example.evil.example"}, {"box.example", "https://box.example/path"},
		{"box.example", "https://user@box.example"}, {"box.example", "null"}, {"box.example:8443", "https://box.example"},
	}
	for _, c := range good {
		if !SameOrigin(mk(c[0], c[1])) {
			t.Errorf("%v rejected", c)
		}
	}
	for _, c := range bad {
		if SameOrigin(mk(c[0], c[1])) {
			t.Errorf("%v accepted", c)
		}
	}
	if SameOrigin(nil) {
		t.Error("nil request accepted")
	}
}
