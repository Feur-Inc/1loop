// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

func signTestIDToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	encode := func(v any) string {
		data, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(data)
	}
	signed := encode(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + encode(claims)
	hash := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestValidateOpenAIID(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	openAIKeysOnce.Do(func() {}) // keep the network out
	saved := openAIKeys
	openAIKeys = &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}
	defer func() { openAIKeys = saved }()

	valid := func() map[string]any {
		return map[string]any{
			"iss": openAIIssuer, "sub": "user-1", "aud": []string{"client"},
			"email": "a@example.com", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		}
	}
	sub, email, err := validateOpenAIID(context.Background(), signTestIDToken(t, key, valid()), "client", "")
	if err != nil || sub != "user-1" || email != "a@example.com" {
		t.Fatalf("valid token: %q %q %v", sub, email, err)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	cases := map[string]string{"wrong key": signTestIDToken(t, other, valid())}
	for name, change := range map[string]func(map[string]any){
		"issuer":   func(c map[string]any) { c["iss"] = "https://evil.example" },
		"audience": func(c map[string]any) { c["aud"] = "someone-else" },
		"expired":  func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
		"subject":  func(c map[string]any) { delete(c, "sub") },
	} {
		claims := valid()
		change(claims)
		cases[name] = signTestIDToken(t, key, claims)
	}
	cases["garbage"] = "not.a.token"
	for name, token := range cases {
		if _, _, err := validateOpenAIID(context.Background(), token, "client", ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	claims := valid()
	claims["nonce"] = "n1"
	token := signTestIDToken(t, key, claims)
	if _, _, err := validateOpenAIID(context.Background(), token, "client", "n2"); err == nil {
		t.Error("wrong nonce accepted")
	}
	if _, _, err := validateOpenAIID(context.Background(), token, "client", "n1"); err != nil {
		t.Errorf("right nonce: %v", err)
	}
}
