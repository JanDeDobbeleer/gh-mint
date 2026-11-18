package mint

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"

	"github.com/youmark/pkcs8"
)

type passwordPrompt func(string) ([]byte, error)

func readKeyBlock(path string) (*pem.Block, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, errors.New("private key must be a readable regular PEM file smaller than 64 KiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read the private key")
	}
	defer clear(data)
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("private key must contain exactly one RSA PEM block")
	}
	return block, nil
}

func readKey(path string, prompt passwordPrompt) (*rsa.PrivateKey, error) {
	block, err := readKeyBlock(path)
	if err != nil {
		return nil, err
	}
	defer clear(block.Bytes)
	var parsed any
	switch block.Type {
	case "RSA PRIVATE KEY":
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "ENCRYPTED PRIVATE KEY":
		if prompt == nil {
			return nil, errors.New("private key is encrypted; an interactive passphrase prompt is required")
		}
		password, promptErr := prompt("Private key passphrase: ")
		if promptErr != nil {
			clear(password)
			return nil, promptErr
		}
		defer clear(password)
		if len(password) == 0 {
			return nil, errors.New("private key passphrase cannot be empty")
		}
		parsed, err = pkcs8.ParsePKCS8PrivateKey(block.Bytes, password)
		if err != nil {
			return nil, errors.New("cannot decrypt private key: incorrect passphrase or invalid encrypted PKCS#8")
		}
	default:
		return nil, errors.New("private key must be a PKCS#1 or PKCS#8 RSA PEM file")
	}
	if err != nil {
		return nil, errors.New("cannot parse the RSA private key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key must be RSA")
	}
	if key.N.BitLen() < 2048 {
		return nil, errors.New("RSA private key must be at least 2048 bits")
	}
	if key.Validate() != nil {
		return nil, errors.New("invalid RSA private key")
	}
	return key, nil
}

func writeEncryptedKey(dir string, key *rsa.PrivateKey, password []byte) (string, error) {
	if len(password) == 0 {
		return "", errors.New("private key passphrase cannot be empty")
	}
	der, err := pkcs8.MarshalPrivateKey(key, password, &pkcs8.Opts{
		Cipher: pkcs8.AES256CBC,
		KDFOpts: pkcs8.PBKDF2Opts{
			SaltSize: 16, IterationCount: 600_000, HMACHash: crypto.SHA256,
		},
	})
	if err != nil {
		return "", errors.New("cannot encrypt the private key")
	}
	path := filepath.Join(dir, "keys", "app.encrypted.pem")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", errors.New("cannot create the encrypted key directory")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", errors.New("cannot create encrypted key; an existing key is never overwritten")
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	err = pem.Encode(file, &pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: der})
	syncErr := file.Sync()
	closeErr := file.Close()
	if err != nil || syncErr != nil || closeErr != nil {
		return "", errors.New("cannot write the encrypted key")
	}
	verified, err := readKey(path, func(string) ([]byte, error) { return bytes.Clone(password), nil })
	if err != nil || !verified.Equal(key) {
		return "", errors.New("cannot verify the saved encrypted key")
	}
	canonical, err := canonicalPath(path)
	if err != nil {
		return "", err
	}
	keep = true
	return canonical, nil
}
