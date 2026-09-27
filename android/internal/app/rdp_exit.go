package app

// FreeRDP 3.32 encodes the error class in the high 16 bits: ERRINFO=1,
// CONNECT=2. In particular, 0x1000c (server logoff) is not 0x2000c
// (security negotiation). Do not infer a password failure from either code.
func rdpExitStatus(code int, connected bool) (string, string) {
	ended := func(message string) (string, string) {
		if connected {
			return "disconnected", "🔌  " + message + "，可重新连接"
		}
		return "error", "⚠️  尚未建立远程桌面：" + message + "。请检查远程 Windows 登录记录和会话策略"
	}
	switch code {
	case 0x10001, 0x1000b:
		return ended("服务器主动断开了会话")
	case 0x10002, 0x1000c:
		return ended("会话已被服务器注销")
	case 0x10003:
		return ended("会话空闲超时，服务器已断开连接")
	case 0x10004:
		return "error", "⏱️  远程 Windows 登录超时，请重新连接"
	case 0x10005:
		return ended("另一条连接接管了此远程会话")
	case 0x10007:
		return "error", "⚠️  服务器拒绝了远程桌面连接，请检查服务器的会话限制和访问策略"
	case 0x10009, 0x2000a, 0x20016, 0x2001a:
		return "error", "🔒  此账号没有远程桌面登录权限，请检查远程 Windows 的用户权限和策略"
	case 0x1000a:
		return "error", "🔑  服务器要求重新提供凭据，请重新输入密码后连接"
	case 0x20004, 0x20005:
		return "error", "🌐  无法解析服务器地址，请检查主机名及 DNS 设置"
	case 0x20006, 0x2000d:
		return "error", "🌐  无法建立或维持 RDP 网络连接，请检查服务器地址、端口、代理和远程桌面服务"
	case 0x20008:
		return "error", "🔒  TLS 安全连接未建立，请检查服务器证书确认和 TLS 设置"
	case 0x20009, 0x20014, 0x20015:
		return "error", "🔑  远程 Windows 身份验证失败，请检查用户名、密码和域"
	case 0x2000b:
		return "disconnected", "🔌  远程桌面连接已取消"
	case 0x2000c, 0x2001e:
		return "error", "🔒  RDP 安全协议协商失败，请检查服务器的 NLA 和安全设置"
	case 0x2000e, 0x2000f, 0x20013:
		return "error", "🔑  远程账号的密码已过期或必须更改，请先在远程 Windows 中修改密码"
	case 0x20012:
		return "error", "🔒  远程账号已被禁用，请联系服务器管理员"
	case 0x20017:
		return "error", "🔒  远程账号受到登录限制，请检查账号和登录策略"
	case 0x20018:
		return "error", "🔒  远程账号已被锁定，请解锁后再连接"
	case 0x20019:
		return "error", "🔒  远程账号已过期，请联系服务器管理员"
	case 0x2001b:
		return "error", "🔑  未提供可用的登录凭据，请填写用户名和密码"
	case 0x2001c:
		return "error", "⏱️  远程桌面启动超时，请稍后重新连接"
	case 0x2001d:
		return "error", "⏳  远程 Windows 正在启动，请稍后重新连接"
	default:
		if connected {
			return "error", "⚠️  远程桌面意外中断，请重新连接；若反复出现，请提供此退出码"
		}
		return "error", "⚠️  远程桌面未能建立连接；若重试后仍失败，请提供此退出码"
	}
}
