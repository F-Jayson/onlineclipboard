using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace OnlineClipboard.Windows.Core;

public sealed class ApiClient : IDisposable
{
    private static readonly JsonSerializerOptions JsonOpts = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull
    };

    private readonly HttpClient _http;
    public string Origin { get; }

    public ApiClient(string origin, HttpMessageHandler? handler = null)
    {
        Origin = origin.TrimEnd('/');
        _http = handler == null ? new HttpClient() : new HttpClient(handler, disposeHandler: false);
        _http.BaseAddress = new Uri(Origin + "/");
        _http.Timeout = TimeSpan.FromSeconds(30);
        _http.DefaultRequestHeaders.Accept.Add(new MediaTypeWithQualityHeaderValue("application/json"));
    }

    public void SetAccessToken(string? token)
    {
        _http.DefaultRequestHeaders.Authorization = string.IsNullOrEmpty(token)
            ? null
            : new AuthenticationHeaderValue("Bearer", token);
    }

    public Task<ServerInfo> ServerInfoAsync(CancellationToken ct) => GetAsync<ServerInfo>("api/v1/server-info", ct);

    public Task<AuthResult> RegisterAsync(object body, CancellationToken ct) =>
        SendAsync<AuthResult>(HttpMethod.Post, "api/v1/auth/register", body, ct, expected: 201);

    public Task<AuthResult> LoginAsync(object body, CancellationToken ct) =>
        SendAsync<AuthResult>(HttpMethod.Post, "api/v1/auth/login", body, ct);

    public Task<AuthResult> RefreshAsync(string refreshToken, CancellationToken ct) =>
        SendAsync<AuthResult>(HttpMethod.Post, "api/v1/auth/refresh", new { refresh_token = refreshToken }, ct);

    public Task LogoutAsync(CancellationToken ct) => SendAsync(HttpMethod.Post, "api/v1/auth/logout", null, ct, 204);

    public Task<VaultEnvelope> GetVaultAsync(CancellationToken ct) => GetAsync<VaultEnvelope>("api/v1/vault", ct);

    public Task<VaultEnvelope> PutVaultAsync(VaultEnvelope env, CancellationToken ct) =>
        SendAsync<VaultEnvelope>(HttpMethod.Put, "api/v1/vault", env, ct, 201, new Dictionary<string, string> { ["If-None-Match"] = "*" });

    public Task<MutationReceipt> CreateClipAsync(ClipEnvelope env, CancellationToken ct) =>
        SendAsync<MutationReceipt>(HttpMethod.Post, "api/v1/clips", env, ct, 0);

    public Task<ClipDto> GetClipAsync(string id, CancellationToken ct) => GetAsync<ClipDto>($"api/v1/clips/{id}", ct);

    public Task<MutationReceipt> TrashAsync(string id, int version, string op, CancellationToken ct) =>
        SendAsync<MutationReceipt>(HttpMethod.Delete, $"api/v1/clips/{id}", null, ct, 200, Headers(version, op));

    public Task<MutationReceipt> RestoreAsync(string id, int version, string op, CancellationToken ct) =>
        SendAsync<MutationReceipt>(HttpMethod.Post, $"api/v1/clips/{id}/restore", null, ct, 200, Headers(version, op));

    public Task<MutationReceipt> PurgeAsync(string id, int version, string op, CancellationToken ct) =>
        SendAsync<MutationReceipt>(HttpMethod.Delete, $"api/v1/clips/{id}/permanent", null, ct, 200, Headers(version, op));

    public Task<ChangePage> ChangesAsync(string epoch, string after, CancellationToken ct) =>
        GetAsync<ChangePage>($"api/v1/sync/changes?epoch={epoch}&after={after}&limit=100", ct);

    public Task<Snapshot> BeginSnapshotAsync(CancellationToken ct) =>
        SendAsync<Snapshot>(HttpMethod.Post, "api/v1/sync/snapshots", null, ct, 201);

    public Task<SnapshotPage> SnapshotPageAsync(string token, string? page, CancellationToken ct)
    {
        var path = $"api/v1/sync/snapshots/{token}?limit=100";
        if (!string.IsNullOrEmpty(page)) path += "&page_token=" + Uri.EscapeDataString(page);
        return GetAsync<SnapshotPage>(path, ct);
    }

    public Task<DeviceList> DevicesAsync(CancellationToken ct) => GetAsync<DeviceList>("api/v1/devices", ct);

    public Task RevokeDeviceAsync(string id, CancellationToken ct) =>
        SendAsync(HttpMethod.Delete, $"api/v1/devices/{id}", null, ct, 204);

    private static Dictionary<string, string> Headers(int version, string op) => new()
    {
        ["If-Match"] = $"\"{version}\"",
        ["Idempotency-Key"] = op
    };

    private async Task<T> GetAsync<T>(string path, CancellationToken ct)
    {
        using var res = await _http.GetAsync(path, ct).ConfigureAwait(false);
        return await ReadAsync<T>(res, 200).ConfigureAwait(false);
    }

    private async Task<T> SendAsync<T>(HttpMethod method, string path, object? body, CancellationToken ct, int expected = 200, Dictionary<string, string>? headers = null)
    {
        using var req = new HttpRequestMessage(method, path);
        if (body != null) req.Content = JsonContent.Create(body, options: JsonOpts);
        if (headers != null)
        {
            foreach (var kv in headers) req.Headers.TryAddWithoutValidation(kv.Key, kv.Value);
        }
        using var res = await _http.SendAsync(req, ct).ConfigureAwait(false);
        if (expected == 0)
        {
            if (res.StatusCode is not (System.Net.HttpStatusCode.OK or System.Net.HttpStatusCode.Created))
                throw await ToError(res).ConfigureAwait(false);
            return (await res.Content.ReadFromJsonAsync<T>(JsonOpts, ct).ConfigureAwait(false))!;
        }
        return await ReadAsync<T>(res, expected).ConfigureAwait(false);
    }

    private async Task SendAsync(HttpMethod method, string path, object? body, CancellationToken ct, int expected, Dictionary<string, string>? headers = null)
    {
        using var req = new HttpRequestMessage(method, path);
        if (body != null) req.Content = JsonContent.Create(body, options: JsonOpts);
        if (headers != null)
        {
            foreach (var kv in headers) req.Headers.TryAddWithoutValidation(kv.Key, kv.Value);
        }
        using var res = await _http.SendAsync(req, ct).ConfigureAwait(false);
        if ((int)res.StatusCode != expected)
            throw await ToError(res).ConfigureAwait(false);
    }

    private static async Task<T> ReadAsync<T>(HttpResponseMessage res, int expected)
    {
        if ((int)res.StatusCode != expected)
            throw await ToError(res).ConfigureAwait(false);
        return (await res.Content.ReadFromJsonAsync<T>(JsonOpts).ConfigureAwait(false))!;
    }

    private static async Task<ApiError> ToError(HttpResponseMessage res)
    {
        try
        {
            var doc = await res.Content.ReadFromJsonAsync<ErrorBox>(JsonOpts).ConfigureAwait(false);
            if (doc?.Error != null)
                return new ApiError((int)res.StatusCode, doc.Error.Code, doc.Error.Message);
        }
        catch
        {
            // fall through
        }
        return new ApiError((int)res.StatusCode, "HTTP_ERROR", res.ReasonPhrase ?? "request failed");
    }

    public void Dispose() => _http.Dispose();

    private sealed class ErrorBox
    {
        [JsonPropertyName("error")] public ErrorBody? Error { get; set; }
    }
    private sealed class ErrorBody
    {
        [JsonPropertyName("code")] public string Code { get; set; } = "";
        [JsonPropertyName("message")] public string Message { get; set; } = "";
    }
}

public sealed class DeviceList
{
    [JsonPropertyName("items")] public List<DeviceDto> Items { get; set; } = [];
}
