using System.IO;
using Microsoft.Data.Sqlite;

namespace OnlineClipboard.Windows.Core;

public sealed class LocalStore : IDisposable
{
    private readonly SqliteConnection _db;

    public LocalStore(string path)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        _db = new SqliteConnection(new SqliteConnectionStringBuilder { DataSource = path }.ToString());
        _db.Open();
        using var cmd = _db.CreateCommand();
        cmd.CommandText = """
            CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v BLOB);
            CREATE TABLE IF NOT EXISTS clips(
                id TEXT PRIMARY KEY, status TEXT, version INTEGER, created_seq TEXT,
                created_at TEXT, expires_at TEXT, source_device_id TEXT,
                vault_id TEXT, nonce TEXT, ciphertext TEXT, plaintext TEXT);
            CREATE TABLE IF NOT EXISTS outbox(
                id TEXT PRIMARY KEY, envelope_json TEXT, delivery_intent TEXT, created_at TEXT);
            CREATE TABLE IF NOT EXISTS ops(
                id TEXT PRIMARY KEY, clip_id TEXT, kind TEXT, version INTEGER);
            """;
        cmd.ExecuteNonQuery();
    }

    public string? GetString(string key)
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "SELECT v FROM meta WHERE k=$k";
        cmd.Parameters.AddWithValue("$k", key);
        return cmd.ExecuteScalar() as string;
    }

    public byte[]? GetBytes(string key)
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "SELECT v FROM meta WHERE k=$k";
        cmd.Parameters.AddWithValue("$k", key);
        return cmd.ExecuteScalar() as byte[];
    }

    public void Set(string key, object value)
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "INSERT INTO meta(k,v) VALUES($k,$v) ON CONFLICT(k) DO UPDATE SET v=excluded.v";
        cmd.Parameters.AddWithValue("$k", key);
        cmd.Parameters.AddWithValue("$v", value);
        cmd.ExecuteNonQuery();
    }

    public void UpsertClip(HistoryItem item, string vaultId, string nonce, string ciphertext)
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = """
            INSERT INTO clips(id,status,version,created_seq,created_at,expires_at,source_device_id,vault_id,nonce,ciphertext,plaintext)
            VALUES($id,$st,$ver,$seq,$ca,$ex,$src,$vault,$n,$c,$p)
            ON CONFLICT(id) DO UPDATE SET status=excluded.status, version=excluded.version,
                created_seq=excluded.created_seq, expires_at=excluded.expires_at, plaintext=excluded.plaintext,
                nonce=excluded.nonce, ciphertext=excluded.ciphertext
            """;
        cmd.Parameters.AddWithValue("$id", item.Id);
        cmd.Parameters.AddWithValue("$st", item.Status);
        cmd.Parameters.AddWithValue("$ver", item.Version);
        cmd.Parameters.AddWithValue("$seq", item.CreatedSeq);
        cmd.Parameters.AddWithValue("$ca", item.CreatedAt.ToString("O"));
        cmd.Parameters.AddWithValue("$ex", item.ExpiresAt?.ToString("O") ?? (object)DBNull.Value);
        cmd.Parameters.AddWithValue("$src", item.SourceDeviceId);
        cmd.Parameters.AddWithValue("$vault", vaultId);
        cmd.Parameters.AddWithValue("$n", nonce);
        cmd.Parameters.AddWithValue("$c", ciphertext);
        cmd.Parameters.AddWithValue("$p", CryptoV1.ProtectLocal(System.Text.Encoding.UTF8.GetBytes(item.Text)));
        cmd.ExecuteNonQuery();
    }

    public void DeleteClip(string id)
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "DELETE FROM clips WHERE id=$id";
        cmd.Parameters.AddWithValue("$id", id);
        cmd.ExecuteNonQuery();
    }

    public List<HistoryItem> LoadClips(string status)
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "SELECT id,status,version,created_seq,created_at,expires_at,source_device_id,plaintext FROM clips WHERE status=$s ORDER BY created_seq DESC";
        cmd.Parameters.AddWithValue("$s", status);
        using var r = cmd.ExecuteReader();
        var list = new List<HistoryItem>();
        while (r.Read())
        {
            var text = ReadPlain(r, 7);
            list.Add(new HistoryItem
            {
                Id = r.GetString(0),
                Status = r.GetString(1),
                Version = r.GetInt32(2),
                CreatedSeq = r.GetString(3),
                CreatedAt = DateTimeOffset.Parse(r.GetString(4)),
                ExpiresAt = r.IsDBNull(5) ? null : DateTimeOffset.Parse(r.GetString(5)),
                SourceDeviceId = r.GetString(6),
                Text = text,
                Preview = PreviewOf(text)
            });
        }
        return list;
    }

    public HistoryItem? GetClip(string id)
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "SELECT id,status,version,created_seq,created_at,expires_at,source_device_id,plaintext FROM clips WHERE id=$id";
        cmd.Parameters.AddWithValue("$id", id);
        using var r = cmd.ExecuteReader();
        if (!r.Read()) return null;
        var text = ReadPlain(r, 7);
        return new HistoryItem
        {
            Id = r.GetString(0), Status = r.GetString(1), Version = r.GetInt32(2), CreatedSeq = r.GetString(3),
            CreatedAt = DateTimeOffset.Parse(r.GetString(4)),
            ExpiresAt = r.IsDBNull(5) ? null : DateTimeOffset.Parse(r.GetString(5)),
            SourceDeviceId = r.GetString(6), Text = text, Preview = PreviewOf(text)
        };
    }

    public void AddOutbox(string id, string json)
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "INSERT OR REPLACE INTO outbox(id,envelope_json,created_at) VALUES($id,$j,$t)";
        cmd.Parameters.AddWithValue("$id", id);
        cmd.Parameters.AddWithValue("$j", json);
        cmd.Parameters.AddWithValue("$t", DateTimeOffset.UtcNow.ToString("O"));
        cmd.ExecuteNonQuery();
    }

    public List<(string Id, string Json)> Outbox()
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "SELECT id, envelope_json FROM outbox ORDER BY created_at";
        using var r = cmd.ExecuteReader();
        var list = new List<(string, string)>();
        while (r.Read()) list.Add((r.GetString(0), r.GetString(1)));
        return list;
    }

    public void RemoveOutbox(string id)
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "DELETE FROM outbox WHERE id=$id";
        cmd.Parameters.AddWithValue("$id", id);
        cmd.ExecuteNonQuery();
    }

    public int OutboxCount()
    {
        using var cmd = _db.CreateCommand();
        cmd.CommandText = "SELECT COUNT(*) FROM outbox";
        return Convert.ToInt32(cmd.ExecuteScalar());
    }

    private static string ReadPlain(SqliteDataReader r, int i)
    {
        if (r.IsDBNull(i)) return "";
        var value = r.GetValue(i);
        if (value is byte[] blob)
        {
            try { return System.Text.Encoding.UTF8.GetString(CryptoV1.UnprotectLocal(blob)); }
            catch { return ""; }
        }
        return value.ToString() ?? "";
    }

    public static string PreviewOf(string text)
    {
        var one = text.Replace("\r", " ").Replace("\n", " ");
        return one.Length <= 80 ? one : one[..80] + "…";
    }

    public void Dispose() => _db.Dispose();
}
