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
	stdrsa "crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateRSAKey(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "generate rsa key",
			err:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateRSAKey()
			t.Logf("PrivateKey: %s", got.PrivateKey)
			t.Logf("PublicKey: %s", got.PublicKey)
			assert.Nil(t, err)
		})
	}
}

func TestEncryptToBase64(t *testing.T) {
	type args struct {
		plaintext []byte
	}
	tests := []struct {
		name string
		args args
		want string
		err  error
	}{
		{
			name: "encrypt to base64",
			args: args{
				plaintext: []byte("1234abcd!@#$"),
			},
		},
		{
			name: "encrypt lang text to base64",
			args: args{
				plaintext: []byte(`aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
				aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rasKey, err := GenerateRSAKey()
			assert.Nil(t, err)
			ciphertext, err := EncryptToBase64(tt.args.plaintext, rasKey.PublicKey)
			assert.Nil(t, err)
			plaintext, err := DecryptFromBase64(ciphertext, rasKey.PrivateKey)
			assert.Nil(t, err)
			assert.Equal(t, plaintext, tt.args.plaintext)
		})
	}
}

func TestEncryptToBase64WithPemPublicKeyWrappedInBase64(t *testing.T) {
	rsaKey, err := GenerateRSAKey()
	assert.NoError(t, err)
	priv := mustParsePrivateKey(t, rsaKey)
	plaintext := []byte("P123456789012345")
	pemText := pkixPemCRLF(&priv.PublicKey)
	wrapped := base64.StdEncoding.EncodeToString([]byte(pemText))
	ciphertext, err := EncryptToBase64(plaintext, wrapped)
	assert.NoError(t, err)
	got, err := DecryptFromBase64(ciphertext, rsaKey.PrivateKey)
	assert.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestEncryptToBase64WithRawPemPublicKey(t *testing.T) {
	rsaKey, err := GenerateRSAKey()
	assert.NoError(t, err)
	priv := mustParsePrivateKey(t, rsaKey)
	plaintext := []byte("P123456789012345")
	ciphertext, err := EncryptToBase64(plaintext, pkixPemCRLF(&priv.PublicKey))
	assert.NoError(t, err)
	got, err := DecryptFromBase64(ciphertext, rsaKey.PrivateKey)
	assert.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func TestEncryptToBase64WithPkixDerBase64(t *testing.T) {
	rsaKey, err := GenerateRSAKey()
	assert.NoError(t, err)
	priv := mustParsePrivateKey(t, rsaKey)
	spki, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	assert.NoError(t, err)
	plaintext := []byte("P123456789012345")
	ciphertext, err := EncryptToBase64(plaintext, base64.StdEncoding.EncodeToString(spki))
	assert.NoError(t, err)
	got, err := DecryptFromBase64(ciphertext, rsaKey.PrivateKey)
	assert.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

// 服务端 maintain 下发的 Base64(PEM) 样本（unknown tag 13 回归，对齐 polaris-java RSAUtilTest）。
func TestEncryptToBase64FromWatchClientEventsSample(t *testing.T) {
	publicKey := "LS0tLS1CRUdJTiBQVUJMSUMgS0VZLS0tLS0KTUlJQklqQU5CZ2txaGtpRzl3MEJBUUVGQUFPQ0FROEFNSUlCQ2dLQ0FRRUFzUXVDdEw1bWEzdXI0K3lnVWtFUApraUc0UnZaRVhkVldwNFg0blNmOG9lcmgvY0RZSjlXRVNLMThpRm1INGpNSmI5bGovWi9ua0s5Q2ZWTGV2T2lZCnp0eUdaSmtLQnhGQStSNjR3TUJFWFdzRTJCb2cyR0xodmdHTlBzUmF6emlqRUNhbC8vTTdaRCtDc1pPM01YY1cKenNsY2pSMjlpMFZQbmZMMWlGQWI3b3hJT1p0b0RjMTVvZklwZDN1VTlMSklicVM5KzEwWnAwTk1YRkRWYzdvegpCUU5pVnI3RGxjc0JxR0JNMnBLQmdVeWk0MERISEp1djJuWUhNdEFqb2R0OVdFelpKQmNidVVnVkVza3V4WGxUClJyRXhJRUVwK3l4T3ppbDhtS2gwbzNZejFkKzFPUlpDZlhOMHF2TEpsOFFFNDMrSmx6Smduc09UVlN5N1RjUkkKQXdJREFRQUIKLS0tLS1FTkQgUFVCTElDIEtFWS0tLS0tCg=="
	_, err := EncryptToBase64([]byte("P123456789012345"), publicKey)
	assert.NoError(t, err)
}

func mustParsePrivateKey(t *testing.T, rsaKey *RSAKey) *stdrsa.PrivateKey {
	t.Helper()
	der, err := base64.StdEncoding.DecodeString(rsaKey.PrivateKey)
	assert.NoError(t, err)
	priv, err := x509.ParsePKCS1PrivateKey(der)
	assert.NoError(t, err)
	return priv
}

func pkixPemCRLF(pub *stdrsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		panic(err)
	}
	body := base64.StdEncoding.EncodeToString(der)
	return "-----BEGIN PUBLIC KEY-----\r\n" + body + "\r\n-----END PUBLIC KEY-----\r\n"
}
