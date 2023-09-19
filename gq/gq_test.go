package gq_test

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	insecurerand "math/rand"

	"github.com/bastionzero/openpubkey/gq"
	"github.com/bastionzero/openpubkey/util"
)

func TestProveVerify(t *testing.T) {
	insecureRNG := insecurerand.New(insecurerand.NewSource(1))

	oidcPrivKey, err := rsa.GenerateKey(insecureRNG, 2048)
	if err != nil {
		panic(err)
	}

	oidcPubKey := &oidcPrivKey.PublicKey

	idToken := createOIDCToken(insecureRNG, oidcPrivKey, "test")

	identity, _, err := util.SplitDecodeJWTSignature(idToken)
	if err != nil {
		t.Fatal(err)
	}

	signerVerifier := gq.NewSignerVerifier(oidcPubKey, 256, insecureRNG)
	gqSig, err := signerVerifier.SignJWTIdentity(idToken)
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("gqSig: %s\n", gqSig)

	ok := signerVerifier.Verify(gqSig, identity)
	if !ok {
		t.Fatal("couldn't verify signature we just made")
	}
}

func createOIDCToken(rng io.Reader, oidcPrivKey *rsa.PrivateKey, audience string) []byte {
	oidcHeader := map[string]any{
		"alg": "RS256",
		"typ": "JWT",
	}
	oidcPayload := map[string]any{
		"sub": "1",
		"iss": "test",
		"aud": audience,
		"iat": time.Now().Unix(),
	}

	oidcHeaderJSON, err := json.Marshal(oidcHeader)
	if err != nil {
		panic(err)
	}
	oidcPayloadJSON, err := json.Marshal(oidcPayload)
	if err != nil {
		panic(err)
	}

	var buf bytes.Buffer
	buf.Write(util.Base64EncodeForJWT(oidcHeaderJSON))
	buf.WriteByte('.')
	buf.Write(util.Base64EncodeForJWT(oidcPayloadJSON))
	oidcSigningPayload := buf.Bytes()

	hash := sha256.Sum256(oidcSigningPayload)
	oidcSigRaw, err := rsa.SignPKCS1v15(rng, oidcPrivKey, crypto.SHA256, hash[:])
	if err != nil {
		panic(err)
	}

	oidcSig := util.Base64EncodeForJWT(oidcSigRaw)
	buf.WriteByte('.')
	buf.Write(oidcSig)

	return buf.Bytes()
}
