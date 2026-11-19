package mint

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testHTTPResponse(request *http.Request, status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

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
			var requestedToken tokenRequest
			var tokenRequestSeen bool
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				assert.True(t, strings.HasPrefix(request.Header.Get("Authorization"), "Bearer "))
				jwt := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
				assertValidJWT(t, jwt, key, now)
				switch request.URL.Path {
				case "/repos/owner/project/installation":
					assert.Equal(t, http.MethodGet, request.Method)
					return testHTTPResponse(request, http.StatusOK, nil, `{"id":123}`), nil
				case "/app/installations/123/access_tokens":
					assert.Equal(t, http.MethodPost, request.Method)
					tokenRequestSeen = true
					assert.NoError(t, json.NewDecoder(request.Body).Decode(&requestedToken))
					response, err := json.Marshal(map[string]any{
						"token": tt.token, "expires_at": tt.expiry, "message": "do-not-echo-this-secret",
					})
					require.NoError(t, err)
					return testHTTPResponse(request, tt.mintCode, nil, string(response)), nil
				default:
					t.Errorf("unexpected API path %s", request.URL.Path)
					return testHTTPResponse(request, http.StatusNotFound, nil, ""), nil
				}
			})
			client := &githubClient{
				base: "https://api.github.test",
				http: &http.Client{Transport: transport},
				now:  func() time.Time { return now },
			}
			cfg := config{AppID: 42}
			token, err := client.mint(t.Context(), cfg, repository{Owner: "owner", Name: "project"}, key)
			require.True(t, tokenRequestSeen, "expected a scoped token request")
			assert.Equal(t, []string{"project"}, requestedToken.Repositories)
			assert.Empty(t, requestedToken.Permissions, "omit permissions entirely so GitHub inherits the installation's grants")
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
			var requests atomic.Int32
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requests.Add(1)
				if request.URL.Host == "target.github.test" {
					leaked.Store(request.Header.Get("Authorization") != "")
					return testHTTPResponse(request, http.StatusNoContent, nil, ""), nil
				}
				return testHTTPResponse(request, tt.code, http.Header{
					"Location": []string{"https://target.github.test/"},
				}, ""), nil
			})
			client := &githubClient{
				base: "https://api.github.test",
				http: &http.Client{Transport: transport},
			}
			err := client.revoke(context.Background(), "private-test-token")
			require.ErrorContains(t, err, fmt.Sprintf("HTTP %d", tt.code))
			assert.NotContains(t, err.Error(), "private-test-token")
			assert.False(t, leaked.Load())
			assert.EqualValues(t, 1, requests.Load())
		})
	}
}
