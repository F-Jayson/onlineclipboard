using System.IO;
using System.Text;
using System.Windows;
using System.Windows.Controls;
using OnlineClipboard.Windows.Core;

namespace OnlineClipboard.Windows;

public partial class MainWindow : Window
{
    private LocalStore? _store;
    private ApiClient? _api;
    private AppSession _session = new();
    private readonly SyncEngine _sync = new();
    private WinClipboard? _clipboard;
    private readonly CancellationTokenSource _cts = new();
    private bool _busy;
    private List<DeviceDto> _devices = [];

    public MainWindow() => InitializeComponent();

    private async void OnLoaded(object sender, RoutedEventArgs e)
    {
        var dir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "OnlineClipboard");
        _store = new LocalStore(Path.Combine(dir, "local.db"));
        _clipboard = new WinClipboard(this);
        _clipboard.Start();
        _clipboard.UserCopied += text => Dispatcher.Invoke(() => _ = OnLocalCopy(text));
        _sync.Clipboard = _clipboard;
        _sync.Store = _store;
        _sync.Changed += () => Dispatcher.Invoke(RefreshLists);
        var restored = AppSession.Restore(_store);
        if (restored != null)
        {
            _session = restored;
            OriginBox.Text = _session.Origin;
            UserBox.Text = _store.GetString("username") ?? "";
            CaptureBox.IsChecked = _session.Capture;
            WriteBox.IsChecked = _session.AutoWrite;
            _clipboard.CaptureEnabled = _session.Capture;
            _clipboard.WriteEnabled = _session.AutoWrite;
            if (!string.IsNullOrEmpty(_session.Origin))
            {
                _api = new ApiClient(_session.Origin);
                _api.SetAccessToken(_session.AccessToken);
                _sync.Api = _api;
                _sync.Session = _session;
            }
        }
        else
        {
            _session.DeviceId = AppSession.NewId();
            _session.DeviceName = "Windows";
        }
        SetStatus(_session.Cmk == null ? "请连接服务器并解锁保险库" : "已解锁，可同步");
        RefreshLists();
        if (_session.Cmk != null && _api != null)
        {
            try { await _sync.RunOnceAsync(_cts.Token); }
            catch (Exception ex) { SetStatus(ex.Message); }
            RefreshLists();
        }
        _ = LoopAsync();
    }

    private async Task LoopAsync()
    {
        using var timer = new PeriodicTimer(TimeSpan.FromSeconds(60));
        while (await timer.WaitForNextTickAsync(_cts.Token))
        {
            if (_session.Cmk == null || _api == null) continue;
            try
            {
                await _sync.RunOnceAsync(_cts.Token);
                Dispatcher.Invoke(RefreshLists);
            }
            catch (Exception ex)
            {
                Dispatcher.Invoke(() => SetStatus(ex.Message));
            }
        }
    }

    private async Task OnLocalCopy(string text)
    {
        if (_session.Cmk == null || !_session.Capture) return;
        var bytes = Encoding.UTF8.GetBytes(text);
        if (bytes.Length == 0 || bytes.Length > 65536) return;
        try
        {
            await _sync.EnqueueLocalAsync(text, live: true, _cts.Token);
            SetStatus("已采集，正在上传");
            RefreshLists();
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async void OnTestOrigin(object sender, RoutedEventArgs e)
    {
        try
        {
            var origin = Origin.Normalize(OriginBox.Text);
            using var api = new ApiClient(origin);
            var info = await api.ServerInfoAsync(_cts.Token);
            OriginStatus.Text = info.SyncAvailable
                ? $"已连接 {info.Name} {info.Version}，注册模式 {info.RegistrationMode}"
                : $"服务器尚未就绪（stage={info.Stage}）";
        }
        catch (Exception ex)
        {
            OriginStatus.Text = ex.Message;
        }
    }

    private async void OnRegister(object sender, RoutedEventArgs e) => await Auth(register: true);
    private async void OnLogin(object sender, RoutedEventArgs e) => await Auth(register: false);

    private async Task Auth(bool register)
    {
        if (_busy) return;
        _busy = true;
        try
        {
            var origin = Origin.Normalize(OriginBox.Text);
            _api?.Dispose();
            _api = new ApiClient(origin);
            object body = register
                ? new
                {
                    username = UserBox.Text.Trim(),
                    password = PassBox.Password,
                    invite_token = InviteBox.Text.Trim(),
                    device = new { id = _session.DeviceId, name = _session.DeviceName, platform = "windows" }
                }
                : new
                {
                    username = UserBox.Text.Trim(),
                    password = PassBox.Password,
                    device = new { id = _session.DeviceId, name = _session.DeviceName, platform = "windows" }
                };
            var result = register ? await _api.RegisterAsync(body, _cts.Token) : await _api.LoginAsync(body, _cts.Token);
            _session.Origin = origin;
            _session.UserId = result.UserId;
            _session.DeviceId = result.DeviceId;
            _session.AccessToken = result.AccessToken;
            _session.RefreshToken = result.RefreshToken;
            _api.SetAccessToken(result.AccessToken);
            _store!.Set("username", UserBox.Text.Trim());
            _session.PersistSecrets(_store);
            _sync.Api = _api;
            _sync.Session = _session;
            _sync.Store = _store;
            SetStatus(result.VaultInitialized ? "已登录，请输入恢复密钥解锁" : "已登录，请初始化保险库");
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
        finally { _busy = false; }
    }

    private async void OnInitVault(object sender, RoutedEventArgs e)
    {
        if (_api == null || _store == null) { SetStatus("请先登录"); return; }
        try
        {
            var cmk = CryptoV1.RandomBytes(32);
            var rk = CryptoV1.RandomBytes(32);
            var salt = CryptoV1.RandomBytes(32);
            var nonce = CryptoV1.RandomBytes(12);
            var vaultId = AppSession.NewId();
            var wrapped = CryptoV1.WrapCmk(rk, salt, nonce, cmk, _session.UserId, vaultId);
            var env = new VaultEnvelope
            {
                VaultId = vaultId,
                WrapSalt = CryptoV1.Encode(salt),
                WrapNonce = CryptoV1.Encode(nonce),
                WrappedKey = CryptoV1.Encode(wrapped)
            };
            await _api.PutVaultAsync(env, _cts.Token);
            _session.VaultId = vaultId;
            _session.Cmk = cmk;
            _session.PersistSecrets(_store);
            var code = CryptoV1.RecoveryCode(rk);
            RecoveryBox.Text = code;
            KeyStatus.Text = "请离线保存恢复密钥。登录密码不能解密历史。";
            MessageBox.Show(this, "请立即抄写并离线保存恢复密钥：\n\n" + code, "恢复密钥");
            SetStatus("保险库已初始化");
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private async void OnUnlock(object sender, RoutedEventArgs e)
    {
        if (_api == null || _store == null) { SetStatus("请先登录"); return; }
        try
        {
            var vault = await _api.GetVaultAsync(_cts.Token);
            var rk = CryptoV1.ParseRecoveryCode(RecoveryBox.Text);
            var cmk = CryptoV1.UnwrapCmk(rk, CryptoV1.Decode(vault.WrapSalt), CryptoV1.Decode(vault.WrapNonce),
                CryptoV1.Decode(vault.WrappedKey), _session.UserId, vault.VaultId);
            _session.VaultId = vault.VaultId;
            _session.Cmk = cmk;
            _session.PersistSecrets(_store);
            _sync.Session = _session;
            KeyStatus.Text = "已解锁";
            SetStatus("已解锁，开始同步");
            await _sync.RunOnceAsync(_cts.Token);
            RefreshLists();
        }
        catch (Exception ex)
        {
            KeyStatus.Text = "恢复密钥不正确，或信封无法解密。";
            SetStatus(ex.Message);
        }
    }

    private async void OnSync(object sender, RoutedEventArgs e)
    {
        if (_session.Cmk == null || _api == null) { SetStatus("请先解锁"); return; }
        try
        {
            await _sync.RunOnceAsync(_cts.Token);
            await LoadDevices();
            RefreshLists();
            SetStatus("已同步");
        }
        catch (Exception ex) { SetStatus(ex.Message); }
    }

    private void OnToggleCapture(object sender, RoutedEventArgs e)
    {
        _session.Capture = CaptureBox.IsChecked == true;
        if (_clipboard != null) _clipboard.CaptureEnabled = _session.Capture;
        _store?.Set("capture", _session.Capture ? "1" : "0");
    }

    private void OnToggleWrite(object sender, RoutedEventArgs e)
    {
        _session.AutoWrite = WriteBox.IsChecked == true;
        if (_clipboard != null) _clipboard.WriteEnabled = _session.AutoWrite;
        _store?.Set("auto_write", _session.AutoWrite ? "1" : "0");
    }

    private void OnSearch(object sender, TextChangedEventArgs e) => RefreshLists();

    private void OnSelectHistory(object sender, SelectionChangedEventArgs e)
    {
        if (HistoryList.SelectedItem is HistoryItem item) DetailBox.Text = item.Text;
    }

    private async void OnCopySelected(object sender, RoutedEventArgs e)
    {
        if (HistoryList.SelectedItem is not HistoryItem item || _clipboard == null) return;
        _clipboard.MarkLocalCopy(item.Text);
        await _clipboard.WriteTextAsync(item.Text, item.Id, _cts.Token);
        SetStatus("已复制到本机剪贴板（不会再次上传）");
    }

    private async void OnTrashSelected(object sender, RoutedEventArgs e)
    {
        if (HistoryList.SelectedItem is not HistoryItem item || _api == null) return;
        try
        {
            await _api.TrashAsync(item.Id, item.Version, AppSession.NewId(), _cts.Token);
            await _sync.RunOnceAsync(_cts.Token);
            RefreshLists();
        }
        catch (Exception ex) { SetStatus(ex.Message); }
    }

    private async void OnRestore(object sender, RoutedEventArgs e)
    {
        if (TrashList.SelectedItem is not HistoryItem item || _api == null) return;
        try
        {
            await _api.RestoreAsync(item.Id, item.Version, AppSession.NewId(), _cts.Token);
            await _sync.RunOnceAsync(_cts.Token);
            RefreshLists();
        }
        catch (Exception ex) { SetStatus(ex.Message); }
    }

    private async void OnPurge(object sender, RoutedEventArgs e)
    {
        if (TrashList.SelectedItem is not HistoryItem item || _api == null) return;
        if (MessageBox.Show(this, "永久删除后无法从服务器恢复。", "确认", MessageBoxButton.OKCancel) != MessageBoxResult.OK)
            return;
        try
        {
            await _api.PurgeAsync(item.Id, item.Version, AppSession.NewId(), _cts.Token);
            _store?.DeleteClip(item.Id);
            await _sync.RunOnceAsync(_cts.Token);
            RefreshLists();
        }
        catch (Exception ex) { SetStatus(ex.Message); }
    }

    private async void OnRevoke(object sender, RoutedEventArgs e)
    {
        if (DeviceList.SelectedIndex < 0 || DeviceList.SelectedIndex >= _devices.Count || _api == null) return;
        var d = _devices[DeviceList.SelectedIndex];
        try
        {
            await _api.RevokeDeviceAsync(d.Id, _cts.Token);
            await LoadDevices();
        }
        catch (Exception ex) { SetStatus(ex.Message); }
    }

    private async Task LoadDevices()
    {
        if (_api == null) return;
        try
        {
            var page = await _api.DevicesAsync(_cts.Token);
            _devices = page.Items;
            DeviceList.ItemsSource = _devices.Select(d => $"{d.Name} ({d.Platform}) {(d.RevokedAt == null ? "" : "已撤销")}").ToList();
        }
        catch { /* ignore */ }
    }

    private void RefreshLists()
    {
        if (_store == null) return;
        var q = SearchBox.Text?.Trim() ?? "";
        var active = _store.LoadClips("active");
        if (q.Length > 0 && _session.Cmk != null)
            active = active.Where(i => i.Text.Contains(q, StringComparison.OrdinalIgnoreCase)).ToList();
        HistoryList.ItemsSource = active;
        TrashList.ItemsSource = _store.LoadClips("trash");
        var n = active.Count;
        if (q.Length > 0) SetStatus($"搜索已下载的 {n} 条");
    }

    private void SetStatus(string text)
    {
        StatusText.Text = text;
        _sync.Status = text;
    }

    private void OnClosed(object? sender, EventArgs e)
    {
        _cts.Cancel();
        _clipboard?.Dispose();
        _store?.Dispose();
        _api?.Dispose();
    }
}
