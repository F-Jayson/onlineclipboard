using System.Text.Json;

namespace OnlineClipboard.Windows.Core;

public sealed class SyncEngine
{
    private readonly SemaphoreSlim _gate = new(1, 1);
    public ApiClient Api { get; set; } = null!;
    public LocalStore Store { get; set; } = null!;
    public AppSession Session { get; set; } = null!;
    public WinClipboard Clipboard { get; set; } = null!;
    public event Action? Changed;
    public string Status { get; set; } = "未连接";
    public bool CatchingUp { get; private set; } = true;

    public async Task RunOnceAsync(CancellationToken ct)
    {
        await _gate.WaitAsync(ct);
        try
        {
            await EnsureAuth(ct);
            await FlushOutbox(ct);
            await CatchUp(ct);
            await PullChanges(ct);
            Status = "已同步";
        }
        catch (ApiError ex) when (ex.Code is "UNAUTHENTICATED" or "TOKEN_EXPIRED")
        {
            await Refresh(ct);
            throw;
        }
        finally
        {
            _gate.Release();
        }
    }

    private async Task EnsureAuth(CancellationToken ct)
    {
        Api.SetAccessToken(Session.AccessToken);
        if (string.IsNullOrEmpty(Session.AccessToken))
            throw new ApiError(401, "UNAUTHENTICATED", "需要登录。");
        await Task.CompletedTask;
    }

    public async Task Refresh(CancellationToken ct)
    {
        if (string.IsNullOrEmpty(Session.RefreshToken)) throw new ApiError(401, "UNAUTHENTICATED", "需要重新登录。");
        var pair = await Api.RefreshAsync(Session.RefreshToken, ct);
        Session.AccessToken = pair.AccessToken;
        Session.RefreshToken = pair.RefreshToken;
        Api.SetAccessToken(pair.AccessToken);
        Session.PersistSecrets(Store);
    }

    private async Task FlushOutbox(CancellationToken ct)
    {
        foreach (var (id, json) in Store.Outbox())
        {
            var env = AppSession.EnvelopeFromJson(json);
            var receipt = await Api.CreateClipAsync(env, ct);
            Store.RemoveOutbox(id);
            var item = Store.GetClip(id);
            if (item != null)
            {
                item.Version = receipt.Version;
                item.CreatedSeq = receipt.Seq;
                Store.UpsertClip(item, Session.VaultId, env.Nonce, env.Ciphertext);
            }
        }
    }

    private async Task CatchUp(CancellationToken ct)
    {
        CatchingUp = true;
        if (string.IsNullOrEmpty(Session.SyncEpoch) || Session.Cursor == "")
        {
            await Rebuild(ct);
        }
        CatchingUp = false;
    }

    private async Task Rebuild(CancellationToken ct)
    {
        Status = "正在校准历史…";
        var snap = await Api.BeginSnapshotAsync(ct);
        string? page = null;
        var seen = new HashSet<string>();
        do
        {
            var batch = await Api.SnapshotPageAsync(snap.Token, page, ct);
            foreach (var clip in batch.Items)
            {
                ApplyRemote(clip, writeClipboard: false);
                seen.Add(clip.Id);
            }
            page = batch.NextPageToken;
        } while (!string.IsNullOrEmpty(page));
        Session.SyncEpoch = snap.SyncEpoch;
        Session.Cursor = snap.BaseSeq;
        Session.PersistSecrets(Store);
        await PullChanges(ct);
        Changed?.Invoke();
    }

    private async Task PullChanges(CancellationToken ct)
    {
        if (string.IsNullOrEmpty(Session.SyncEpoch)) return;
        while (true)
        {
            ChangePage page;
            try
            {
                page = await Api.ChangesAsync(Session.SyncEpoch, Session.Cursor, ct);
            }
            catch (ApiError ex) when (ex.Code is "CURSOR_EXPIRED" or "SYNC_RESET_REQUIRED")
            {
                Session.SyncEpoch = "";
                Session.Cursor = "0";
                await Rebuild(ct);
                return;
            }
            Session.LastServerTime = page.ServerTime;
            foreach (var ev in page.Events)
            {
                try
                {
                    var clip = await Api.GetClipAsync(ev.ClipId, ct);
                    var live = !CatchingUp && ev.Kind == "clip.created" && clip.DeliveryIntent == "live"
                               && clip.SourceDeviceId != Session.DeviceId
                               && page.ServerTime - clip.CreatedAt <= TimeSpan.FromSeconds(30)
                               && Session.AutoWrite && Clipboard.WriteEnabled;
                    ApplyRemote(clip, live);
                }
                catch (ApiError ex) when (ex.Code is "CLIP_PURGED" or "TRASH_EXPIRED")
                {
                    Store.DeleteClip(ev.ClipId);
                }
            }
            Session.Cursor = page.NextSeq;
            Session.SyncEpoch = page.SyncEpoch;
            Session.PersistSecrets(Store);
            Changed?.Invoke();
            if (!page.HasMore) break;
        }
    }

    public void ApplyRemote(ClipDto clip, bool writeClipboard)
    {
        if (Session.Cmk == null) return;
        string text;
        try
        {
            text = Session.Decrypt(clip);
        }
        catch
        {
            return;
        }
        var item = new HistoryItem
        {
            Id = clip.Id, Status = clip.Status, Version = clip.Version, CreatedSeq = clip.CreatedSeq,
            CreatedAt = clip.CreatedAt, ExpiresAt = clip.ExpiresAt, SourceDeviceId = clip.SourceDeviceId,
            Text = text, Preview = LocalStore.PreviewOf(text)
        };
        Store.UpsertClip(item, clip.VaultId, clip.Nonce, clip.Ciphertext);
        if (writeClipboard && !CatchingUp)
        {
            var gen = Clipboard.Generation;
            if (Clipboard.Generation != gen) return;
            try { Clipboard.WriteTextNow(text, clip.Id); }
            catch { /* busy clipboard */ }
        }
    }

    public async Task EnqueueLocalAsync(string text, bool live, CancellationToken ct)
    {
        var bytes = System.Text.Encoding.UTF8.GetBytes(text);
        if (bytes.Length == 0 || bytes.Length > 65536) return;
        var intent = live ? "live" : "history_only";
        var env = Session.Encrypt(text, intent);
        Store.AddOutbox(env.Id, AppSession.EnvelopeJson(env));
        var item = new HistoryItem
        {
            Id = env.Id, Status = "active", Version = 1, CreatedSeq = "0",
            CreatedAt = DateTimeOffset.UtcNow, SourceDeviceId = Session.DeviceId,
            Text = text, Preview = LocalStore.PreviewOf(text)
        };
        Store.UpsertClip(item, Session.VaultId, env.Nonce, env.Ciphertext);
        Changed?.Invoke();
        try
        {
            await FlushOutbox(ct);
            Changed?.Invoke();
        }
        catch
        {
            Status = "待上传 " + Store.OutboxCount();
        }
    }
}
