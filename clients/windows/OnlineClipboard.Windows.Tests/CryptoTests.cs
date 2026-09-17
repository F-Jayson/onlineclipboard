using System.Text.Json;
using OnlineClipboard.Windows.Core;
using Xunit;

namespace OnlineClipboard.Windows.Tests;

public class CryptoTests
{
    [Fact]
    public void MatchesPublishedVectors()
    {
        var json = File.ReadAllText("crypto-v1-vectors.json");
        using var doc = JsonDocument.Parse(json);
        var root = doc.RootElement;
        var userId = root.GetProperty("user_id").GetString()!;
        var cmk = Convert.FromHexString(root.GetProperty("cmk_hex").GetString()!);
        var rk = Convert.FromHexString(root.GetProperty("recovery_key_hex").GetString()!);
        Assert.Equal(root.GetProperty("recovery_code").GetString(), CryptoV1.RecoveryCode(rk));
        var vault = root.GetProperty("vault_envelope");
        var vaultId = vault.GetProperty("vault_id").GetString()!;
        var salt = CryptoV1.Decode(vault.GetProperty("wrap_salt").GetString()!);
        var nonce = CryptoV1.Decode(vault.GetProperty("wrap_nonce").GetString()!);
        var wrapped = CryptoV1.WrapCmk(rk, salt, nonce, cmk, userId, vaultId);
        Assert.Equal(vault.GetProperty("wrapped_key").GetString(), CryptoV1.Encode(wrapped));
        Assert.Equal(cmk, CryptoV1.UnwrapCmk(rk, salt, nonce, CryptoV1.Decode(vault.GetProperty("wrapped_key").GetString()!), userId, vaultId));
        foreach (var item in root.GetProperty("items").EnumerateArray())
        {
            var env = item.GetProperty("envelope");
            var id = env.GetProperty("id").GetString()!;
            var src = env.GetProperty("source_device_id").GetString()!;
            var n = CryptoV1.Decode(env.GetProperty("nonce").GetString()!);
            var plain = System.Text.Encoding.UTF8.GetBytes(item.GetProperty("plaintext").GetString()!);
            var ct = CryptoV1.EncryptItem(cmk, vaultId, userId, id, src, 1, n, plain);
            Assert.Equal(env.GetProperty("ciphertext").GetString(), CryptoV1.Encode(ct));
            var opened = CryptoV1.DecryptItem(cmk, vaultId, userId, id, src, 1, n, CryptoV1.Decode(env.GetProperty("ciphertext").GetString()!));
            Assert.Equal(plain, opened);
            var bad = ct.ToArray();
            bad[^1] ^= 1;
            Assert.ThrowsAny<Exception>(() => CryptoV1.DecryptItem(cmk, vaultId, userId, id, src, 1, n, bad));
        }
    }

    [Fact]
    public void MatchesPasswordWrapVector()
    {
        var json = File.ReadAllText("crypto-v1-vectors.json");
        using var doc = JsonDocument.Parse(json);
        var root = doc.RootElement;
        var userId = root.GetProperty("user_id").GetString()!;
        var cmk = Convert.FromHexString(root.GetProperty("cmk_hex").GetString()!);
        var vaultId = root.GetProperty("vault_envelope").GetProperty("vault_id").GetString()!;
        var password = root.GetProperty("password_wrap_password").GetString()!;
        var wrap = root.GetProperty("password_wrap");
        var kdfSalt = CryptoV1.Decode(wrap.GetProperty("kdf_salt").GetString()!);
        var wrapSalt = CryptoV1.Decode(wrap.GetProperty("wrap_salt").GetString()!);
        var nonce = CryptoV1.Decode(wrap.GetProperty("wrap_nonce").GetString()!);
        var wrapped = CryptoV1.WrapCmkWithPassword(password, kdfSalt, wrapSalt, nonce, cmk, userId, vaultId);
        Assert.Equal(wrap.GetProperty("wrapped_key").GetString(), CryptoV1.Encode(wrapped));
        var opened = CryptoV1.UnwrapCmkWithPassword(
            password, kdfSalt, wrapSalt, nonce,
            CryptoV1.Decode(wrap.GetProperty("wrapped_key").GetString()!),
            userId, vaultId,
            wrap.GetProperty("time").GetInt32(),
            wrap.GetProperty("memory").GetInt32(),
            wrap.GetProperty("parallelism").GetInt32());
        Assert.Equal(cmk, opened);
        Assert.ThrowsAny<Exception>(() => CryptoV1.UnwrapCmkWithPassword(
            "wrong-password-12", kdfSalt, wrapSalt, nonce, wrapped, userId, vaultId, 3, 65536, 1));
    }

    [Fact]
    public void NormalizesOrigins()
    {
        Assert.Equal("https://clip.example.com", Origin.Normalize("clip.example.com"));
        Assert.Equal("http://127.0.0.1:8080", Origin.Normalize("http://127.0.0.1:8080"));
        Assert.Throws<FormatException>(() => Origin.Normalize("http://example.com"));
        Assert.Throws<FormatException>(() => Origin.Normalize("https://clip.example.com/api"));
    }
}
