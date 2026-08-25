/*
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 *  under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 *
 */

package rsa

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"unicode"
)

// RSAKey RSA key pair
type RSAKey struct {
	PrivateKey string
	PublicKey  string
}

// GenerateKey generate RSA key pair
func GenerateRSAKey() (*RSAKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}
	rsaKey := &RSAKey{
		PrivateKey: base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PrivateKey(privateKey)),
		PublicKey:  base64.StdEncoding.EncodeToString(x509.MarshalPKCS1PublicKey(&privateKey.PublicKey)),
	}
	return rsaKey, nil
}

// Encrypt RSA encrypt plaintext using a PKCS1 or X.509 SPKI DER public key.
func Encrypt(plaintext, publicKey []byte) ([]byte, error) {
	pub, err := parsePkcs1OrX509(publicKey)
	if err != nil {
		return nil, err
	}
	return encryptWithPublicKey(plaintext, pub)
}

func encryptWithPublicKey(plaintext []byte, pub *rsa.PublicKey) ([]byte, error) {
	totalLen := len(plaintext)
	segLen := pub.Size() - 11
	start := 0
	buffer := bytes.Buffer{}
	for start < totalLen {
		end := start + segLen
		if end > totalLen {
			end = totalLen
		}
		seg, err := rsa.EncryptPKCS1v15(rand.Reader, pub, plaintext[start:end])
		if err != nil {
			return nil, err
		}
		buffer.Write(seg)
		start = end
	}
	return buffer.Bytes(), nil
}

// Decrypt RSA decrypt ciphertext using private key
func Decrypt(ciphertext, privateKey []byte) ([]byte, error) {
	priv, err := x509.ParsePKCS1PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	keySize := priv.Size()
	totalLen := len(ciphertext)
	start := 0
	buffer := bytes.Buffer{}
	for start < totalLen {
		end := start + keySize
		if end > totalLen {
			end = totalLen
		}
		seg, err := rsa.DecryptPKCS1v15(rand.Reader, priv, ciphertext[start:end])
		if err != nil {
			return nil, err
		}
		buffer.Write(seg)
		start = end
	}
	return buffer.Bytes(), nil
}

// EncryptToBase64 RSA encrypt plaintext and base64 encode ciphertext.
// encodedPublicKey 对齐 polaris-java RSAUtil：PKCS1 DER Base64、X.509 SPKI Base64、
// PEM（含 BEGIN PUBLIC KEY / BEGIN RSA PUBLIC KEY），以及 WatchClientEvents PUSH 的 Base64(PEM)。
func EncryptToBase64(plaintext []byte, encodedPublicKey string) (string, error) {
	pub, err := parseRsaPublicKey(encodedPublicKey)
	if err != nil {
		return "", err
	}
	ciphertext, err := encryptWithPublicKey(plaintext, pub)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// parseRsaPublicKey 解析 PKCS1 / X.509 / PEM 公钥（可再包一层 Base64）。
func parseRsaPublicKey(encodedPublicKey string) (*rsa.PublicKey, error) {
	material := unwrapToKeyMaterial(encodedPublicKey)
	if strings.Contains(material, "BEGIN") {
		block, _ := pem.Decode([]byte(material))
		if block == nil {
			return nil, errors.New("failed to decode PEM public key")
		}
		switch block.Type {
		case "RSA PUBLIC KEY":
			return x509.ParsePKCS1PublicKey(block.Bytes)
		case "PUBLIC KEY":
			return parsePKIXRSAPublicKey(block.Bytes)
		default:
			return parsePkcs1OrX509(block.Bytes)
		}
	}
	der, err := base64.StdEncoding.DecodeString(stripWhitespace(material))
	if err != nil {
		return nil, err
	}
	return parsePkcs1OrX509(der)
}

// unwrapToKeyMaterial 若外层是 Base64(PEM)，先解开得到 PEM 文本；否则保持原串。
func unwrapToKeyMaterial(encodedPublicKey string) string {
	material := strings.TrimSpace(encodedPublicKey)
	if strings.Contains(material, "BEGIN") {
		return material
	}
	decoded, err := base64.StdEncoding.DecodeString(material)
	if err != nil {
		return material
	}
	asText := strings.TrimSpace(string(decoded))
	if strings.HasPrefix(asText, "-----BEGIN") {
		return asText
	}
	return material
}

func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func parsePkcs1OrX509(der []byte) (*rsa.PublicKey, error) {
	pub, err := x509.ParsePKCS1PublicKey(der)
	if err == nil {
		return pub, nil
	}
	return parsePKIXRSAPublicKey(der)
}

func parsePKIXRSAPublicKey(der []byte) (*rsa.PublicKey, error) {
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("not an RSA public key")
	}
	return rsaPub, nil
}

// DecryptFromBase64 base64 decode ciphertext and RSA decrypt
func DecryptFromBase64(base64Ciphertext, base64PrivateKey string) ([]byte, error) {
	priv, err := base64.StdEncoding.DecodeString(base64PrivateKey)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(base64Ciphertext)
	if err != nil {
		return nil, err
	}
	return Decrypt(ciphertext, priv)
}
