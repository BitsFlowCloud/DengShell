package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const finalShellFolder = "finalshell_oot_pot"
const finalShellMaxFile = 1 << 20
const finalShellMaxTotal = 32 << 20
const finalShellMaxFiles = 2000

type finalShellConnection struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	Description      string                     `json:"description"`
	Host             string                     `json:"host"`
	Port             int                        `json:"port"`
	User             string                     `json:"user_name"`
	Auth             int                        `json:"authentication_type"`
	Type             int                        `json:"conection_type"`
	Password         string                     `json:"password"`
	SecretKeyID      string                     `json:"secret_key_id"`
	ProxyID          string                     `json:"proxy_id"`
	Encoding         string                     `json:"terminal_encoding"`
	Deleted          int64                      `json:"delete_time"`
	Forwarding       []json.RawMessage          `json:"port_forwarding_list"`
	RemoteForwarding map[string]json.RawMessage `json:"remote_port_forwarding"`
}
type FinalShellImportItem struct {
	File          string `json:"file"`
	Name          string `json:"name,omitempty"`
	ProfileID     string `json:"profileId,omitempty"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	NeedsKey      bool   `json:"needsKey,omitempty"`
	NeedsPassword bool   `json:"needsPassword,omitempty"`
	NeedsProxy    bool   `json:"needsProxy,omitempty"`
}
type FinalShellImportResult struct {
	Directory      string                 `json:"directory"`
	Found          bool                   `json:"found"`
	Imported       int                    `json:"imported"`
	Updated        int                    `json:"updated"`
	KeysImported   int                    `json:"keysImported"`
	KeysSkipped    int                    `json:"keysSkipped"`
	KeysFailed     int                    `json:"keysFailed"`
	KeysAssociated int                    `json:"keysAssociated"`
	KeyItems       []FinalShellKeyItem    `json:"keyItems"`
	Skipped        int                    `json:"skipped"`
	Failed         int                    `json:"failed"`
	NeedsKey       int                    `json:"needsKey"`
	NeedsPassword  int                    `json:"needsPassword"`
	NeedsProxy     int                    `json:"needsProxy"`
	Items          []FinalShellImportItem `json:"items"`
}
type finalShellCandidate struct {
	profile      Profile
	keyReference string
	folders      []string
	item         int
}

func finalShellImportDirectory() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", errors.New("无法确定 DengShell 程序所在目录")
	}
	return filepath.Join(filepath.Dir(executable), finalShellFolder), nil
}
func (a *App) registerFinalShellImportHTTP(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/imports/finalshell", func(w http.ResponseWriter, r *http.Request) {
		directory, err := finalShellImportDirectory()
		respond(w, map[string]string{"directory": directory, "folderName": finalShellFolder}, err)
	})
	mux.HandleFunc("POST /api/imports/finalshell", func(w http.ResponseWriter, r *http.Request) {
		// The request cannot choose a local path or an external decoder program.
		directory, err := finalShellImportDirectory()
		if err != nil {
			respond(w, nil, err)
			return
		}
		result, err := a.store.importFinalShell(r.Context(), directory)
		respond(w, result, err)
	})
}

func parseFinalShellProfile(data []byte) (Profile, string, error) {
	var input finalShellConnection
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if json.Unmarshal(data, &input) != nil {
		return Profile{}, "", errors.New("JSON 格式或字段类型无效")
	}
	if input.Type != 100 {
		return Profile{}, "", errors.New("仅支持 FinalShell SSH 连接（类型 100）")
	}
	if input.Deleted != 0 {
		return Profile{}, "", errors.New("已删除的 FinalShell 连接不导入")
	}
	if encoding := strings.ToLower(strings.ReplaceAll(input.Encoding, "-", "")); encoding != "" && encoding != "utf8" {
		return Profile{}, "", errors.New("当前仅支持 UTF-8 终端连接，请调整 FinalShell 编码后重新导出")
	}
	p := Profile{Name: strings.TrimSpace(input.Name), Host: strings.Trim(strings.TrimSpace(input.Host), "[]"), User: strings.TrimSpace(input.User), Port: input.Port, Proxy: ProxyConfig{Type: "direct"}}
	p.Notes = input.Description
	if err := validateProfileNotes(p.Notes); err != nil {
		return Profile{}, "", err
	}
	if p.Name == "" || len(p.Name) > 100 || p.Host == "" || len(p.Host) > 253 || strings.ContainsAny(p.Host, " /\r\n\t\x00") || p.User == "" || len(p.User) > 256 || strings.ContainsAny(p.User, "\r\n\x00") || p.Port < 1 || p.Port > 65535 {
		return Profile{}, "", errors.New("名称、地址、端口或用户名无效")
	}
	message := ""
	switch input.Auth {
	case 1:
		p.Auth = "password"
		secret, err := decodeFinalShellPassword(input.Password)
		if err != nil {
			message = err.Error()
		} else if secret == "" {
			message = "导出文件未保存密码，请在连接设置中补充"
		} else {
			p.Secret = secret
		}
	case 2:
		// secret_key_id is an identifier, not private-key material. The password
		// field can be a leftover account password and must not become a key passphrase.
		p.Auth = "key"
		message = "未找到匹配私钥；连接时将提示重新配置密钥"
	default:
		return Profile{}, "", errors.New("不支持此认证方式，请手动建立连接")
	}
	if input.ProxyID != "" && input.ProxyID != "0" && input.ProxyID != "-1" {
		p.NeedsProxy = true
		message += "；原连接使用代理，请在编辑连接中补充代理（或确认改为直连）后再连接"
	}
	identity := input.ID
	if identity == "" {
		identity = fmt.Sprintf("%s\x00%s\x00%d\x00%s\x00%s", p.Name, p.Host, p.Port, p.User, p.Auth)
	}
	if len(identity) > 1024 || len(input.SecretKeyID) > 1024 {
		return Profile{}, "", errors.New("连接标识过长")
	}
	digest := sha256.Sum256([]byte(identity))
	p.FinalShellID = hex.EncodeToString(digest[:])
	if len(input.Forwarding) > 0 || len(input.RemoteForwarding) > 0 {
		message += "；端口转发规则未导入"
	}
	return p, strings.TrimPrefix(message, "；"), nil
}

func (s *Store) importFinalShell(ctx context.Context, directory string) (FinalShellImportResult, error) {
	result := FinalShellImportResult{Directory: directory, Items: []FinalShellImportItem{}, KeyItems: []FinalShellKeyItem{}}
	info, err := os.Lstat(directory)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("导入目录不可读取或不是普通文件夹")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return result, errors.New("无法打开导入文件夹")
	}
	defer root.Close()
	result.Found = true
	candidates := []finalShellCandidate{}
	entries, total := 0, 0
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > 10000 {
			return errors.New("导入文件过多，请分批放入文件夹（每批最多 2000 个连接）")
		}
		if walkErr != nil {
			return errors.New("部分导入目录无法读取，请检查权限后重试")
		}
		if entry.IsDir() {
			if name == "key" {
				return fs.SkipDir
			}
			if strings.Count(name, "/") > 16 {
				return errors.New("导入目录层级过深")
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), "_connect_config.json") {
			return nil
		}
		if len(result.Items) >= finalShellMaxFiles {
			return errors.New("每批最多导入 2000 个连接，请分批导入")
		}
		item := FinalShellImportItem{File: name, Status: "failed"}
		appendFailure := func(message string) {
			item.Message = message
			result.Items = append(result.Items, item)
			result.Failed++
		}
		if !entry.Type().IsRegular() {
			appendFailure("只读取普通 JSON 文件，不跟随符号链接")
			return nil
		}
		f, err := root.Open(name)
		if err != nil {
			appendFailure("无法读取文件")
			return nil
		}
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > finalShellMaxFile {
			f.Close()
			appendFailure("文件不是普通文件或超过 1 MiB")
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(f, finalShellMaxFile+1))
		f.Close()
		if err != nil || len(data) > finalShellMaxFile {
			appendFailure("读取失败或文件超过 1 MiB")
			return nil
		}
		total += len(data)
		if total > finalShellMaxTotal {
			return errors.New("本批导入数据超过 32 MiB，请分批导入")
		}
		p, message, err := parseFinalShellProfile(data)
		var reference struct {
			ID string `json:"secret_key_id"`
		}
		if err == nil {
			_ = json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &reference)
		}
		clear(data)
		if err != nil {
			appendFailure(err.Error())
			return nil
		}
		item.Name = p.Name
		item.Message = message
		item.Status = "pending"
		item.NeedsProxy = p.NeedsProxy
		item.NeedsKey = p.Auth == "key"
		item.NeedsPassword = p.Auth == "password" && p.Secret == ""
		folders := []string{"FinalShell 导入"}
		if dir := path.Dir(name); dir != "." {
			for _, folder := range strings.Split(dir, "/") {
				if strings.TrimSpace(folder) == "" || len(folder) > 100 {
					appendFailure("目录名称为空或过长")
					return nil
				}
				folders = append(folders, folder)
			}
		}
		candidates = append(candidates, finalShellCandidate{profile: p, keyReference: strings.TrimSpace(reference.ID), folders: folders, item: len(result.Items)})
		result.Items = append(result.Items, item)
		return nil
	})
	if err != nil {
		return FinalShellImportResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return FinalShellImportResult{}, err
	}
	keys, err := readFinalShellKeys(ctx, root, &result, &total)
	if err != nil {
		return FinalShellImportResult{}, err
	}
	defer func() {
		for _, key := range keys {
			clear(key.data)
		}
	}()
	if len(candidates) == 0 && len(keys) == 0 {
		return result, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := cloneConnectionConfig(s.config)
	old.Keys = append([]ManagedKey{}, s.config.Keys...)
	var created []string
	committed := false
	defer func() {
		if !committed {
			s.config = old
			for _, filename := range created {
				_ = os.Remove(filename)
			}
		}
	}()
	matched, err := s.importFinalShellKeysLocked(keys, &result, &created)
	if err != nil {
		return FinalShellImportResult{}, err
	}
	for _, candidate := range candidates {
		if err = ctx.Err(); err != nil {
			return FinalShellImportResult{}, err
		}
		p := candidate.profile
		item := &result.Items[candidate.item]
		if p.Auth == "key" && candidate.keyReference != "" {
			p.KeyID = matched[candidate.keyReference]
		}
		var existing *Profile
		for i := range s.config.Servers {
			other := &s.config.Servers[i]
			if other.FinalShellID == p.FinalShellID || (other.Name == p.Name && strings.EqualFold(other.Host, p.Host) && other.Port == p.Port && other.User == p.User && other.Auth == p.Auth) {
				existing = other
				break
			}
		}
		if existing != nil {
			item.Status = "skipped"
			item.ProfileID = existing.ID
			item.NeedsProxy = existing.NeedsProxy
			if item.NeedsProxy {
				result.NeedsProxy++
			}
			item.NeedsKey = false
			item.NeedsPassword = false
			item.Message = "已存在，保留 DengShell 中的配置和凭据"
			if existing.DeletedAt != nil {
				item.ProfileID = ""
				item.Message = "已在回收站，不重复导入；可在已删除列表中恢复"
			} else {
				item.NeedsKey = existing.Auth == "key" && existing.KeyID == "" && existing.KeyPath == ""
				item.NeedsPassword = existing.Auth == "password" && existing.Secret == ""
				if item.NeedsKey && existing.FinalShellID == p.FinalShellID && p.KeyID != "" {
					existing.KeyID = p.KeyID
					item.NeedsKey = false
					item.Status = "updated"
					item.Message = "已自动关联私钥，保留其他连接设置"
					result.Updated++
					result.KeysAssociated++
				}
				if item.NeedsKey {
					result.NeedsKey++
					item.Message += "；连接时将提示重新配置密钥"
				}
				if item.NeedsPassword {
					result.NeedsPassword++
					item.Message += "；仍需补充密码"
				}
			}
			if item.Status == "skipped" {
				result.Skipped++
			}
			continue
		}
		parent := ""
		for _, name := range candidate.folders {
			id := ""
			for _, group := range s.config.GroupNodes {
				if group.ParentID == parent && group.Name == name {
					id = group.ID
					break
				}
			}
			if id == "" {
				id = randomID()
				s.config.GroupNodes = append(s.config.GroupNodes, ServerGroup{ID: id, Name: name, ParentID: parent})
			}
			parent = id
		}
		if p.Auth == "key" && p.KeyID != "" {
			item.NeedsKey = false
			item.Message = strings.Replace(item.Message, "未找到匹配私钥；连接时将提示重新配置密钥", "私钥已自动导入密钥管理器并关联", 1)
			result.KeysAssociated++
		}
		p.ID = randomID()
		p.GroupID = parent
		s.config.Servers = append(s.config.Servers, p)
		item.ProfileID = p.ID
		item.Status = "imported"
		result.Imported++
		if item.NeedsProxy {
			result.NeedsProxy++
		}
		if item.NeedsKey {
			result.NeedsKey++
		}
		if item.NeedsPassword {
			result.NeedsPassword++
		}
		if item.Message == "" {
			item.Message = "已导入，密码已保存在本机加密配置中"
		}
	}
	if result.Imported > 0 || result.Updated > 0 || result.KeysImported > 0 {
		s.refreshLegacyGroupsLocked()
		if err = ctx.Err(); err != nil {
			return FinalShellImportResult{}, err
		}
		if err = s.writeLocked(); err != nil {
			return FinalShellImportResult{}, err
		}
	}
	committed = true
	return result, nil
}
