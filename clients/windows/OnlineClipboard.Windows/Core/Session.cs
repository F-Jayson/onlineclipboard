using System.Text.Json;

namespace OnlineClipboard.Windows.Core;

public sealed class AppSession
{
    public string Origin { get; set; } = "";
    public string UserId { get; set; } = "";
    public string DeviceId { get; set; } = "";
    public string DeviceName { get; set; } = "";
    public string VaultId { get; set; } = "";
    public string AccessToken { get; set; } = "";
    public string RefreshToken { get; set; } = "";
    public byte[]? Cmk { get; set; }
    public string SyncEpoch { get; set; } = "";
    public string Cursor { get; set; } = "0";
    public bool Live { get; set; }
    public bool Capture { get; set; }
    public bool AutoWrite { get; set; } = true;
    public DateTimeOffset? LastServerTime { get; set; }

    public static string NewId() => Guid.NewGuid().ToString();

    public ClipEnvelope Encrypt(string text, string intent)
    {
        if (Cmk == null) throw new InvalidOperationException("locked");
        var id = NewId();
        var nonce = CryptoV1.RandomBytes(12);
        var plain = System.Text.Encoding.UTF8.GetBytes(text);
        if (plain.Length == 0 || plain.Length > 65536) throw new InvalidOperationException("text size");
        var ct = CryptoV1.EncryptItem(Cmk, VaultId, UserId, id, DeviceId, 1, nonce, plain);
        return new ClipEnvelope
        {
            Id = id, VaultId = VaultId, SourceDeviceId = DeviceId,
            Nonce = CryptoV1.Encode(nonce), Ciphertext = CryptoV1.Encode(ct),
            DeliveryIntent = intent
        };
    }

    public string Decrypt(ClipDto clip)
    {
        if (Cmk == null) throw new InvalidOperationException("locked");
        var nonce = CryptoV1.Decode(clip.Nonce);
        var ct = CryptoV1.Decode(clip.Ciphertext);
        var plain = CryptoV1.DecryptItem(Cmk, clip.VaultId, UserId, clip.Id, clip.SourceDeviceId, clip.KeyEpoch, nonce, ct);
        return System.Text.Encoding.UTF8.GetString(plain);
    }

    public void PersistSecrets(LocalStore store)
    {
        store.Set("origin", Origin);
        store.Set("user_id", UserId);
        store.Set("device_id", DeviceId);
        store.Set("device_name", DeviceName);
        store.Set("vault_id", VaultId);
        if (AccessToken.Length > 0)
            store.Set("access_token", CryptoV1.ProtectLocal(System.Text.Encoding.UTF8.GetBytes(AccessToken)));
        if (RefreshToken.Length > 0)
            store.Set("refresh_token", CryptoV1.ProtectLocal(System.Text.Encoding.UTF8.GetBytes(RefreshToken)));
        if (Cmk != null)
            store.Set("cmk", CryptoV1.ProtectLocal(Cmk));
        else
            store.Delete("cmk");
        store.Set("sync_epoch", SyncEpoch);
        store.Set("cursor", Cursor);
        store.Set("capture", Capture ? "1" : "0");
        store.Set("auto_write", AutoWrite ? "1" : "0");
    }

    public static AppSession? Restore(LocalStore store)
    {
        var origin = store.GetString("origin");
        if (string.IsNullOrEmpty(origin)) return null;
        var s = new AppSession { Origin = origin };
        s.UserId = store.GetString("user_id") ?? "";
        s.DeviceId = store.GetString("device_id") ?? NewId();
        s.DeviceName = store.GetString("device_name") ?? Environment.MachineName;
        s.VaultId = store.GetString("vault_id") ?? "";
        s.SyncEpoch = store.GetString("sync_epoch") ?? "";
        s.Cursor = store.GetString("cursor") ?? "0";
        s.Capture = store.GetString("capture") == "1";
        s.AutoWrite = store.GetString("auto_write") != "0";
        try
        {
            var at = store.GetBytes("access_token");
            if (at != null) s.AccessToken = System.Text.Encoding.UTF8.GetString(CryptoV1.UnprotectLocal(at));
            var rt = store.GetBytes("refresh_token");
            if (rt != null) s.RefreshToken = System.Text.Encoding.UTF8.GetString(CryptoV1.UnprotectLocal(rt));
            var cmk = store.GetBytes("cmk");
            if (cmk != null) s.Cmk = CryptoV1.UnprotectLocal(cmk);
        }
        catch
        {
            s.AccessToken = "";
            s.RefreshToken = "";
            s.Cmk = null;
        }
        return s;
    }

    public static string EnvelopeJson(ClipEnvelope env) => JsonSerializer.Serialize(env);
    public static ClipEnvelope EnvelopeFromJson(string json) => JsonSerializer.Deserialize<ClipEnvelope>(json)!;
}
