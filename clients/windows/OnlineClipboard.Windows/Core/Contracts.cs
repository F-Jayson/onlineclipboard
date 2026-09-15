namespace OnlineClipboard.Windows.Core;

// No adapters are registered by the skeleton. The eventual implementation must
// keep clipboard access on an STA thread and networking outside callbacks.
public interface IClipboardAdapter
{
    bool CanReadNow { get; }
    bool CanWriteNow { get; }
    Task<string?> ReadTextAsync(CancellationToken cancellationToken);
    Task WriteTextAsync(string text, string sourceClipId, CancellationToken cancellationToken);
}

public interface ISyncCoordinator
{
    Task CatchUpAsync(CancellationToken cancellationToken);
    Task PauseAsync(CancellationToken cancellationToken);
}

public interface ILocalVault
{
    bool IsUnlocked { get; }
    Task UnlockAsync(string recoveryCode, CancellationToken cancellationToken);
    void Lock();
}
