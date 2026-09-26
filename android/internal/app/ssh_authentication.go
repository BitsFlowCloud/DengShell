package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"
)

type AuthenticationError struct {
	code    string
	message string
}

func (e *AuthenticationError) Error() string { return e.message }
func (e *AuthenticationError) Code() string  { return e.code }

type authenticationTrace struct {
	allowed, tried, partial []string
}

func (trace *authenticationTrace) observe(state *ssh.ClientAuthContext) (ssh.AuthMethod, error) {
	trace.allowed = append([]string(nil), state.AllowedMethods...)
	trace.tried = append([]string(nil), state.TriedMethods...)
	trace.partial = append([]string(nil), state.PartialSuccessMethods...)
	return nil, nil // Preserve the library's offered-method negotiation.
}

func passwordChallenge(secret string) ssh.KeyboardInteractiveChallenge {
	answered := false
	return func(_ string, instruction string, questions []string, echo []bool) ([]string, error) {
		if len(questions) == 0 {
			return []string{}, nil
		}
		if len(questions) != 1 || len(echo) != 1 || echo[0] || !plainPasswordPrompt(questions[0], instruction) {
			return nil, &AuthenticationError{code: "ssh_authentication_unsupported", message: "服务器要求验证码、多项输入或其他交互认证，当前密码模式无法代填。请使用服务器允许的密钥认证，或支持该交互方式的客户端完成登录。"}
		}
		if answered {
			return nil, &AuthenticationError{code: "ssh_authentication_failed", message: "服务器再次要求密码，前一次自动提交未完成认证。请检查账号、密码和服务器登录策略后重试。"}
		}
		answered = true
		return []string{secret}, nil
	}
}

func plainPasswordPrompt(prompt, instruction string) bool {
	text := strings.ToLower(prompt + " " + instruction)
	for _, unsupported := range []string{"one-time", "one time", "otp", "verification", "authenticator", "token", "passcode", "验证码", "动态", "一次性", "new password", "change password", "expired", "新密码", "过期", "修改密码"} {
		if strings.Contains(text, unsupported) {
			return false
		}
	}
	prompt = strings.ToLower(prompt)
	return strings.Contains(prompt, "password") || strings.Contains(prompt, "密码") || strings.Contains(prompt, "口令")
}

func authenticationMethodNames(methods []string) string {
	labels := []string{}
	for _, method := range methods {
		switch method {
		case "password":
			labels = append(labels, "密码")
		case "keyboard-interactive":
			labels = append(labels, "交互认证")
		case "publickey":
			labels = append(labels, "公钥")
		case "gssapi-with-mic":
			labels = append(labels, "GSSAPI")
		}
	}
	if len(labels) == 0 {
		return "其他认证方式"
	}
	return strings.Join(labels, "、")
}

func explainSSHAuthentication(err error, auth string, trace authenticationTrace) error {
	var known *AuthenticationError
	if errors.As(err, &known) {
		return known
	}
	if !strings.Contains(err.Error(), "unable to authenticate, attempted methods") {
		return fmt.Errorf("SSH 握手未完成：%w", err)
	}
	if len(trace.partial) > 0 {
		return &AuthenticationError{code: "ssh_authentication_unsupported", message: "服务器已接受部分认证，但还要求" + authenticationMethodNames(trace.allowed) + "；当前单一认证模式不能完成此组合。请检查服务器认证策略。"}
	}
	attempted := slices.Contains(trace.tried, "password") || slices.Contains(trace.tried, "keyboard-interactive")
	if auth == "password" && !attempted {
		return &AuthenticationError{code: "ssh_authentication_unsupported", message: "服务器未接受使用当前密码认证方式继续登录，目前提供" + authenticationMethodNames(trace.allowed) + "。请检查账号允许的认证方式；这不能证明密码错误。"}
	}
	if auth == "password" {
		return &AuthenticationError{code: "ssh_authentication_failed", message: "SSH 身份验证未通过。可能是账号或密码不匹配、保存的凭据已过期、该账号（包括 root）被登录策略限制，或服务器要求其他认证。此返回无法确定具体原因，请重新输入凭据或检查服务器认证日志。"}
	}
	return &AuthenticationError{code: "ssh_authentication_failed", message: "SSH 公钥身份验证未通过。请检查用户名、服务器 authorized_keys、选用的私钥或 SSH Agent，以及服务器允许的认证方式；此返回无法确定具体原因。"}
}
