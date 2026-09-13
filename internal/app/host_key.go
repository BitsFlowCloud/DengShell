package app

type HostKeyApproval struct {
	Host                string `json:"host"`
	Fingerprint         string `json:"fingerprint"`
	PreviousFingerprint string `json:"previousFingerprint"`
}

type HostKeyError struct {
	Host                string `json:"host"`
	Fingerprint         string `json:"fingerprint"`
	PreviousFingerprint string `json:"previousFingerprint"`
	Algorithm           string `json:"algorithm"`
}

func (e *HostKeyError) Code() string {
	if e.PreviousFingerprint != "" {
		return "ssh_host_key_changed"
	}
	return "ssh_host_key_unknown"
}

func (e *HostKeyError) Error() string {
	if e.PreviousFingerprint != "" {
		return "服务器主机指纹已变化；在核实服务器身份前，已停止 SSH 身份验证。"
	}
	return "首次连接此服务器，需要核实并确认 SSH 主机指纹。"
}
