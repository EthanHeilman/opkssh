// Copyright 2025 OpenPubkey
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package commands

import (
	"context"
	"crypto"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/openpubkey/openpubkey/client"
	"github.com/openpubkey/openpubkey/oidc"
	"github.com/openpubkey/openpubkey/pktoken"
	"github.com/openpubkey/openpubkey/providers"
	"github.com/openpubkey/openpubkey/util"
	"github.com/openpubkey/opkssh/sshcert"
	"golang.org/x/crypto/ssh"
)

type loginResult struct {
	pkt        *pktoken.PKToken
	signer     crypto.Signer
	alg        jwa.SignatureAlgorithm
	client     *client.OpkClient
	principals []string
}

func login(ctx context.Context, provider client.OpenIdProvider) (*loginResult, error) {
	var err error
	alg := jwa.ES256
	signer, err := util.GenKeyPair(alg)
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %w", err)
	}

	opkClient, err := client.New(provider, client.WithSigner(signer, alg))
	if err != nil {
		return nil, err
	}

	pkt, err := opkClient.Auth(ctx)
	if err != nil {
		return nil, err
	}

	// If principals is empty the server does not enforce any principal. The OPK
	// verifier should use policy to make this decision.
	principals := []string{}
	certBytes, seckeySshPem, err := createSSHCert(pkt, signer, principals)
	if err != nil {
		return nil, fmt.Errorf("failed to generate SSH cert: %w", err)
	}

	// Write ssh secret key and public key to filesystem
	if err := writeKeysToSSHDir(seckeySshPem, certBytes); err != nil {
		return nil, fmt.Errorf("failed to write SSH keys to filesystem: %w", err)
	}

	idStr, err := IdentityString(*pkt)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ID Token: %w", err)
	}
	fmt.Printf("Keys generated for identity\n%s\n", idStr)

	return &loginResult{
		pkt:        pkt,
		signer:     signer,
		client:     opkClient,
		alg:        alg,
		principals: principals,
	}, nil
}

// Login performs the OIDC login procedure and creates the SSH certs/keys in the
// default SSH key location.
func Login(ctx context.Context, provider client.OpenIdProvider) error {
	_, err := login(ctx, provider)
	return err
}

// LoginWithRefresh performs the OIDC login procedure, creates the SSH
// certs/keys in the default SSH key location, and continues to run and refresh
// the PKT (and create new SSH certs) indefinitely as its token expires. This
// function only returns if it encounters an error or if the supplied context is
// cancelled.
func LoginWithRefresh(ctx context.Context, provider providers.RefreshableOpenIdProvider) error {
	if loginResult, err := login(ctx, provider); err != nil {
		return err
	} else {
		var claims struct {
			Expiration int64 `json:"exp"`
		}
		if err := json.Unmarshal(loginResult.pkt.Payload, &claims); err != nil {
			return err
		}

		for {
			// Sleep until a minute before expiration to give us time to refresh
			// the token and minimize any interruptions
			untilExpired := time.Until(time.Unix(claims.Expiration, 0)) - time.Minute
			log.Printf("Waiting for %v before attempting to refresh id_token...", untilExpired)
			select {
			case <-time.After(untilExpired):
				log.Print("Refreshing id_token...")
			case <-ctx.Done():
				return ctx.Err()
			}

			refreshedPkt, err := loginResult.client.Refresh(ctx)
			if err != nil {
				return err
			}
			loginResult.pkt = refreshedPkt

			certBytes, seckeySshPem, err := createSSHCert(loginResult.pkt, loginResult.signer, loginResult.principals)
			if err != nil {
				return fmt.Errorf("failed to generate SSH cert: %w", err)
			}

			// Write ssh secret key and public key to filesystem
			if err := writeKeysToSSHDir(seckeySshPem, certBytes); err != nil {
				return fmt.Errorf("failed to write SSH keys to filesystem: %w", err)
			}

			comPkt, err := refreshedPkt.Compact()
			if err != nil {
				return err
			}

			_, payloadB64, _, err := jws.SplitCompactString(string(comPkt))
			if err != nil {
				return fmt.Errorf("malformed ID token: %w", err)
			}
			payload, err := base64.RawURLEncoding.DecodeString(string(payloadB64))
			if err != nil {
				return fmt.Errorf("refreshed ID token payload is not base64 encoded: %w", err)
			}

			if err = json.Unmarshal(payload, &claims); err != nil {
				return fmt.Errorf("malformed refreshed ID token payload: %w", err)
			}
		}
	}
}

func createSSHCert(pkt *pktoken.PKToken, signer crypto.Signer, principals []string) ([]byte, []byte, error) {
	cert, err := sshcert.New(pkt, principals)
	if err != nil {
		return nil, nil, err
	}
	sshSigner, err := ssh.NewSignerFromSigner(signer)
	if err != nil {
		return nil, nil, err
	}

	signerMas, err := ssh.NewSignerWithAlgorithms(sshSigner.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoECDSA256})
	if err != nil {
		return nil, nil, err
	}

	sshCert, err := cert.SignCert(signerMas)
	if err != nil {
		return nil, nil, err
	}
	certBytes := ssh.MarshalAuthorizedKey(sshCert)
	// Remove newline character that MarshalAuthorizedKey() adds
	certBytes = certBytes[:len(certBytes)-1]

	seckeySsh, err := ssh.MarshalPrivateKey(signer, "openpubkey cert")
	if err != nil {
		return nil, nil, err
	}
	seckeySshBytes := pem.EncodeToMemory(seckeySsh)

	return certBytes, seckeySshBytes, nil
}

func writeKeysToSSHDir(seckeySshPem []byte, certBytes []byte) error {
	homePath, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sshPath := filepath.Join(homePath, ".ssh")

	// Make ~/.ssh if folder does not exist
	err = os.MkdirAll(sshPath, os.ModePerm)
	if err != nil {
		return err
	}

	// For ssh to automatically find the key created by openpubkey when
	// connecting, we use one of the default ssh key paths. However, the file
	// might contain an existing key. We will overwrite the key if it was
	// generated by openpubkey  which we check by looking at the associated
	// comment. If the comment is equal to "openpubkey", we overwrite the file
	// with a new key.
	for _, keyFilename := range []string{"id_ecdsa", "id_ed25519"} {
		seckeyPath := filepath.Join(sshPath, keyFilename)
		pubkeyPath := seckeyPath + ".pub"

		if !fileExists(seckeyPath) {
			// If ssh key file does not currently exist, we don't have to worry about overwriting it
			return writeKeys(seckeyPath, pubkeyPath, seckeySshPem, certBytes)
		} else if !fileExists(pubkeyPath) {
			continue
		} else {
			// If the ssh key file does exist, check if it was generated by openpubkey, if it was then it is safe to overwrite
			sshPubkey, err := os.ReadFile(pubkeyPath)
			if err != nil {
				log.Println("Failed to read:", pubkeyPath)
				continue
			}
			_, comment, _, _, err := ssh.ParseAuthorizedKey(sshPubkey)
			if err != nil {
				log.Println("Failed to parse:", pubkeyPath)
				continue
			}

			// If the key comment is "openpubkey" then we generated it
			if comment == "openpubkey" {
				return writeKeys(seckeyPath, pubkeyPath, seckeySshPem, certBytes)
			}
		}
	}
	return fmt.Errorf("no default ssh key file free for openpubkey")
}

func writeKeys(seckeyPath string, pubkeyPath string, seckeySshPem []byte, certBytes []byte) error {
	// Write ssh secret key to filesystem
	if err := os.WriteFile(seckeyPath, seckeySshPem, 0600); err != nil {
		return err
	}

	fmt.Printf("Writing opk ssh public key to %s and corresponding secret key to %s", pubkeyPath, seckeyPath)

	certBytes = append(certBytes, []byte(" openpubkey")...)
	// Write ssh public key (certificate) to filesystem
	return os.WriteFile(pubkeyPath, certBytes, 0777)
}

func fileExists(fPath string) bool {
	_, err := os.Open(fPath)
	return !errors.Is(err, os.ErrNotExist)
}

func IdentityString(pkt pktoken.PKToken) (string, error) {
	idt, err := oidc.NewJwt(pkt.OpToken)
	if err != nil {
		return "", err
	}
	claims := idt.GetClaims()
	if claims.Email == "" {
		return "Sub, issuer, audience: \n" + claims.Subject + " " + claims.Issuer + " " + claims.Audience, nil
	} else {
		return "Email, sub, issuer, audience: \n" + claims.Email + " " + claims.Subject + " " + claims.Issuer + " " + claims.Audience, nil
	}
}
