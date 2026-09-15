// Package domain defines extension points, not implemented business behavior.
package domain

import (
	"context"
	"time"
)

type Scope struct {
	UserID   string
	DeviceID string
}

// Envelope must never contain plaintext. Validate its shape before persistence.
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

type MutationReceipt struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Version   int        `json:"version"`
	Seq       string     `json:"seq"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// ClipService implementations must enforce ownership, idempotency and the
// transaction ordering described in docs/04-sync-protocol.md.
type ClipService interface {
	Create(context.Context, Scope, Envelope) (MutationReceipt, error)
	Trash(ctx context.Context, scope Scope, clipID, operationID string, expectedVersion int) (MutationReceipt, error)
	Restore(ctx context.Context, scope Scope, clipID, operationID string, expectedVersion int) (MutationReceipt, error)
	Purge(ctx context.Context, scope Scope, clipID, operationID string, expectedVersion int) (MutationReceipt, error)
}
