using System.Text.Json.Serialization;

namespace OnlineClipboard.Windows.Core;

public sealed class ServerInfo
{
    [JsonPropertyName("name")] public string Name { get; set; } = "";
    [JsonPropertyName("version")] public string Version { get; set; } = "";
    [JsonPropertyName("stage")] public string Stage { get; set; } = "";
    [JsonPropertyName("protocol_version")] public int ProtocolVersion { get; set; }
    [JsonPropertyName("sync_available")] public bool SyncAvailable { get; set; }
    [JsonPropertyName("e2ee_available")] public bool E2eeAvailable { get; set; }
    [JsonPropertyName("registration_mode")] public string RegistrationMode { get; set; } = "";
    [JsonPropertyName("max_text_bytes")] public int MaxTextBytes { get; set; }
    [JsonPropertyName("trash_retention_seconds")] public int TrashRetentionSeconds { get; set; }
}

public sealed class AuthResult
{
    [JsonPropertyName("access_token")] public string AccessToken { get; set; } = "";
    [JsonPropertyName("refresh_token")] public string RefreshToken { get; set; } = "";
    [JsonPropertyName("token_type")] public string TokenType { get; set; } = "";
    [JsonPropertyName("expires_in")] public int ExpiresIn { get; set; }
    [JsonPropertyName("refresh_expires_at")] public DateTimeOffset RefreshExpiresAt { get; set; }
    [JsonPropertyName("user_id")] public string UserId { get; set; } = "";
    [JsonPropertyName("device_id")] public string DeviceId { get; set; } = "";
    [JsonPropertyName("vault_initialized")] public bool VaultInitialized { get; set; }
}

public sealed class VaultEnvelope
{
    [JsonPropertyName("vault_id")] public string VaultId { get; set; } = "";
    [JsonPropertyName("format_version")] public int FormatVersion { get; set; } = 1;
    [JsonPropertyName("key_epoch")] public int KeyEpoch { get; set; } = 1;
    [JsonPropertyName("wrap_salt")] public string WrapSalt { get; set; } = "";
    [JsonPropertyName("wrap_nonce")] public string WrapNonce { get; set; } = "";
    [JsonPropertyName("wrapped_key")] public string WrappedKey { get; set; } = "";
}

public class ClipEnvelope
{
    [JsonPropertyName("id")] public string Id { get; set; } = "";
    [JsonPropertyName("vault_id")] public string VaultId { get; set; } = "";
    [JsonPropertyName("source_device_id")] public string SourceDeviceId { get; set; } = "";
    [JsonPropertyName("format_version")] public int FormatVersion { get; set; } = 1;
    [JsonPropertyName("key_epoch")] public int KeyEpoch { get; set; } = 1;
    [JsonPropertyName("content_type")] public string ContentType { get; set; } = "text/plain";
    [JsonPropertyName("nonce")] public string Nonce { get; set; } = "";
    [JsonPropertyName("ciphertext")] public string Ciphertext { get; set; } = "";
    [JsonPropertyName("delivery_intent")] public string DeliveryIntent { get; set; } = "history_only";
}

public sealed class ClipDto : ClipEnvelope
{
    [JsonPropertyName("status")] public string Status { get; set; } = "";
    [JsonPropertyName("version")] public int Version { get; set; }
    [JsonPropertyName("created_seq")] public string CreatedSeq { get; set; } = "";
    [JsonPropertyName("last_seq")] public string LastSeq { get; set; } = "";
    [JsonPropertyName("created_at")] public DateTimeOffset CreatedAt { get; set; }
    [JsonPropertyName("deleted_at")] public DateTimeOffset? DeletedAt { get; set; }
    [JsonPropertyName("expires_at")] public DateTimeOffset? ExpiresAt { get; set; }
}

public sealed class MutationReceipt
{
    [JsonPropertyName("id")] public string Id { get; set; } = "";
    [JsonPropertyName("status")] public string Status { get; set; } = "";
    [JsonPropertyName("version")] public int Version { get; set; }
    [JsonPropertyName("seq")] public string Seq { get; set; } = "";
    [JsonPropertyName("created_at")] public DateTimeOffset? CreatedAt { get; set; }
    [JsonPropertyName("deleted_at")] public DateTimeOffset? DeletedAt { get; set; }
    [JsonPropertyName("expires_at")] public DateTimeOffset? ExpiresAt { get; set; }
}

public sealed class ChangePage
{
    [JsonPropertyName("sync_epoch")] public string SyncEpoch { get; set; } = "";
    [JsonPropertyName("events")] public List<ChangeEvent> Events { get; set; } = [];
    [JsonPropertyName("next_seq")] public string NextSeq { get; set; } = "0";
    [JsonPropertyName("high_watermark")] public string HighWatermark { get; set; } = "0";
    [JsonPropertyName("has_more")] public bool HasMore { get; set; }
    [JsonPropertyName("server_time")] public DateTimeOffset ServerTime { get; set; }
}

public sealed class ChangeEvent
{
    [JsonPropertyName("seq")] public string Seq { get; set; } = "";
    [JsonPropertyName("clip_id")] public string ClipId { get; set; } = "";
    [JsonPropertyName("kind")] public string Kind { get; set; } = "";
    [JsonPropertyName("version")] public int Version { get; set; }
    [JsonPropertyName("occurred_at")] public DateTimeOffset OccurredAt { get; set; }
}

public sealed class Snapshot
{
    [JsonPropertyName("token")] public string Token { get; set; } = "";
    [JsonPropertyName("base_seq")] public string BaseSeq { get; set; } = "";
    [JsonPropertyName("sync_epoch")] public string SyncEpoch { get; set; } = "";
    [JsonPropertyName("expires_at")] public DateTimeOffset ExpiresAt { get; set; }
}

public sealed class SnapshotPage
{
    [JsonPropertyName("base_seq")] public string BaseSeq { get; set; } = "";
    [JsonPropertyName("sync_epoch")] public string SyncEpoch { get; set; } = "";
    [JsonPropertyName("items")] public List<ClipDto> Items { get; set; } = [];
    [JsonPropertyName("next_page_token")] public string? NextPageToken { get; set; }
    [JsonPropertyName("server_time")] public DateTimeOffset ServerTime { get; set; }
}

public sealed class DeviceDto
{
    [JsonPropertyName("id")] public string Id { get; set; } = "";
    [JsonPropertyName("name")] public string Name { get; set; } = "";
    [JsonPropertyName("platform")] public string Platform { get; set; } = "";
    [JsonPropertyName("created_at")] public DateTimeOffset CreatedAt { get; set; }
    [JsonPropertyName("last_seen_at")] public DateTimeOffset? LastSeenAt { get; set; }
    [JsonPropertyName("revoked_at")] public DateTimeOffset? RevokedAt { get; set; }
    public string StatusLabel => RevokedAt == null ? "可用" : "已撤销";
    public string MetaLabel => LastSeenAt is { } seen
        ? $"{Platform} · 最近 {seen.ToLocalTime():MM-dd HH:mm}"
        : Platform;
}

public sealed class ApiError : Exception
{
    public int Status { get; }
    public string Code { get; }
    public ApiError(int status, string code, string message) : base(message)
    {
        Status = status;
        Code = code;
    }
}

public sealed class HistoryItem
{
    public string Id { get; set; } = "";
    public string Status { get; set; } = "active";
    public int Version { get; set; }
    public string CreatedSeq { get; set; } = "";
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset? ExpiresAt { get; set; }
    public string SourceDeviceId { get; set; } = "";
    public string Preview { get; set; } = "";
    public string Text { get; set; } = "";
    public string TimeLabel => CreatedAt.ToLocalTime().ToString("yyyy-MM-dd HH:mm");
    public string ExpiresLabel => ExpiresAt is { } exp ? $"将于 {exp.ToLocalTime():MM-dd HH:mm} 永久删除" : "回收站";
    public override string ToString() => $"{TimeLabel}  {Preview}";
}
