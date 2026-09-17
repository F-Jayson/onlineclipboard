package canon

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	uuidRE     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	usernameRE = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)
	emailRE    = regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}$`)
	seqRE      = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	b64urlRE   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

const TokenBytes = 32

func RandomToken() (plain string, hash []byte, err error) {
	raw := make([]byte, TokenBytes)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, err
	}
	plain = Encode(raw)
	sum := sha256.Sum256([]byte(plain))
	return plain, sum[:], nil
}

func HashToken(plain string) []byte {
	sum := sha256.Sum256([]byte(plain))
	return sum[:]
}

func Encode(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func Decode(value string, expectedLen int) ([]byte, error) {
	if !b64urlRE.MatchString(value) {
		return nil, fmt.Errorf("non-canonical base64url")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid base64url")
	}
	if Encode(raw) != value {
		return nil, fmt.Errorf("non-canonical base64url")
	}
	if expectedLen >= 0 && len(raw) != expectedLen {
		return nil, fmt.Errorf("unexpected binary length")
	}
	return raw, nil
}

func UUID(value string) error {
	if !uuidRE.MatchString(value) {
		return fmt.Errorf("uuid must be lowercase canonical form")
	}
	return nil
}

func Username(value string) error {
	if !usernameRE.MatchString(value) {
		return fmt.Errorf("username must be 3-32 ascii lowercase letters, digits or underscore")
	}
	return nil
}

func Email(value string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	if !utf8.ValidString(v) || len(v) < 5 || len(v) > 190 || !emailRE.MatchString(v) {
		return "", fmt.Errorf("email is invalid")
	}
	return v, nil
}

func LoginIdentifier(value string) error {
	v := strings.TrimSpace(value)
	if !utf8.ValidString(v) || v == "" || utf8.RuneCountInString(v) > 190 || len(v) > 190 {
		return fmt.Errorf("login identifier is invalid")
	}
	return nil
}

func Password(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("password is not valid utf-8")
	}
	n := utf8.RuneCountInString(value)
	if n < 12 || n > 128 || len(value) > 512 {
		return fmt.Errorf("password must be 12-128 characters and at most 512 bytes")
	}
	return nil
}

func LoginSecret(value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("password is not valid utf-8")
	}
	n := utf8.RuneCountInString(value)
	if n < 1 || n > 128 || len(value) > 512 {
		return fmt.Errorf("password is invalid")
	}
	return nil
}

func EmailCode(value string) error {
	if len(value) != 6 {
		return fmt.Errorf("email code must be 6 digits")
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return fmt.Errorf("email code must be 6 digits")
		}
	}
	return nil
}

func Seq(value string) (int64, error) {
	if !seqRE.MatchString(value) {
		return 0, fmt.Errorf("invalid seq")
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid seq")
	}
	return n, nil
}

func FormatSeq(n int64) string {
	return strconv.FormatInt(n, 10)
}

func ParseIfMatch(value string) (int, error) {
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, fmt.Errorf("If-Match must be a quoted positive integer")
	}
	inner := value[1 : len(value)-1]
	if strings.HasPrefix(inner, "0") || inner == "" {
		return 0, fmt.Errorf("If-Match must be a quoted positive integer")
	}
	n, err := strconv.Atoi(inner)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("If-Match must be a quoted positive integer")
	}
	return n, nil
}

func DeviceName(value string) error {
	n := utf8.RuneCountInString(value)
	if n < 1 || n > 64 || !utf8.ValidString(value) {
		return fmt.Errorf("device name must be 1-64 characters")
	}
	return nil
}

func Platform(value string) error {
	if value != "windows" && value != "android" {
		return fmt.Errorf("platform must be windows or android")
	}
	return nil
}

func Token(value string) error {
	if len(value) != 43 {
		return fmt.Errorf("token must be 43-character base64url")
	}
	_, err := Decode(value, TokenBytes)
	return err
}

func AppendLenPrefixed(dst []byte, field []byte) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(field)))
	dst = append(dst, n[:]...)
	return append(dst, field...)
}

func HashFields(fields ...[]byte) []byte {
	var buf []byte
	for _, f := range fields {
		buf = AppendLenPrefixed(buf, f)
	}
	sum := sha256.Sum256(buf)
	return sum[:]
}

func CreateRequestHash(id, vaultID, sourceDeviceID string, formatVersion, keyEpoch int, contentType string, nonce, ciphertext []byte, deliveryIntent string) []byte {
	return HashFields(
		[]byte(id),
		[]byte(vaultID),
		[]byte(sourceDeviceID),
		[]byte(strconv.Itoa(formatVersion)),
		[]byte(strconv.Itoa(keyEpoch)),
		[]byte(contentType),
		nonce,
		ciphertext,
		[]byte(deliveryIntent),
	)
}

func OperationRequestHash(kind, clipID string, expectedVersion int) []byte {
	return HashFields(
		[]byte(kind),
		[]byte(clipID),
		[]byte(strconv.Itoa(expectedVersion)),
	)
}

func EmailCodeHash(email, purpose, code string) []byte {
	sum := sha256.Sum256([]byte(purpose + "|" + email + "|" + code))
	return sum[:]
}
