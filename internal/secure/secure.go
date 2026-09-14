package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"

	"golang.org/x/crypto/argon2"
)

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func EqualHash(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	return fmt.Sprintf("argon2id$v=19$m=65536,t=1,p=4$%s$%s", hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

func VerifyPassword(encoded, password string) bool {
	if len(encoded) > 512 {
		return false
	}
	parts := splitDollar(encoded)
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}
	if parts[1] != "v=19" || len(encoded) > 512 {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	// Reject corrupted/untrusted costs before allocation or the Argon2 call.
	if m < 8*uint32(p) || m > 65536 || t < 1 || t > 4 || p < 1 || p > 4 {
		return false
	}
	canonical := "m=" + strconv.FormatUint(uint64(m), 10) + ",t=" + strconv.FormatUint(uint64(t), 10) + ",p=" + strconv.FormatUint(uint64(p), 10)
	if parts[2] != canonical {
		return false
	}
	salt, err := hex.DecodeString(parts[3])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := hex.DecodeString(parts[4])
	if err != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(want, got) == 1
}

func splitDollar(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '$' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

func LoadOrCreateKey(path string, n int) ([]byte, error) {
	if n < 16 || n > 4096 {
		return nil, fmt.Errorf("invalid key length")
	}
	read := func() ([]byte, error) {
		fi, e := os.Lstat(path)
		if e != nil {
			return nil, e
		}
		if !fi.Mode().IsRegular() || fi.Size() != int64(n) {
			return nil, fmt.Errorf("existing key has invalid type or length; restore the original key")
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		if len(b) != n {
			return nil, fmt.Errorf("key length changed while reading")
		}
		return b, nil
	}
	if b, e := read(); e == nil {
		return b, nil
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return nil, e
	}
	// Exclusive creation never truncates a concurrently created/existing key.
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(e) {
		return read()
	}
	if e != nil {
		return nil, e
	}
	count, e := f.Write(b)
	if e == nil && count != len(b) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return nil, e
	}
	return b, nil
}

func Seal(master, plaintext []byte) (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(master)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return nonce, ciphertext, nil
}

func Open(master, nonce, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(master)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() || len(ciphertext) < gcm.Overhead() {
		return nil, fmt.Errorf("invalid encrypted record")
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func SHA256File(path string) (string, int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), int64(len(b)), nil
}

func SHA256Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
