package infrastructure

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestVaultPersistsConcurrentKey verifies concurrent first writes share a durable private key.
// TestVaultPersistsConcurrentKey 验证并发首次写入共用持久化私钥。
func TestVaultPersistsConcurrentKey(t *testing.T) {
	v := &Vault{path: filepath.Join(t.TempDir(), "credentials.key")}
	var wg sync.WaitGroup
	sealed := make([]string, 16)
	errs := make([]error, 16)
	for i := range sealed {
		wg.Add(1)
		go func(i int) { defer wg.Done(); sealed[i], errs[i] = v.Seal("token-secret") }(i)
	}
	wg.Wait()
	for i, raw := range sealed {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		plain, err := (&Vault{path: v.path}).Open(raw)
		if err != nil || plain != "token-secret" {
			t.Fatalf("decrypt %d: %v", i, err)
		}
		if raw == plain {
			t.Fatal("plaintext persisted")
		}
	}
	info, err := os.Stat(v.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("key permissions")
	}
	if _, err := v.Open("not ciphertext"); err == nil {
		t.Fatal("invalid ciphertext accepted")
	}
	if err := os.Remove(v.path); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Open(sealed[0]); err == nil {
		t.Fatal("missing key recreated for existing ciphertext")
	}
}
