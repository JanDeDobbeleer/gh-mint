package mint

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallationTokenIsLimitedToSelectedRepository(t *testing.T) {
	key := testKey(t)
	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		expiry   time.Time
		name     string
		token    string
		err      string
		mintCode int
	}{
		{name: "one repository", mintCode: http.StatusCreated, expiry: now.Add(time.Hour), token: "scoped-token"},
		{name: "permission refused", mintCode: http.StatusForbidden, err: "HTTP 403"},
		{name: "empty token", mintCode: http.StatusCreated, expiry: now.Add(time.Hour), err: "empty, invalid"},
		{name: "expired token", mintCode: http.StatusCreated, expiry: now.Add(-time.Minute), token: "expired", err: "nearly expired"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			type tokenRequest struct {
				Permissions  json.RawMessage `json:"permissions"`
				Repositories []string        `json:"repositories"`
			}
			requests := make(chan tokenRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
				jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				assertValidJWT(t, jwt, key, now)
				switch r.URL.Path {
				case "/repos/owner/project/installation":
					assert.Equal(t, http.MethodGet, r.Method)
					_, _ = fmt.Fprint(w, `{"id":123}`)
				case "/app/installations/123/access_tokens":
					assert.Equal(t, http.MethodPost, r.Method)
					var body tokenRequest
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					requests <- body
					w.WriteHeader(tt.mintCode)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"token": tt.token, "expires_at": tt.expiry, "message": "do-not-echo-this-secret",
					})
				default:
					t.Errorf("unexpected API path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := &githubClient{base: server.URL, http: server.Client(), now: func() time.Time { return now }}
			cfg := config{AppID: 42}
			token, err := client.mint(t.Context(), cfg, repository{Owner: "owner", Name: "project"}, key)
			select {
			case body := <-requests:
				assert.Equal(t, []string{"project"}, body.Repositories)
				assert.Empty(t, body.Permissions, "omit permissions entirely so GitHub inherits the installation's grants")
			default:
				t.Fatal("expected a scoped token request")
			}
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				assert.NotContains(t, err.Error(), "do-not-echo-this-secret")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.token, token.Token)
			assert.Equal(t, tt.expiry, token.ExpiresAt)
		})
	}
}

func assertValidJWT(t *testing.T, jwt string, key *rsa.PrivateKey, now time.Time) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if !assert.Len(t, parts, 3) {
		return
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if !assert.NoError(t, err) {
		return
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	assert.NoError(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature))
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if !assert.NoError(t, err) {
		return
	}
	var claims struct {
		Issuer    string `json:"iss"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
	}
	if !assert.NoError(t, json.Unmarshal(data, &claims)) {
		return
	}
	assert.Equal(t, "42", claims.Issuer)
	assert.LessOrEqual(t, claims.IssuedAt, now.Unix())
	assert.Greater(t, claims.ExpiresAt, now.Unix())
	assert.LessOrEqual(t, claims.ExpiresAt, now.Add(10*time.Minute).Unix())
}

func TestGitHubClientDoesNotForwardCredentialsOnRedirect(t *testing.T) {
	tests := []struct {
		name string
		code int
	}{
		{name: "permanent", code: http.StatusMovedPermanently},
		{name: "temporary", code: http.StatusTemporaryRedirect},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var leaked atomic.Bool
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				leaked.Store(true)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, tt.code)
			}))
			defer server.Close()
			client := newGitHubClient()
			client.base = server.URL
			err := client.revoke(context.Background(), "private-test-token")
			require.ErrorContains(t, err, fmt.Sprintf("HTTP %d", tt.code))
			assert.NotContains(t, err.Error(), "private-test-token")
			assert.False(t, leaked.Load())
		})
	}
}
