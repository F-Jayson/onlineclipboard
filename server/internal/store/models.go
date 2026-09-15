package store

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type Scope struct {
	UserID    string
	DeviceID  string
	SessionID string
	FamilyID  string
}

type TokenPair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type"`
	ExpiresIn        int       `json:"expires_in"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

type AuthResult struct {
	TokenPair
	UserID           string `json:"user_id"`
	DeviceID         string `json:"device_id"`
	VaultInitialized bool   `json:"vault_initialized"`
}

type Device struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type VaultEnvelope struct {
	VaultID       string `json:"vault_id"`
	FormatVersion int    `json:"format_version"`
	KeyEpoch      int    `json:"key_epoch"`
	WrapSalt      string `json:"wrap_salt"`
	WrapNonce     string `json:"wrap_nonce"`
	WrappedKey    string `json:"wrapped_key"`
}

type Envelope struct {
	ID             string `json:"id"`
	VaultID        string `json:"vault_id"`
	SourceDeviceID string `json:"source_device_id"`
	FormatVersion  int    `json:"format_version"`
	KeyEpoch       int    `json:"key_epoch"`
	ContentType    string `json:"content_type"`
	Nonce          string `json:"nonce"`
	Ciphertext     string `json:"ciphertext"`
	DeliveryIntent string `json:"delivery_intent"`
}

type Clip struct {
	Envelope
	Status     string     `json:"status"`
	Version    int        `json:"version"`
	CreatedSeq string     `json:"created_seq"`
	LastSeq    string     `json:"last_seq"`
	CreatedAt  time.Time  `json:"created_at"`
	DeletedAt  *time.Time `json:"deleted_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

type MutationReceipt struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Version   int        `json:"version"`
	Seq       string     `json:"seq"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type Change struct {
	Seq        string    `json:"seq"`
	ClipID     string    `json:"clip_id"`
	Kind       string    `json:"kind"`
	Version    int       `json:"version"`
	OccurredAt time.Time `json:"occurred_at"`
}

type ChangePage struct {
	SyncEpoch     string    `json:"sync_epoch"`
	Events        []Change  `json:"events"`
	NextSeq       string    `json:"next_seq"`
	HighWatermark string    `json:"high_watermark"`
	HasMore       bool      `json:"has_more"`
	ServerTime    time.Time `json:"server_time"`
}

type Snapshot struct {
	Token     string    `json:"token"`
	BaseSeq   string    `json:"base_seq"`
	SyncEpoch string    `json:"sync_epoch"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ClipPage struct {
	Items         []Clip    `json:"items"`
	NextPageToken *string   `json:"next_page_token"`
	ServerTime    time.Time `json:"server_time"`
}

type SnapshotPage struct {
	BaseSeq       string    `json:"base_seq"`
	SyncEpoch     string    `json:"sync_epoch"`
	Items         []Clip    `json:"items"`
	NextPageToken *string   `json:"next_page_token"`
	ServerTime    time.Time `json:"server_time"`
}

type ServerState struct {
	ServerID  string
	SyncEpoch string
}

func ptrTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func utc(t time.Time) time.Time {
	return t.UTC()
}

func marshalReceipt(r MutationReceipt) []byte {
	b, _ := json.Marshal(r)
	return b
}
