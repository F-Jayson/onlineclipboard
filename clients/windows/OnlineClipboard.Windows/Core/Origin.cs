using System.Net;
using System.Net.Sockets;

namespace OnlineClipboard.Windows.Core;

public static class Origin
{
    public static string Normalize(string input)
    {
        input = input.Trim();
        if (input.Length == 0) throw new FormatException("请填写服务器地址。");
        if (input.Contains('?', StringComparison.Ordinal) || input.Contains('#', StringComparison.Ordinal) || input.Contains('@', StringComparison.Ordinal))
            throw new FormatException("地址不能包含用户名、查询或片段。");
        if (!input.Contains("://", StringComparison.Ordinal))
            input = "https://" + input;
        if (!Uri.TryCreate(input, UriKind.Absolute, out var uri))
            throw new FormatException("无法解析服务器地址。");
        if (uri.Scheme is not ("https" or "http"))
            throw new FormatException("只支持 http 或 https。");
        if (uri.PathAndQuery is not ("/" or "") && uri.PathAndQuery != "/")
            throw new FormatException("首版不支持子路径，请只填写主机和端口。");
        var host = uri.IdnHost;
        var loopback = IsLoopback(host);
        if (uri.Scheme == "http" && !loopback)
            throw new FormatException("非本机地址必须使用 HTTPS。");
        var builder = new UriBuilder(uri.Scheme, host, uri.IsDefaultPort ? -1 : uri.Port);
        return builder.Uri.GetLeftPart(UriPartial.Authority).TrimEnd('/');
    }

    public static bool IsLoopback(string host)
    {
        if (host.Equals("localhost", StringComparison.OrdinalIgnoreCase) || host == "127.0.0.1" || host == "::1")
            return true;
        if (IPAddress.TryParse(host, out var ip))
            return IPAddress.IsLoopback(ip);
        try
        {
            return Dns.GetHostAddresses(host).Any(IPAddress.IsLoopback);
        }
        catch (SocketException)
        {
            return false;
        }
    }
}
