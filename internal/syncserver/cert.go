package syncserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type certificates struct {
	mu      sync.Mutex
	dir, ip string
	root    *x509.Certificate
	key     *ecdsa.PrivateKey
	leaf    *tls.Certificate
	PEM     string
}

func writePrivate(path string, data []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".sync-write-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(name, path)
}
func serial() *big.Int {
	n, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		panic(e)
	}
	return n
}
func openCertificates(dir, ip string) (*certificates, error) {
	if net.ParseIP(ip) == nil {
		return nil, errors.New("同步地址必须是 IP")
	}
	c := &certificates{dir: dir, ip: ip}
	path := filepath.Join(dir, "identity.pem")
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return nil, e
		}
		now := time.Now()
		template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "DengShell private sync identity"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if e != nil {
			return nil, e
		}
		pk, e := x509.MarshalECPrivateKey(key)
		if e != nil {
			return nil, e
		}
		b = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: pk})...)
		if e = writePrivate(path, b); e != nil {
			return nil, e
		}
	} else if e != nil {
		return nil, e
	}
	block, rest := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("同步证书已损坏，禁止自动替换身份")
	}
	c.root, e = x509.ParseCertificate(block.Bytes)
	if e != nil {
		return nil, e
	}
	c.PEM = string(pem.EncodeToMemory(block))
	block, _ = pem.Decode(rest)
	if block == nil {
		return nil, errors.New("同步身份私钥缺失")
	}
	c.key, e = x509.ParseECPrivateKey(block.Bytes)
	if e != nil {
		return nil, e
	}
	if !c.root.IsCA || !c.key.PublicKey.Equal(c.root.PublicKey) {
		return nil, errors.New("同步身份私钥不匹配")
	}
	_, e = c.get(nil)
	return c, e
}
func (c *certificates) get(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if now.Before(c.root.NotBefore) || !now.Before(c.root.NotAfter) {
		return nil, errors.New("同步根证书无效，需要通过可信设备重新绑定")
	}
	if c.leaf != nil && c.leaf.Leaf.NotAfter.After(now.Add(30*24*time.Hour)) {
		return c.leaf, nil
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	until := now.Add(90 * 24 * time.Hour)
	if until.After(c.root.NotAfter) {
		until = c.root.NotAfter
	}
	t := &x509.Certificate{SerialNumber: serial(), NotBefore: now.Add(-5 * time.Minute), NotAfter: until, IPAddresses: []net.IP{net.ParseIP(c.ip)}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, t, c.root, &key.PublicKey, c.key)
	if e != nil {
		return nil, e
	}
	leaf, e := x509.ParseCertificate(der)
	if e != nil {
		return nil, e
	}
	c.leaf = &tls.Certificate{Certificate: [][]byte{der, c.root.Raw}, PrivateKey: key, Leaf: leaf}
	return c.leaf, nil
}
