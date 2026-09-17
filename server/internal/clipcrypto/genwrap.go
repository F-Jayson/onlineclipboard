//go:build ignore

package main

import (
	"encoding/hex"
	"fmt"

	"onlineclipboard/server/internal/clipcrypto"
)

func main() {
	cmk, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	kdfSalt := []byte{0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x5b, 0x5c, 0x5d, 0x5e, 0x5f}
	wrapSalt := []byte{
		0x60, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69, 0x6a, 0x6b, 0x6c, 0x6d, 0x6e, 0x6f,
		0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x7b, 0x7c, 0x7d, 0x7e, 0x7f,
	}
	nonce := []byte{0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a, 0x2b}
	userID := "11111111-1111-4111-8111-111111111111"
	vaultID := "22222222-2222-4222-8222-222222222222"
	password := "correct-horse-battery"
	wrapped, err := clipcrypto.WrapCMKWithPassword(password, kdfSalt, wrapSalt, nonce, cmk, userID, vaultID)
	if err != nil {
		panic(err)
	}
	ikm := clipcrypto.PasswordIKM(password, kdfSalt, clipcrypto.PasswordKDFTime, clipcrypto.PasswordKDFMemoryKiB, clipcrypto.PasswordKDFParallel)
	kek, err := clipcrypto.Derive(ikm, wrapSalt, []byte(clipcrypto.PasswordWrapInfo))
	if err != nil {
		panic(err)
	}
	fmt.Println("kdf_salt", clipcrypto.Encode(kdfSalt))
	fmt.Println("wrap_salt", clipcrypto.Encode(wrapSalt))
	fmt.Println("wrap_nonce", clipcrypto.Encode(nonce))
	fmt.Println("wrap_key_hex", hex.EncodeToString(kek))
	fmt.Println("wrapped_key", clipcrypto.Encode(wrapped))
}
