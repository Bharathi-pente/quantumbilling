package byok

import (
	"testing"
)

// TC-01: AES-256-GCM encrypt/decrypt roundtrip
func TestTC01_EncryptDecryptRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key { key[i] = byte(i) }

	plaintext := []byte("sk-my-provider-secret-key-12345")
	ciphertext, iv, err := encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}
	if len(iv) != 12 {
		t.Errorf("expected 12-byte IV, got %d", len(iv))
	}

	decrypted, err := decrypt(key, ciphertext, iv)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("roundtrip mismatch: got %s, want %s", decrypted, plaintext)
	}
}

// TC-02: GCM auth fails on tampered ciphertext
func TestTC02_TamperedCiphertextFails(t *testing.T) {
	key := make([]byte, 32)
	for i := range key { key[i] = byte(i) }

	plaintext := []byte("secret-key")
	ciphertext, iv, _ := encrypt(key, plaintext)

	// Flip a byte in ciphertext
	ciphertext[0] ^= 0xFF

	_, err := decrypt(key, ciphertext, iv)
	if err == nil {
		t.Fatal("GCM must reject tampered ciphertext")
	}
}

// TC-03: Fresh IV per encryption
func TestTC03_FreshIVPerEncryption(t *testing.T) {
	key := make([]byte, 32)
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		_, iv, err := encrypt(key, []byte("test"))
		if err != nil {
			t.Fatal(err)
		}
		ivStr := string(iv)
		if seen[ivStr] {
			t.Errorf("IV collision at iteration %d", i)
		}
		seen[ivStr] = true
	}
}

// TC-04: resolveMasterKey produces 32 bytes
func TestTC04_MasterKeyLength(t *testing.T) {
	mk := resolveMasterKey()
	if len(mk) != 32 {
		t.Errorf("master key must be 32 bytes, got %d", len(mk))
	}
}

// TC-05: Empty plaintext encrypts and decrypts
func TestTC05_EmptyPlaintext(t *testing.T) {
	key := make([]byte, 32)
	ciphertext, iv, err := encrypt(key, []byte{})
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := decrypt(key, ciphertext, iv)
	if err != nil {
		t.Fatal(err)
	}
	if len(decrypted) != 0 {
		t.Error("empty plaintext roundtrip failed")
	}
}
