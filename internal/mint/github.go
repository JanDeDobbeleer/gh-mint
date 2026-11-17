package mint

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiURL       = "https://api.github.com"
	maxResponse  = 2 * 1024 * 1024
	apiVersion   = "2026-03-10"
	minTokenLife = 2 * time.Minute
)

type githubClient struct {
	http *http.Client
	now  func() time.Time
	base string
}

type installationToken struct {
	ExpiresAt time.Time `json:"expires_at"`
	Token     string    `json:"token"`
}

func newGitHubClient() *githubClient {
	return &githubClient{
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		base: apiURL,
		now:  time.Now,
	}
}

func signJWT(key *rsa.PrivateKey, appID int64, now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(struct {
		Issuer    string `json:"iss"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
	}{
		Issuer:    strconv.FormatInt(appID, 10),
		IssuedAt:  now.Add(-time.Minute).Unix(),
		ExpiresAt: now.Add(9 * time.Minute).Unix(),
	})
	if err != nil {
		return "", err
	}
	unsigned := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		return "", errors.New("cannot sign GitHub App authentication")
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (g *githubClient) mint(ctx context.Context, cfg config, repo repository, key *rsa.PrivateKey) (installationToken, error) {
	jwt, err := signJWT(key, cfg.AppID, g.now())
	if err != nil {
		return installationToken{}, err
	}
	var installation struct {
		ID int64 `json:"id"`
	}
	path := "/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name) + "/installation"
	if err := g.request(ctx, http.MethodGet, path, jwt, nil, http.StatusOK, &installation); err != nil {
		return installationToken{}, err
	}
	if installation.ID <= 0 {
		return installationToken{}, errors.New("GitHub returned an invalid installation")
	}
	body, err := json.Marshal(struct {
		Repositories []string `json:"repositories"`
	}{
		Repositories: []string{repo.Name},
	})
	if err != nil {
		return installationToken{}, err
	}

	var token installationToken
	path = fmt.Sprintf("/app/installations/%d/access_tokens", installation.ID)
	if err := g.request(ctx, http.MethodPost, path, jwt, body, http.StatusCreated, &token); err != nil {
		return installationToken{}, err
	}
	if strings.TrimSpace(token.Token) == "" || strings.ContainsAny(token.Token, " \t\r\n\x00") || !token.ExpiresAt.After(g.now().Add(minTokenLife)) {
		return installationToken{}, errors.New("GitHub returned an empty, invalid, or nearly expired installation token")
	}
	return token, nil
}

func (g *githubClient) revoke(ctx context.Context, token string) error {
	return g.request(ctx, http.MethodDelete, "/installation/token", token, nil, http.StatusNoContent, nil)
}

func (g *githubClient) request(ctx context.Context, method, path, token string, body []byte, expected int, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, g.base+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("cannot construct GitHub API request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "gh-mint")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := g.http.Do(req)
	if err != nil {
		return errors.New("GitHub API request failed; check connectivity and TLS configuration")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != expected {
		return fmt.Errorf("GitHub API returned HTTP %d for %s %s", response.StatusCode, method, path)
	}
	if result == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return errors.New("cannot read the GitHub API response")
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errors.New("GitHub returned an invalid JSON response")
	}
	return nil
}
