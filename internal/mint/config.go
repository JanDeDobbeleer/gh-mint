package mint

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type config struct {
	KeyFile string `json:"key_file"`
	Copilot string `json:"copilot"`
	Remote  string `json:"remote"`
	AppID   int64  `json:"app_id"`
}

func configDirectory() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("cannot locate the user configuration directory")
	}
	return filepath.Join(base, "gh-mint"), nil
}

func readConfig(dir string) (config, error) {
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return config{}, errors.New("cannot read configuration; run 'gh mint setup --help'")
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, errors.New("invalid gh-mint configuration")
	}
	if err := cfg.validate(); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func (c config) validate() error {
	if c.AppID <= 0 {
		return errors.New("app ID must be positive")
	}
	if !filepath.IsAbs(c.KeyFile) || !filepath.IsAbs(c.Copilot) {
		return errors.New("key and Copilot paths must be absolute")
	}
	if c.Remote == "" || strings.HasPrefix(c.Remote, "-") || strings.ContainsAny(c.Remote, "\x00\r\n") {
		return errors.New("invalid Git remote name")
	}
	return nil
}

func writeConfig(dir string, cfg config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create the configuration directory")
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("cannot create configuration; existing configuration is never overwritten by setup")
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errors.New("cannot write configuration")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return errors.New("cannot finish writing configuration")
	}
	return nil
}

func readKey(path string) (*rsa.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, errors.New("private key must be a readable regular PEM file smaller than 64 KiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read the private key")
	}
	block, rest := pem.Decode(data)
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("private key must contain exactly one unencrypted RSA PEM block")
	}

	var key *rsa.PrivateKey
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var parsed any
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		if err == nil {
			var ok bool
			key, ok = parsed.(*rsa.PrivateKey)
			if !ok {
				return nil, errors.New("private key must be RSA")
			}
		}
	default:
		return nil, errors.New("private key must be an unencrypted PKCS#1 or PKCS#8 RSA PEM file")
	}
	if err != nil || key == nil {
		return nil, errors.New("cannot parse the RSA private key")
	}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("RSA private key must be at least 2048 bits")
	}
	return key, nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("cannot resolve path")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path %q", absolute)
	}
	return resolved, nil
}

func within(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func parseAppID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("app ID must be a positive integer")
	}
	return id, nil
}
