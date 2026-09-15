using System.Security.Cryptography;
using System.Text;

namespace OnlineClipboard.Windows.Core;

public static class CryptoV1
{
    public const int FormatVersion = 1;
    public const int KeyEpoch = 1;
    private static readonly byte[] WrapInfo = Encoding.UTF8.GetBytes("onlineclipboard/v1/wrap");

    public static byte[] RandomBytes(int n)
    {
        var buf = new byte[n];
        RandomNumberGenerator.Fill(buf);
        return buf;
    }

    public static string Encode(byte[] raw) => Convert.ToBase64String(raw).TrimEnd('=').Replace('+', '-').Replace('/', '_');

    public static byte[] Decode(string value)
    {
        var padded = value.Replace('-', '+').Replace('_', '/');
        switch (padded.Length % 4)
        {
            case 2: padded += "=="; break;
            case 3: padded += "="; break;
            case 1: throw new FormatException("non-canonical base64url");
        }
        var raw = Convert.FromBase64String(padded);
        if (Encode(raw) != value) throw new FormatException("non-canonical base64url");
        return raw;
    }

    public static string RecoveryCode(byte[] rk) => "oc1_" + Encode(rk);

    public static byte[] ParseRecoveryCode(string code)
    {
        code = code.Replace(" ", "", StringComparison.Ordinal);
        if (!code.StartsWith("oc1_", StringComparison.Ordinal)) throw new FormatException("recovery code must start with oc1_");
        var raw = Decode(code[4..]);
        if (raw.Length != 32) throw new FormatException("recovery key must be 32 bytes");
        return raw;
    }

    public static byte[] Derive(byte[] ikm, byte[] salt, byte[] info) =>
        HKDF.DeriveKey(HashAlgorithmName.SHA256, ikm, 32, salt, info);

    public static byte[] AesGcmSeal(byte[] key, byte[] nonce, byte[] plaintext, byte[] aad)
    {
        var cipher = new byte[plaintext.Length];
        var tag = new byte[16];
        using var gcm = new AesGcm(key, 16);
        gcm.Encrypt(nonce, plaintext, cipher, tag, aad);
        var combined = new byte[cipher.Length + tag.Length];
        Buffer.BlockCopy(cipher, 0, combined, 0, cipher.Length);
        Buffer.BlockCopy(tag, 0, combined, cipher.Length, tag.Length);
        return combined;
    }

    public static byte[] AesGcmOpen(byte[] key, byte[] nonce, byte[] ciphertextAndTag, byte[] aad)
    {
        if (ciphertextAndTag.Length < 17) throw new CryptographicException("ciphertext too short");
        var cipherLen = ciphertextAndTag.Length - 16;
        var cipher = ciphertextAndTag[..cipherLen];
        var tag = ciphertextAndTag[cipherLen..];
        var plain = new byte[cipherLen];
        using var gcm = new AesGcm(key, 16);
        gcm.Decrypt(nonce, cipher, tag, plain, aad);
        return plain;
    }

    public static byte[] WrapAad(string userId, string vaultId) =>
        Encoding.UTF8.GetBytes($"oc-v1|vault|{userId}|{vaultId}|1");

    public static byte[] ItemInfo(string clipId, int epoch) =>
        Encoding.UTF8.GetBytes($"onlineclipboard/v1/item|{clipId}|{epoch}");

    public static byte[] ItemAad(string userId, string vaultId, string clipId, string sourceDeviceId, int epoch) =>
        Encoding.UTF8.GetBytes($"oc-v1|clip|{userId}|{vaultId}|{clipId}|{sourceDeviceId}|{epoch}|text/plain");

    public static byte[] WrapCmk(byte[] rk, byte[] wrapSalt, byte[] wrapNonce, byte[] cmk, string userId, string vaultId)
    {
        var kek = Derive(rk, wrapSalt, WrapInfo);
        return AesGcmSeal(kek, wrapNonce, cmk, WrapAad(userId, vaultId));
    }

    public static byte[] UnwrapCmk(byte[] rk, byte[] wrapSalt, byte[] wrapNonce, byte[] wrapped, string userId, string vaultId)
    {
        var kek = Derive(rk, wrapSalt, WrapInfo);
        return AesGcmOpen(kek, wrapNonce, wrapped, WrapAad(userId, vaultId));
    }

    public static byte[] EncryptItem(byte[] cmk, string vaultId, string userId, string clipId, string sourceDeviceId, int epoch, byte[] nonce, byte[] plaintext)
    {
        var key = Derive(cmk, Encoding.UTF8.GetBytes(vaultId), ItemInfo(clipId, epoch));
        return AesGcmSeal(key, nonce, plaintext, ItemAad(userId, vaultId, clipId, sourceDeviceId, epoch));
    }

    public static byte[] DecryptItem(byte[] cmk, string vaultId, string userId, string clipId, string sourceDeviceId, int epoch, byte[] nonce, byte[] ciphertext)
    {
        var key = Derive(cmk, Encoding.UTF8.GetBytes(vaultId), ItemInfo(clipId, epoch));
        return AesGcmOpen(key, nonce, ciphertext, ItemAad(userId, vaultId, clipId, sourceDeviceId, epoch));
    }

    public static byte[] ProtectLocal(byte[] data) => ProtectedData.Protect(data, null, DataProtectionScope.CurrentUser);
    public static byte[] UnprotectLocal(byte[] data) => ProtectedData.Unprotect(data, null, DataProtectionScope.CurrentUser);
}
