using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Threading;

namespace OnlineClipboard.Windows.Core;

public sealed class WinClipboard : IClipboardAdapter, IDisposable
{
    private const int WM_CLIPBOARDUPDATE = 0x031D;
    private readonly Window _window;
    private readonly Dispatcher _dispatcher;
    private HwndSource? _source;
    private IntPtr _hwnd;
    private uint _lastSeq;
    private string? _suppressHmac;
    private DateTimeOffset _suppressUntil;
    private string? _suppressClipId;
    public event Action<string>? UserCopied;
    public long Generation { get; private set; }
    public bool CaptureEnabled { get; set; }
    public bool WriteEnabled { get; set; } = true;
    public bool Started { get; private set; }

    public WinClipboard(Window window)
    {
        _window = window;
        _dispatcher = window.Dispatcher;
    }

    public bool CanReadNow => true;
    public bool CanWriteNow => true;

    public void Start()
    {
        _hwnd = new WindowInteropHelper(_window).EnsureHandle();
        _source = HwndSource.FromHwnd(_hwnd);
        _source?.AddHook(Hook);
        AddClipboardFormatListener(_hwnd);
        _lastSeq = GetClipboardSequenceNumber();
        Started = true;
    }

    public async Task<string?> ReadTextAsync(CancellationToken cancellationToken)
    {
        return await _dispatcher.InvokeAsync(() => TryRead()).Task.WaitAsync(cancellationToken);
    }

    public async Task WriteTextAsync(string text, string sourceClipId, CancellationToken cancellationToken)
    {
        await _dispatcher.InvokeAsync(() => WriteTextNow(text, sourceClipId)).Task.WaitAsync(cancellationToken);
    }

    public void WriteTextNow(string text, string sourceClipId)
    {
        void Apply()
        {
            _suppressHmac = Hmac(text);
            _suppressClipId = sourceClipId;
            _suppressUntil = DateTimeOffset.UtcNow.AddSeconds(2);
            TryWrite(text);
            _lastSeq = GetClipboardSequenceNumber();
        }
        if (_dispatcher.CheckAccess()) Apply();
        else _dispatcher.Invoke(Apply);
    }

    public void MarkLocalCopy(string text)
    {
        Generation++;
        _suppressHmac = Hmac(text);
        _suppressClipId = "local";
        _suppressUntil = DateTimeOffset.UtcNow.AddSeconds(2);
    }

    private IntPtr Hook(IntPtr hwnd, int msg, IntPtr wParam, IntPtr lParam, ref bool handled)
    {
        if (msg != WM_CLIPBOARDUPDATE) return IntPtr.Zero;
        var seq = GetClipboardSequenceNumber();
        if (seq == _lastSeq) return IntPtr.Zero;
        _lastSeq = seq;
        if (!Started || !CaptureEnabled) return IntPtr.Zero;
        var text = TryRead();
        if (string.IsNullOrEmpty(text)) return IntPtr.Zero;
        if (DateTimeOffset.UtcNow < _suppressUntil && Hmac(text) == _suppressHmac)
        {
            _suppressHmac = null;
            return IntPtr.Zero;
        }
        Generation++;
        UserCopied?.Invoke(text);
        return IntPtr.Zero;
    }

    private static string? TryRead()
    {
        foreach (var delay in new[] { 0, 20, 50, 100, 200 })
        {
            if (delay > 0) Thread.Sleep(delay);
            try
            {
                if (!Clipboard.ContainsText()) return null;
                var text = Clipboard.GetText(TextDataFormat.UnicodeText);
                return text.Length == 0 ? null : text;
            }
            catch (COMException)
            {
            }
        }
        return null;
    }

    private static void TryWrite(string text)
    {
        foreach (var delay in new[] { 0, 20, 50, 100, 200 })
        {
            if (delay > 0) Thread.Sleep(delay);
            try
            {
                Clipboard.SetText(text, TextDataFormat.UnicodeText);
                return;
            }
            catch (COMException)
            {
            }
        }
    }

    private static string Hmac(string text)
    {
        var bytes = System.Text.Encoding.UTF8.GetBytes(text);
        var hash = System.Security.Cryptography.SHA256.HashData(bytes);
        return Convert.ToHexString(hash);
    }

    public void Dispose()
    {
        if (_hwnd != IntPtr.Zero) RemoveClipboardFormatListener(_hwnd);
        _source?.RemoveHook(Hook);
    }

    [DllImport("user32.dll")] private static extern bool AddClipboardFormatListener(IntPtr hwnd);
    [DllImport("user32.dll")] private static extern bool RemoveClipboardFormatListener(IntPtr hwnd);
    [DllImport("user32.dll")] private static extern uint GetClipboardSequenceNumber();
}
