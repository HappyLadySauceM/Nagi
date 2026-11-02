// Package infrastructure implements encrypted credential storage.
// Package infrastructure 实现加密凭证存储。
package infrastructure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Vault struct{ path string }

// NewVault stores the key in the system configuration directory, outside the repository.
// NewVault 将密钥保存在仓库外的系统配置目录中。
func NewVault() (*Vault, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &Vault{path: filepath.Join(dir, "nagi", "credentials.key")}, nil
}

func (v *Vault) key(create bool) ([]byte, error) {
	data, err := os.ReadFile(v.path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err = os.MkdirAll(filepath.Dir(v.path), 0700); err != nil {
			return nil, err
		}
		data = make([]byte, 32)
		if _, err = rand.Read(data); err != nil {
			return nil, err
		}
		// Publish a complete key atomically; concurrent bootstraps must share the winner.
		// 原子发布完整密钥，并发初始化必须使用最终胜出的同一密钥。
		f, e := os.CreateTemp(filepath.Dir(v.path), ".credentials-*")
		if e != nil {
			return nil, e
		}
		defer os.Remove(f.Name())
		if e = f.Chmod(0600); e == nil {
			_, e = f.Write(data)
		}
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return nil, e
		}
		e = os.Link(f.Name(), v.path)
		if e != nil && !errors.Is(e, os.ErrExist) {
			return nil, e
		}
		data, err = os.ReadFile(v.path)
	}
	if err != nil {
		return nil, fmt.Errorf("credentials: key unavailable")
	}
	info, err := os.Stat(v.path)
	if err != nil || info.Mode().Perm()&0077 != 0 || len(data) != 32 {
		return nil, fmt.Errorf("credentials: invalid key file")
	}
	return data, nil
}
func (v *Vault) aead(create bool) (cipher.AEAD, error) {
	key, err := v.key(create)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (v *Vault) Seal(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	a, err := v.aead(true)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(a.Seal(nonce, nonce, []byte(value), []byte("nagi-credentials-v1"))), nil
}
func (v *Vault) Open(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	a, err := v.aead(false)
	if err != nil {
		return "", err
	}
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(b) < a.NonceSize() {
		return "", fmt.Errorf("credentials: invalid ciphertext")
	}
	plain, err := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte("nagi-credentials-v1"))
	if err != nil {
		return "", fmt.Errorf("credentials: decryption failed")
	}
	return string(plain), nil
}
