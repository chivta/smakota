package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
)

const (
	testKeyID    = "test-key"
	testAudience = "calbot-api"
	opaqueUserID = 7
	subjectUser  = 42
)

type fakeUsers struct {
	userStore
	subjects map[string]int64
	emails   map[string]string
}

func (f *fakeUsers) GetOrCreateBySubject(_ context.Context, subject, email string) (int64, error) {
	id, ok := f.subjects[subject]
	if !ok {
		id = subjectUser + int64(len(f.subjects))
		f.subjects[subject] = id
		f.emails[subject] = email
	}
	return id, nil
}

type fakeTokens struct{ tokenStore }

func (fakeTokens) Authenticate(_ context.Context, _ []byte) (int64, int64, error) {
	return opaqueUserID, 1, nil
}

func (fakeTokens) Revoke(context.Context, int64) error { return nil }

func TestIsJWT(t *testing.T) {
	tests := []struct {
		token string
		want  bool
	}{
		{"aaa.bbb.ccc", true},
		{"garbage.", true},
		{"sWU0kTKaZbAXkjDwjt3E4KsWspqQPxYqb38mHN5oldQ", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isJWT(tt.token); got != tt.want {
			t.Errorf("isJWT(%q) = %v, want %v", tt.token, got, tt.want)
		}
	}
}

func TestAuthMiddleware(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &oidctest.Server{PublicKeys: []oidctest.PublicKey{
		{PublicKey: priv.Public(), KeyID: testKeyID, Algorithm: oidc.RS256},
	}}
	srv := httptest.NewServer(idp)
	defer srv.Close()
	idp.SetIssuer(srv.URL)

	sign := func(iss, aud string, exp time.Time) string {
		claims := fmt.Sprintf(`{"iss":%q,"aud":%q,"sub":"idp-user","email":"Jane@Example.com","exp":%s}`,
			iss, aud, strconv.FormatInt(exp.Unix(), 10))
		return oidctest.SignIDToken(priv, testKeyID, oidc.RS256, claims)
	}
	future := time.Now().Add(time.Hour)

	users := &fakeUsers{subjects: map[string]int64{}, emails: map[string]string{}}
	build := func(issuer string) http.Handler {
		s := New(Deps{Users: users, Tokens: fakeTokens{}, OIDCIssuer: issuer, OIDCAudience: testAudience, AuthRateLimit: RateLimit{PerMinute: 1, Burst: 1}})
		r, err := s.Router(context.Background(), false)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	on, off := build(srv.URL), build("")

	tests := []struct {
		name   string
		h      http.Handler
		header string
		want   int
	}{
		{"no header", on, "", http.StatusUnauthorized},
		{"wrong scheme", on, "Basic abc", http.StatusUnauthorized},
		{"empty bearer", on, "Bearer ", http.StatusUnauthorized},
		{"opaque token", on, "Bearer opaque", http.StatusNoContent},
		{"valid jwt", on, "Bearer " + sign(srv.URL, testAudience, future), http.StatusNoContent},
		{"wrong audience", on, "Bearer " + sign(srv.URL, "other", future), http.StatusUnauthorized},
		{"wrong issuer", on, "Bearer " + sign("https://evil.example", testAudience, future), http.StatusUnauthorized},
		{"expired", on, "Bearer " + sign(srv.URL, testAudience, time.Now().Add(-time.Hour)), http.StatusUnauthorized},
		{"garbage jwt", on, "Bearer aaa.bbb.ccc", http.StatusUnauthorized},
		{"jwt with oidc off", off, "Bearer " + sign(srv.URL, testAudience, future), http.StatusUnauthorized},
		{"garbage jwt with oidc off", off, "Bearer aaa.bbb.ccc", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rec := httptest.NewRecorder()
			tt.h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}

	if len(users.subjects) != 1 || users.emails["idp-user"] != "jane@example.com" {
		t.Fatalf("subjects = %v, emails = %v; want one subject with a normalized email", users.subjects, users.emails)
	}
}

func TestRouterFailsWhenIssuerUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	s := New(Deps{OIDCIssuer: url, OIDCAudience: testAudience, AuthRateLimit: RateLimit{PerMinute: 1, Burst: 1}})
	_, err := s.Router(context.Background(), false)
	if err == nil {
		t.Fatal("router started with an unreachable issuer")
	}
}
