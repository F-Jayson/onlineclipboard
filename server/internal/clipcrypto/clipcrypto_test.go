package clipcrypto_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"onlineclipboard/server/internal/clipcrypto"
)

type fixture struct {
	UserID         string `json:"user_id"`
	CMKHex         string `json:"cmk_hex"`
	RecoveryKeyHex string `json:"recovery_key_hex"`
	RecoveryCode   string `json:"recovery_code"`
	WrapKeyHex     string `json:"wrap_key_hex"`
	Vault          struct {
		VaultID    string `json:"vault_id"`
		WrapSalt   string `json:"wrap_salt"`
		WrapNonce  string `json:"wrap_nonce"`
		WrappedKey string `json:"wrapped_key"`
	} `json:"vault_envelope"`
	Items []struct {
		Plaintext  string `json:"plaintext"`
		PlainHex   string `json:"plaintext_utf8_hex"`
		ItemKeyHex string `json:"item_key_hex"`
		AAD        string `json:"aad_utf8"`
		Envelope   struct {
			ID             string `json:"id"`
			SourceDeviceID string `json:"source_device_id"`
			Nonce          string `json:"nonce"`
			Ciphertext     string `json:"ciphertext"`
		} `json:"envelope"`
	} `json:"items"`
}

func TestVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "crypto-v1-vectors.json"))
	if err != nil {
		raw, err = os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "crypto-v1-vectors.json"))
	}
	if err != nil {
		wd, _ := os.Getwd()
		raw, err = os.ReadFile(filepath.Join(wd, "..", "..", "..", "..", "contracts", "crypto-v1-vectors.json"))
	}
	if err != nil {
		t.Fatal(err)
	}
	var fix fixture
	if err := json.Unmarshal(raw, &fix); err != nil {
		t.Fatal(err)
	}
	cmk, _ := hex.DecodeString(fix.CMKHex)
	rk, _ := hex.DecodeString(fix.RecoveryKeyHex)
	if clipcrypto.RecoveryCode(rk) != fix.RecoveryCode {
		t.Fatalf("recovery code")
	}
	salt, _ := clipcrypto.Decode(fix.Vault.WrapSalt)
	nonce, _ := clipcrypto.Decode(fix.Vault.WrapNonce)
	wrapped, _ := clipcrypto.Decode(fix.Vault.WrappedKey)
	kek, err := clipcrypto.Derive(rk, salt, []byte(clipcrypto.WrapInfo))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(kek) != fix.WrapKeyHex {
		t.Fatalf("wrap key %s", hex.EncodeToString(kek))
	}
	got, err := clipcrypto.WrapCMK(rk, salt, nonce, cmk, fix.UserID, fix.Vault.VaultID)
	if err != nil {
		t.Fatal(err)
	}
	if clipcrypto.Encode(got) != fix.Vault.WrappedKey {
		t.Fatalf("wrapped key mismatch")
	}
	opened, err := clipcrypto.UnwrapCMK(rk, salt, nonce, wrapped, fix.UserID, fix.Vault.VaultID)
	if err != nil || hex.EncodeToString(opened) != fix.CMKHex {
		t.Fatalf("unwrap")
	}
	for _, item := range fix.Items {
		n, _ := clipcrypto.Decode(item.Envelope.Nonce)
		ct, _ := clipcrypto.Decode(item.Envelope.Ciphertext)
		key, err := clipcrypto.Derive(cmk, []byte(fix.Vault.VaultID), clipcrypto.ItemInfo(item.Envelope.ID, 1))
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(key) != item.ItemKeyHex {
			t.Fatalf("item key")
		}
		aad := clipcrypto.ItemAAD(fix.UserID, fix.Vault.VaultID, item.Envelope.ID, item.Envelope.SourceDeviceID, 1)
		if string(aad) != item.AAD {
			t.Fatalf("aad")
		}
		out, err := clipcrypto.EncryptItem(cmk, fix.Vault.VaultID, fix.UserID, item.Envelope.ID, item.Envelope.SourceDeviceID, 1, n, []byte(item.Plaintext))
		if err != nil {
			t.Fatal(err)
		}
		if clipcrypto.Encode(out) != item.Envelope.Ciphertext {
			t.Fatalf("ciphertext for %q", item.Plaintext)
		}
		plain, err := clipcrypto.DecryptItem(cmk, fix.Vault.VaultID, fix.UserID, item.Envelope.ID, item.Envelope.SourceDeviceID, 1, n, ct)
		if err != nil || string(plain) != item.Plaintext {
			t.Fatalf("decrypt")
		}
		if hex.EncodeToString(plain) != item.PlainHex {
			t.Fatalf("utf8")
		}
		bad := append([]byte{}, ct...)
		bad[len(bad)-1] ^= 1
		if _, err := clipcrypto.DecryptItem(cmk, fix.Vault.VaultID, fix.UserID, item.Envelope.ID, item.Envelope.SourceDeviceID, 1, n, bad); err == nil {
			t.Fatalf("tamper must fail")
		}
	}
}

func TestPasswordWrapVector(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "crypto-v1-vectors.json"))
	if err != nil {
		raw, err = os.ReadFile(filepath.Join("..", "..", "..", "..", "contracts", "crypto-v1-vectors.json"))
	}
	if err != nil {
		wd, _ := os.Getwd()
		raw, err = os.ReadFile(filepath.Join(wd, "..", "..", "..", "..", "contracts", "crypto-v1-vectors.json"))
	}
	if err != nil {
		t.Fatal(err)
	}
	var fix struct {
		UserID   string `json:"user_id"`
		CMKHex   string `json:"cmk_hex"`
		Password string `json:"password_wrap_password"`
		Vault    struct {
			VaultID string `json:"vault_id"`
		} `json:"vault_envelope"`
		PasswordWrap struct {
			KDF         string `json:"kdf"`
			Time        int    `json:"time"`
			Memory      int    `json:"memory"`
			Parallelism int    `json:"parallelism"`
			KDFSalt     string `json:"kdf_salt"`
			WrapSalt    string `json:"wrap_salt"`
			WrapNonce   string `json:"wrap_nonce"`
			WrapKeyHex  string `json:"wrap_key_hex"`
			WrappedKey  string `json:"wrapped_key"`
		} `json:"password_wrap"`
	}
	if err := json.Unmarshal(raw, &fix); err != nil {
		t.Fatal(err)
	}
	if fix.Password == "" || fix.PasswordWrap.WrappedKey == "" {
		t.Fatal("password wrap vector missing")
	}
	cmk, _ := hex.DecodeString(fix.CMKHex)
	kdfSalt, err := clipcrypto.Decode(fix.PasswordWrap.KDFSalt)
	if err != nil {
		t.Fatal(err)
	}
	wrapSalt, _ := clipcrypto.Decode(fix.PasswordWrap.WrapSalt)
	nonce, _ := clipcrypto.Decode(fix.PasswordWrap.WrapNonce)
	ikm := clipcrypto.PasswordIKM(fix.Password, kdfSalt, uint32(fix.PasswordWrap.Time), uint32(fix.PasswordWrap.Memory), uint8(fix.PasswordWrap.Parallelism))
	kek, err := clipcrypto.Derive(ikm, wrapSalt, []byte(clipcrypto.PasswordWrapInfo))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(kek) != fix.PasswordWrap.WrapKeyHex {
		t.Fatalf("password wrap key %s", hex.EncodeToString(kek))
	}
	got, err := clipcrypto.WrapCMKWithPassword(fix.Password, kdfSalt, wrapSalt, nonce, cmk, fix.UserID, fix.Vault.VaultID)
	if err != nil {
		t.Fatal(err)
	}
	if clipcrypto.Encode(got) != fix.PasswordWrap.WrappedKey {
		t.Fatalf("password wrapped key mismatch got %s", clipcrypto.Encode(got))
	}
	opened, err := clipcrypto.UnwrapCMKWithPassword(fix.Password, kdfSalt, wrapSalt, nonce, got, fix.UserID, fix.Vault.VaultID, uint32(fix.PasswordWrap.Time), uint32(fix.PasswordWrap.Memory), uint8(fix.PasswordWrap.Parallelism))
	if err != nil || hex.EncodeToString(opened) != fix.CMKHex {
		t.Fatalf("password unwrap")
	}
	if _, err := clipcrypto.UnwrapCMKWithPassword("wrong-password-12", kdfSalt, wrapSalt, nonce, got, fix.UserID, fix.Vault.VaultID, uint32(fix.PasswordWrap.Time), uint32(fix.PasswordWrap.Memory), uint8(fix.PasswordWrap.Parallelism)); err == nil {
		t.Fatalf("wrong password must fail")
	}
}
