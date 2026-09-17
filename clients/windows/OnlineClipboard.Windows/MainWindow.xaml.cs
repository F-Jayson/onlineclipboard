using System.Diagnostics;
using System.IO;
using System.Text;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Documents;
using System.Windows.Media;
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
    private ServerInfo? _info;

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
                _ = RefreshServerInfoAsync();
            }
        }
        else
        {
            _session.DeviceId = AppSession.NewId();
            _session.DeviceName = "Windows";
        }
        SetStatus(_session.Cmk == null ? "请连接服务器并解锁保险库" : "已解锁，可同步");
        ShowPage(0);
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
            _info = info;
            ApplyAuthMode(info);
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
            var password = PassBox.Password;
            _api?.Dispose();
            _api = new ApiClient(origin);
            try { _info = await _api.ServerInfoAsync(_cts.Token); ApplyAuthMode(_info); } catch { /* probe later */ }
            object body = register
                ? new
                {
                    username = UserBox.Text.Trim(),
                    password,
                    invite_token = InviteBox.Text.Trim(),
                    email = EmailBox.Text.Trim(),
                    email_code = EmailCodeBox.Text.Trim(),
                    device = new { id = _session.DeviceId, name = _session.DeviceName, platform = "windows" }
                }
                : new
                {
                    username = UserBox.Text.Trim(),
                    password,
                    device = new { id = _session.DeviceId, name = _session.DeviceName, platform = "windows" }
                };
            var previousUser = _session.UserId;
            var previousCmk = _session.Cmk;
            var previousVault = _session.VaultId;
            var result = register ? await _api.RegisterAsync(body, _cts.Token) : await _api.LoginAsync(body, _cts.Token);
            _session.Origin = origin;
            _session.UserId = result.UserId;
            _session.DeviceId = result.DeviceId;
            _session.AccessToken = result.AccessToken;
            _session.RefreshToken = result.RefreshToken;
            if (result.UserId != previousUser)
            {
                _session.Cmk = null;
                _session.VaultId = "";
                _session.SyncEpoch = "";
                _session.Cursor = "0";
                _store!.ClearUserData();
            }
            else
            {
                _session.Cmk = previousCmk;
                _session.VaultId = previousVault;
            }
            _api.SetAccessToken(result.AccessToken);
            _store!.Set("username", UserBox.Text.Trim());
            _session.PersistSecrets(_store);
            _sync.Api = _api;
            _sync.Session = _session;
            _sync.Store = _store;
            await UnlockAfterAuthAsync(password, result.VaultInitialized);
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
        finally { _busy = false; }
    }

    private async void OnSendEmailCode(object sender, RoutedEventArgs e)
    {
        try
        {
            var origin = Origin.Normalize(OriginBox.Text);
            using var api = new ApiClient(origin);
            await api.RequestEmailCodeAsync(EmailBox.Text.Trim(), _cts.Token);
            SetStatus("验证码已发送，请查收邮箱。");
        }
        catch (Exception ex)
        {
            SetStatus(ex.Message);
        }
    }

    private void OnOpenRegister(object sender, RoutedEventArgs e)
    {
        var url = _info?.ExternalRegisterUrl;
        if (string.IsNullOrWhiteSpace(url)) return;
        Process.Start(new ProcessStartInfo(url) { UseShellExecute = true });
    }

    private async void OnInitVault(object sender, RoutedEventArgs e)
    {
        if (_api == null || _store == null) { SetStatus("请先登录"); return; }
        if (string.IsNullOrEmpty(PassBox.Password)) { SetStatus("请先填写登录密码，再初始化保险库。"); return; }
        try
        {
            await InitVaultWithPasswordAsync(PassBox.Password, showRecovery: true);
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
            await UnlockAfterAuthAsync(PassBox.Password, vaultInitialized: true);
            if (_session.Cmk == null)
            {
                var vault = await _api.GetVaultAsync(_cts.Token);
                var rk = CryptoV1.ParseRecoveryCode(RecoveryBox.Text);
                var cmk = CryptoV1.UnwrapCmk(rk, CryptoV1.Decode(vault.WrapSalt), CryptoV1.Decode(vault.WrapNonce),
                    CryptoV1.Decode(vault.WrappedKey), _session.UserId, vault.VaultId);
                await FinishUnlockAsync(cmk, vault, PassBox.Password);
            }
        }
        catch (Exception ex)
        {
            KeyStatus.Text = "恢复密钥不正确，或信封无法解密。";
            SetStatus(ex.Message);
        }
    }

    private async Task RefreshServerInfoAsync()
    {
        if (_api == null) return;
        try
        {
            _info = await _api.ServerInfoAsync(_cts.Token);
            Dispatcher.Invoke(() => ApplyAuthMode(_info));
        }
        catch { /* ignore */ }
    }

    private void ApplyAuthMode(ServerInfo? info)
    {
        var mode = info?.RegistrationMode ?? "";
        EmailPanel.Visibility = Vis(mode == "email");
        InvitePanel.Visibility = Vis(mode is "" or "invite");
        RegisterBtn.Visibility = Vis(mode is not "external" and not "disabled");
        RegisterHint.Visibility = Vis(mode == "external" && !string.IsNullOrWhiteSpace(info?.ExternalRegisterUrl));
        var min = info is { MinPasswordChars: > 0 } ? info.MinPasswordChars : 12;
        PassLabel.Text = min <= 1 ? "密码" : $"密码（至少 {min} 位）";
        UserLabel.Text = mode == "external" ? "站点账号或邮箱" : "用户名或邮箱";
    }

    private async Task UnlockAfterAuthAsync(string password, bool vaultInitialized)
    {
        if (_api == null || _store == null) return;
        if (!vaultInitialized)
        {
            if (string.IsNullOrEmpty(password)) { SetStatus("已登录，请填写密码后初始化保险库"); return; }
            await InitVaultWithPasswordAsync(password, showRecovery: true);
            return;
        }
        VaultEnvelope vault;
        try { vault = await _api.GetVaultAsync(_cts.Token); }
        catch (ApiError ex) when (ex.Code == "VAULT_NOT_INITIALIZED")
        {
            if (string.IsNullOrEmpty(password)) { SetStatus("已登录，请初始化保险库"); return; }
            await InitVaultWithPasswordAsync(password, showRecovery: true);
            return;
        }
        byte[]? cmk = null;
        if (_session.Cmk != null && _session.VaultId == vault.VaultId)
            cmk = _session.Cmk;
        if (cmk == null && vault.PasswordWrap != null && !string.IsNullOrEmpty(password))
        {
            try { cmk = await Task.Run(() => UnwrapPassword(vault, password)); }
            catch { /* stale wrap */ }
        }
        if (cmk == null && !string.IsNullOrWhiteSpace(RecoveryBox.Text))
        {
            try
            {
                var rk = CryptoV1.ParseRecoveryCode(RecoveryBox.Text);
                cmk = CryptoV1.UnwrapCmk(rk, CryptoV1.Decode(vault.WrapSalt), CryptoV1.Decode(vault.WrapNonce),
                    CryptoV1.Decode(vault.WrappedKey), _session.UserId, vault.VaultId);
            }
            catch { /* wait for manual unlock */ }
        }
        if (cmk == null)
        {
            SetStatus("已登录。新设备请输入恢复密钥解锁，之后即可用登录密码打开历史。");
            return;
        }
        await FinishUnlockAsync(cmk, vault, password);
    }

    private async Task InitVaultWithPasswordAsync(string password, bool showRecovery)
    {
        if (_api == null || _store == null) return;
        var cmk = CryptoV1.RandomBytes(32);
        var rk = CryptoV1.RandomBytes(32);
        var salt = CryptoV1.RandomBytes(32);
        var nonce = CryptoV1.RandomBytes(12);
        var vaultId = AppSession.NewId();
        var wrapped = await Task.Run(() => CryptoV1.WrapCmk(rk, salt, nonce, cmk, _session.UserId, vaultId));
        var pw = await Task.Run(() => CryptoV1.MakePasswordWrap(password, cmk, _session.UserId, vaultId));
        var env = new VaultEnvelope
        {
            VaultId = vaultId,
            WrapSalt = CryptoV1.Encode(salt),
            WrapNonce = CryptoV1.Encode(nonce),
            WrappedKey = CryptoV1.Encode(wrapped),
            PasswordWrap = pw
        };
        await _api.PutVaultAsync(env, _cts.Token);
        _session.VaultId = vaultId;
        _session.Cmk = cmk;
        _session.PersistSecrets(_store);
        var code = CryptoV1.RecoveryCode(rk);
        RecoveryBox.Text = code;
        KeyStatus.Text = "请离线保存恢复密钥。之后登录即可用密码解锁。";
        if (showRecovery)
            MessageBox.Show(this, "请立即抄写并离线保存恢复密钥（密码丢失时用来恢复）：\n\n" + code, "恢复密钥");
        SetStatus("保险库已初始化并解锁");
        await _sync.RunOnceAsync(_cts.Token);
        RefreshLists();
    }

    private async Task FinishUnlockAsync(byte[] cmk, VaultEnvelope vault, string password)
    {
        if (_api == null || _store == null) return;
        _session.VaultId = vault.VaultId;
        _session.Cmk = cmk;
        _session.PersistSecrets(_store);
        _sync.Session = _session;
        if (!string.IsNullOrEmpty(password))
        {
            var wrapOk = false;
            if (vault.PasswordWrap != null)
            {
                try
                {
                    await Task.Run(() => UnwrapPassword(vault, password));
                    wrapOk = true;
                }
                catch { wrapOk = false; }
            }
            if (!wrapOk)
            {
                var pw = await Task.Run(() => CryptoV1.MakePasswordWrap(password, cmk, _session.UserId, vault.VaultId));
                await _api.PutPasswordWrapAsync(pw, _cts.Token);
            }
        }
        KeyStatus.Text = "已解锁";
        SetStatus("已解锁，开始同步");
        await _sync.RunOnceAsync(_cts.Token);
        RefreshLists();
    }

    private byte[] UnwrapPassword(VaultEnvelope vault, string password)
    {
        var wrap = vault.PasswordWrap ?? throw new InvalidOperationException("no password wrap");
        return CryptoV1.UnwrapCmkWithPassword(
            password,
            CryptoV1.Decode(wrap.KdfSalt),
            CryptoV1.Decode(wrap.WrapSalt),
            CryptoV1.Decode(wrap.WrapNonce),
            CryptoV1.Decode(wrap.WrappedKey),
            _session.UserId,
            vault.VaultId,
            wrap.Time, wrap.Memory, wrap.Parallelism);
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
        if (DeviceList.SelectedItem is not DeviceDto d || _api == null) return;
        try
        {
            await _api.RevokeDeviceAsync(d.Id, _cts.Token);
            await LoadDevices();
        }
        catch (Exception ex) { SetStatus(ex.Message); }
    }

    private void OnNavConnect(object sender, RoutedEventArgs e) => ShowPage(0);
    private void OnNavHistory(object sender, RoutedEventArgs e) => ShowPage(1);
    private void OnNavTrash(object sender, RoutedEventArgs e) => ShowPage(2);
    private void OnNavDevices(object sender, RoutedEventArgs e)
    {
        ShowPage(3);
        _ = LoadDevices();
    }

    private void ShowPage(int index)
    {
        ConnectPage.Visibility = Vis(index == 0);
        HistoryPage.Visibility = Vis(index == 1);
        TrashPage.Visibility = Vis(index == 2);
        DevicePage.Visibility = Vis(index == 3);
        PageTitle.Text = index switch
        {
            0 => "连接与账号",
            1 => "剪贴历史",
            2 => "回收站",
            _ => "已登录设备"
        };
        PageSubtitle.Text = index switch
        {
            0 => "连接自托管服务器，登录后即可用密码解锁历史",
            1 => "明文只保存在这台电脑，服务器只有密文",
            2 => "删除后 168 小时内可恢复，到期将从服务器清除",
            _ => "撤销设备会立即切断该设备的同步会话"
        };
        MarkNav(NavConnect, index == 0);
        MarkNav(NavHistory, index == 1);
        MarkNav(NavTrash, index == 2);
        MarkNav(NavDevices, index == 3);
    }

    private static Visibility Vis(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    private static void MarkNav(Button button, bool on) => button.Tag = on ? "Active" : null;

    private async Task LoadDevices()
    {
        if (_api == null) return;
        try
        {
            var page = await _api.DevicesAsync(_cts.Token);
            _devices = page.Items;
            DeviceList.ItemsSource = _devices;
            DeviceEmpty.Visibility = Vis(_devices.Count == 0);
        }
        catch { /* ignore */ }
    }

    private void RefreshLists()
    {
        if (_store == null) return;
        var q = SearchBox.Text?.Trim() ?? "";
        SearchHint.Visibility = Vis(q.Length == 0);
        var active = _store.LoadClips("active");
        if (q.Length > 0 && _session.Cmk != null)
            active = active.Where(i => i.Text.Contains(q, StringComparison.OrdinalIgnoreCase)).ToList();
        HistoryList.ItemsSource = active;
        var trash = _store.LoadClips("trash");
        TrashList.ItemsSource = trash;
        HistoryEmpty.Visibility = Vis(active.Count == 0);
        TrashEmpty.Visibility = Vis(trash.Count == 0);
        if (q.Length > 0) SetStatus($"搜索已下载的 {active.Count} 条");
    }

    private void SetStatus(string text)
    {
        StatusText.Text = text;
        SidebarStatus.Text = text;
        _sync.Status = text;
        StatusDot.Fill = new SolidColorBrush(_session.Cmk != null
            ? Color.FromRgb(0x22, 0xC5, 0x5E)
            : Color.FromRgb(0x94, 0xA3, 0xB8));
    }

    private void OnClosed(object? sender, EventArgs e)
    {
        _cts.Cancel();
        _clipboard?.Dispose();
        _store?.Dispose();
        _api?.Dispose();
    }
}
