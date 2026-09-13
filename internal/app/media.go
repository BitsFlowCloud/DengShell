package app

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxFontBytes = 64 << 20
const maxBackgroundBytes = 32 << 20

type ManagedAsset struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	MIMEType  string    `json:"mimeType"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}

type AssetContent struct {
	Asset   ManagedAsset `json:"asset"`
	DataURL string       `json:"dataUrl"`
}

func assetLimit(kind string) (int64, error) {
	switch kind {
	case "font":
		return maxFontBytes, nil
	case "background":
		return maxBackgroundBytes, nil
	default:
		return 0, errors.New("资源类型应为字体或背景")
	}
}

func validAssetID(id string) bool {
	if len(id) != 48 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func cleanAssetName(name, filename string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		base := filepath.Base(filename)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if name == "" || name == "." || len(name) > 160 {
		return "", errors.New("请填写资源名称（最多 160 字节）")
	}
	return name, nil
}

func (a *App) ImportLocalAsset(kind, path, name string) (ManagedAsset, error) {
	limit, err := assetLimit(kind)
	if err != nil {
		return ManagedAsset{}, err
	}
	f, err := os.Open(expandHome(path))
	if err != nil {
		return ManagedAsset{}, fmt.Errorf("读取资源：%w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ManagedAsset{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return ManagedAsset{}, fmt.Errorf("请选择不超过 %d MB 的普通文件", limit>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return ManagedAsset{}, err
	}
	return a.store.ImportAsset(kind, filepath.Base(path), name, data)
}

func (s *Store) ImportAsset(kind, filename, name string, data []byte) (ManagedAsset, error) {
	limit, err := assetLimit(kind)
	if err != nil {
		return ManagedAsset{}, err
	}
	if len(data) == 0 || int64(len(data)) > limit {
		return ManagedAsset{}, fmt.Errorf("文件不能为空或超过 %d MB", limit>>20)
	}
	name, err = cleanAssetName(name, filename)
	if err != nil {
		return ManagedAsset{}, err
	}
	mime, err := validateAsset(kind, filename, data)
	if err != nil {
		return ManagedAsset{}, err
	}
	asset := ManagedAsset{ID: randomID(), Name: name, Kind: kind, MIMEType: mime, Size: int64(len(data)), CreatedAt: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.dir, "assets")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ManagedAsset{}, err
	}
	path := filepath.Join(dir, asset.ID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ManagedAsset{}, err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return ManagedAsset{}, err
	}
	old := s.config.Assets
	s.config.Assets = append(append([]ManagedAsset{}, old...), asset)
	if err := s.writeLocked(); err != nil {
		s.config.Assets = old
		os.Remove(path)
		return ManagedAsset{}, err
	}
	return asset, nil
}

func (s *Store) AssetData(id string) (AssetContent, error) {
	if !validAssetID(id) {
		return AssetContent{}, errors.New("资源不存在")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, asset := range s.config.Assets {
		if asset.ID != id {
			continue
		}
		limit, err := assetLimit(asset.Kind)
		if err != nil {
			return AssetContent{}, err
		}
		f, err := os.Open(filepath.Join(s.dir, "assets", id))
		if err != nil {
			return AssetContent{}, fmt.Errorf("资源副本无法读取，请重新导入：%w", err)
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, limit+1))
		if err != nil {
			return AssetContent{}, err
		}
		if len(data) == 0 || int64(len(data)) > limit {
			return AssetContent{}, errors.New("资源文件大小无效，请重新导入")
		}
		return AssetContent{Asset: asset, DataURL: "data:" + asset.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(data)}, nil
	}
	return AssetContent{}, errors.New("资源不存在")
}

func (s *Store) RenameAsset(id, name string) (ManagedAsset, error) {
	name, err := cleanAssetName(name, "")
	if err != nil {
		return ManagedAsset{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, asset := range s.config.Assets {
		if asset.ID != id {
			continue
		}
		updated := asset
		updated.Name = name
		s.config.Assets[i] = updated
		if err := s.writeLocked(); err != nil {
			s.config.Assets[i] = asset
			return ManagedAsset{}, err
		}
		return updated, nil
	}
	return ManagedAsset{}, errors.New("资源不存在")
}

func (s *Store) DeleteAsset(id string) error {
	if !validAssetID(id) {
		return errors.New("资源不存在")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := []ManagedAsset{}
	found := false
	for _, asset := range s.config.Assets {
		if asset.ID == id {
			found = true
		} else {
			next = append(next, asset)
		}
	}
	if !found {
		return errors.New("资源不存在")
	}
	path := filepath.Join(s.dir, "assets", id)
	removedPath := path + ".removing"
	removed := true
	if err := os.Rename(path, removedPath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		removed = false
	}
	oldAssets, oldAppearance := s.config.Assets, s.config.Appearance
	s.config.Assets = next
	s.config.Appearance = cloneAppearance(s.config.Appearance)
	delete(s.config.Appearance.FontColors, id)
	delete(s.config.Appearance.FontBold, id)
	if s.config.Appearance.FontID == id {
		s.config.Appearance.FontID = defaultAppearance().FontID
	}
	if s.config.Appearance.BackgroundID == id {
		s.config.Appearance.BackgroundID = defaultAppearance().BackgroundID
	}
	if err := s.writeLocked(); err != nil {
		s.config.Assets, s.config.Appearance = oldAssets, oldAppearance
		if removed {
			if restoreErr := os.Rename(removedPath, path); restoreErr != nil {
				return fmt.Errorf("保存失败（%v），资源副本回滚失败：%w", err, restoreErr)
			}
		}
		return err
	}
	if removed {
		if err := os.Remove(removedPath); err != nil {
			return fmt.Errorf("资源记录已删除，但清理副本失败：%w", err)
		}
	}
	return nil
}

func (a *App) importAssetHTTP(w http.ResponseWriter, r *http.Request) {
	// Base64 adds one third to file size. Other API requests retain their 2 MB cap.
	r.Body = http.MaxBytesReader(w, r.Body, 90<<20)
	var input struct {
		Kind     string `json:"kind"`
		Filename string `json:"filename"`
		Name     string `json:"name"`
		Data     string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, 400, errors.New("文件导入请求无效或过大"))
		return
	}
	limit, err := assetLimit(input.Kind)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if strings.HasPrefix(input.Data, "data:") {
		_, content, ok := strings.Cut(input.Data, ";base64,")
		if !ok {
			writeError(w, 400, errors.New("文件内容应使用 Base64 编码"))
			return
		}
		input.Data = content
	}
	if int64(len(input.Data)) > (limit+2)/3*4 {
		writeError(w, 400, fmt.Errorf("文件不能超过 %d MB", limit>>20))
		return
	}
	data, err := base64.StdEncoding.DecodeString(input.Data)
	if err != nil {
		writeError(w, 400, errors.New("文件内容应使用 Base64 编码"))
		return
	}
	value, err := a.store.ImportAsset(input.Kind, input.Filename, input.Name, data)
	respond(w, value, err)
}

func validateAsset(kind, filename string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if kind == "font" {
		if ext != ".ttf" && ext != ".otf" && ext != ".woff" && ext != ".woff2" {
			return "", errors.New("字体支持 TTF、OTF、WOFF、WOFF2 文件")
		}
		mime, err := validateFont(data)
		if err != nil {
			return "", fmt.Errorf("字体文件无效：%w", err)
		}
		return mime, nil
	}
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".webp" {
		return "", errors.New("背景支持 PNG、JPEG、WebP 图片")
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		if err := validateWebP(data); err != nil {
			return "", err
		}
		return "image/webp", nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return "", errors.New("图片无法读取，请选择有效的 PNG、JPEG 或 WebP 文件")
	}
	if err := validateImageSize(cfg.Width, cfg.Height); err != nil {
		return "", err
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", errors.New("图片内容不完整或损坏")
	}
	return "image/" + format, nil
}

func validateImageSize(width, height int) error {
	if width < 1 || height < 1 || width > 32768 || height > 32768 || int64(width)*int64(height) > 32_000_000 {
		return errors.New("背景图片不能超过 3200 万像素或单边 32768 像素")
	}
	return nil
}

func validateWebP(data []byte) error {
	if uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		return errors.New("WebP 图片长度无效")
	}
	found := false
	for offset := 12; offset < len(data); {
		if len(data)-offset < 8 {
			return errors.New("WebP 图片内容不完整")
		}
		tag := string(data[offset : offset+4])
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start, end := uint64(offset+8), uint64(offset+8)+size
		if end > uint64(len(data)) {
			return errors.New("WebP 图片内容不完整")
		}
		chunk := data[start:end]
		width, height := 0, 0
		switch tag {
		case "VP8 ":
			if len(chunk) < 10 || !bytes.Equal(chunk[3:6], []byte{0x9d, 0x01, 0x2a}) {
				return errors.New("WebP 图片帧无效")
			}
			width = int(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff)
			height = int(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff)
			found = true
		case "VP8L":
			if len(chunk) < 5 || chunk[0] != 0x2f {
				return errors.New("WebP 图片帧无效")
			}
			bits := binary.LittleEndian.Uint32(chunk[1:5])
			width, height = int(bits&0x3fff)+1, int((bits>>14)&0x3fff)+1
			found = true
		case "VP8X":
			if len(chunk) != 10 {
				return errors.New("WebP 图片头无效")
			}
			width = int(chunk[4]) | int(chunk[5])<<8 | int(chunk[6])<<16
			height = int(chunk[7]) | int(chunk[8])<<8 | int(chunk[9])<<16
			width++
			height++
		case "ANIM", "ANMF":
			return errors.New("请使用静态 WebP 背景图片")
		}
		if width != 0 || height != 0 {
			if err := validateImageSize(width, height); err != nil {
				return err
			}
		}
		offset = int(end + size%2)
		if offset > len(data) {
			return errors.New("WebP 图片内容不完整")
		}
	}
	if !found {
		return errors.New("WebP 图片缺少图像内容")
	}
	return nil
}

func validateFont(data []byte) (string, error) {
	invalid := errors.New("文件头或字体表损坏")
	if len(data) < 12 {
		return "", invalid
	}
	switch string(data[:4]) {
	case "\x00\x01\x00\x00", "OTTO", "true":
		count := int(binary.BigEndian.Uint16(data[4:6]))
		if count == 0 || count > 4096 || len(data) < 12+16*count {
			return "", invalid
		}
		tables := map[string]bool{}
		for i := 0; i < count; i++ {
			entry := data[12+i*16 : 12+(i+1)*16]
			offset, length := uint64(binary.BigEndian.Uint32(entry[8:12])), uint64(binary.BigEndian.Uint32(entry[12:16]))
			if offset < uint64(12+16*count) || offset+length > uint64(len(data)) || tables[string(entry[:4])] {
				return "", invalid
			}
			tables[string(entry[:4])] = true
		}
		for _, tag := range []string{"cmap", "head", "hhea", "hmtx", "maxp"} {
			if !tables[tag] {
				return "", errors.New("字体缺少必要的字符映射或度量表")
			}
		}
		if string(data[:4]) == "OTTO" {
			return "font/otf", nil
		}
		return "font/ttf", nil
	case "wOFF":
		if len(data) < 44 || uint64(binary.BigEndian.Uint32(data[8:12])) != uint64(len(data)) {
			return "", invalid
		}
		count := int(binary.BigEndian.Uint16(data[12:14]))
		if count == 0 || count > 4096 || len(data) < 44+20*count || binary.BigEndian.Uint32(data[16:20]) > maxFontBytes {
			return "", invalid
		}
		for i := 0; i < count; i++ {
			entry := data[44+i*20 : 44+(i+1)*20]
			offset, length, original := uint64(binary.BigEndian.Uint32(entry[4:8])), uint64(binary.BigEndian.Uint32(entry[8:12])), uint64(binary.BigEndian.Uint32(entry[12:16]))
			if offset < uint64(44+20*count) || offset+length > uint64(len(data)) || length > original || original > maxFontBytes {
				return "", invalid
			}
		}
		return "font/woff", nil
	case "wOF2":
		if len(data) < 48 || uint64(binary.BigEndian.Uint32(data[8:12])) != uint64(len(data)) {
			return "", invalid
		}
		count := binary.BigEndian.Uint16(data[12:14])
		compressed := uint64(binary.BigEndian.Uint32(data[20:24]))
		if count == 0 || count > 4096 || binary.BigEndian.Uint16(data[14:16]) != 0 || binary.BigEndian.Uint32(data[16:20]) > maxFontBytes || compressed == 0 || compressed+48 > uint64(len(data)) {
			return "", invalid
		}
		return "font/woff2", nil
	}
	return "", errors.New("不支持此字体格式")
}
