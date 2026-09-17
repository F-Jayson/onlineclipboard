// Package clipcrypto is the reference v1 envelope implementation used by tests
// and client interoperability checks. HTTP handlers must never decrypt user
// ciphertext or accept a recovery key.
package clipcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	WrapInfo             = "onlineclipboard/v1/wrap"
	PasswordWrapInfo     = "onlineclipboard/v1/wrap-password"
	PasswordKDF          = "argon2id"
	PasswordKDFTime      = 3
	PasswordKDFMemoryKiB = 64 * 1024
	PasswordKDFParallel  = 1
	FormatV1             = 1
	EpochV1              = 1
)

func Decode(value string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if base64.RawURLEncoding.EncodeToString(raw) != value {
		return nil, fmt.Errorf("non-canonical base64url")
	}
	return raw, nil
}

func Encode(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func Derive(ikm, salt, info []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, ikm, salt, string(info), 32)
}

func AESGCM(key, nonce, plaintext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("nonce must be %d bytes", aead.NonceSize())
	}
	return aead.Seal(nil, nonce, plaintext, aad), nil
}

func OpenAESGCM(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, aad)
}

func WrapAAD(userID, vaultID string) []byte {
	return []byte("oc-v1|vault|" + userID + "|" + vaultID + "|1")
}

func PasswordWrapAAD(userID, vaultID string) []byte {
	return []byte("oc-v1|vault-password|" + userID + "|" + vaultID + "|1")
}

func PasswordIKM(password string, salt []byte, time, memory uint32, parallel uint8) []byte {
	if time == 0 {
		time = PasswordKDFTime
	}
	if memory == 0 {
		memory = PasswordKDFMemoryKiB
	}
	if parallel == 0 {
		parallel = PasswordKDFParallel
	}
	return argon2.IDKey([]byte(password), salt, time, memory, parallel, 32)
}

func ItemInfo(clipID string, keyEpoch int) []byte {
	return []byte(fmt.Sprintf("onlineclipboard/v1/item|%s|%d", clipID, keyEpoch))
}

func ItemAAD(userID, vaultID, clipID, sourceDeviceID string, keyEpoch int) []byte {
	return []byte(strings.Join([]string{
		"oc-v1", "clip", userID, vaultID, clipID, sourceDeviceID, fmt.Sprintf("%d", keyEpoch), "text/plain",
	}, "|"))
}

func WrapCMK(rk, wrapSalt, wrapNonce, cmk []byte, userID, vaultID string) ([]byte, error) {
	kek, err := Derive(rk, wrapSalt, []byte(WrapInfo))
	if err != nil {
		return nil, err
	}
	return AESGCM(kek, wrapNonce, cmk, WrapAAD(userID, vaultID))
}

func UnwrapCMK(rk, wrapSalt, wrapNonce, wrapped []byte, userID, vaultID string) ([]byte, error) {
	kek, err := Derive(rk, wrapSalt, []byte(WrapInfo))
	if err != nil {
		return nil, err
	}
	return OpenAESGCM(kek, wrapNonce, wrapped, WrapAAD(userID, vaultID))
}

func WrapCMKWithPassword(password string, kdfSalt, wrapSalt, wrapNonce, cmk []byte, userID, vaultID string) ([]byte, error) {
	ikm := PasswordIKM(password, kdfSalt, PasswordKDFTime, PasswordKDFMemoryKiB, PasswordKDFParallel)
	kek, err := Derive(ikm, wrapSalt, []byte(PasswordWrapInfo))
	if err != nil {
		return nil, err
	}
	return AESGCM(kek, wrapNonce, cmk, PasswordWrapAAD(userID, vaultID))
}

func UnwrapCMKWithPassword(password string, kdfSalt, wrapSalt, wrapNonce, wrapped []byte, userID, vaultID string, time, memory uint32, parallel uint8) ([]byte, error) {
	ikm := PasswordIKM(password, kdfSalt, time, memory, parallel)
	kek, err := Derive(ikm, wrapSalt, []byte(PasswordWrapInfo))
	if err != nil {
		return nil, err
	}
	return OpenAESGCM(kek, wrapNonce, wrapped, PasswordWrapAAD(userID, vaultID))
}

func EncryptItem(cmk []byte, vaultID, userID, clipID, sourceDeviceID string, keyEpoch int, nonce []byte, plaintext []byte) ([]byte, error) {
	key, err := Derive(cmk, []byte(vaultID), ItemInfo(clipID, keyEpoch))
	if err != nil {
		return nil, err
	}
	return AESGCM(key, nonce, plaintext, ItemAAD(userID, vaultID, clipID, sourceDeviceID, keyEpoch))
}

func DecryptItem(cmk []byte, vaultID, userID, clipID, sourceDeviceID string, keyEpoch int, nonce, ciphertext []byte) ([]byte, error) {
	key, err := Derive(cmk, []byte(vaultID), ItemInfo(clipID, keyEpoch))
	if err != nil {
		return nil, err
	}
	return OpenAESGCM(key, nonce, ciphertext, ItemAAD(userID, vaultID, clipID, sourceDeviceID, keyEpoch))
}

func RecoveryCode(rk []byte) string {
	return "oc1_" + Encode(rk)
}

func ParseRecoveryCode(code string) ([]byte, error) {
	code = strings.ReplaceAll(code, " ", "")
	if !strings.HasPrefix(code, "oc1_") {
		return nil, fmt.Errorf("recovery code must start with oc1_")
	}
	raw, err := Decode(strings.TrimPrefix(code, "oc1_"))
	if err != nil {
		return nil, err
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("recovery key must be 32 bytes")
	}
	return raw, nil
}
