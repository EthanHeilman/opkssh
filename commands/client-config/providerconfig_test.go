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

package config

import (
	"testing"

	"github.com/openpubkey/openpubkey/providers"
	"github.com/stretchr/testify/require"
)

func ProviderFromString(t *testing.T, providerString string) providers.OpenIdProvider {
	providerAlias := "op1"
	providerIssuer := "https://example.com/tokens-1/"
	providerScopes := "openid profile email"
	providerArg := providerIssuer + ",client-id1234," + "," + "" + "," + providerScopes
	providerStr := providerAlias + "," + providerArg

	providerConfig, err := NewProviderConfigFromString(providerStr, true)
	require.NoError(t, err)
	provider, err := providerConfig.ToProvider(false)
	require.NoError(t, err)
	return provider
}

func TestProviderConfigFromString(t *testing.T) {

	providerAlias := "op1"
	providerIssuer := "https://example.com/tokens-1/"
	providerScopes := "openid profile email"
	providerArg := providerIssuer + ",client-id1234," + "," + "" + "," + providerScopes
	providerStr := providerAlias + "," + providerArg

	tests := []struct {
		name           string
		configString   string
		hasAlias       bool
		expectedIssuer string
		wantError1     bool
		errorString1   string
		wantError2     bool
		errorString2   string
	}{
		{
			name:           "Good path with test providerStr",
			configString:   providerStr,
			hasAlias:       true,
			expectedIssuer: providerIssuer,
		},
		{
			name:           "Good path with test authentik OP",
			configString:   "authentik,https://authentik.io/application/o/opkssh/,client_id,,openid profile email",
			hasAlias:       true,
			expectedIssuer: "https://authentik.io/application/o/opkssh/",
		},
		{
			name:           "Good path with test Google OP",
			configString:   "https://accounts.google.com,206584157355-7cbe4s640tvm7naoludob4ut1emii7sf.apps.googleusercontent.com,NOT-aREAL_3a_GOOGLE-CLIENTSECRET",
			hasAlias:       false,
			expectedIssuer: "https://accounts.google.com",
		},
		{
			name:           "Good path with test microsoft OP",
			configString:   "https://login.microsoftonline.com/9188040d-6c67-4c5b-b112-36a304b66dad/v2.0,096ce0a3-5e72-4da8-9c86-12924b294a01",
			hasAlias:       false,
			expectedIssuer: "https://login.microsoftonline.com/9188040d-6c67-4c5b-b112-36a304b66dad/v2.0",
		},
		{
			name:           "Good path with test microsoft OP",
			configString:   "https://gitlab.com,8d8b7024572c7fd501f64374dec6bba37096783dfcd792b3988104be08cb6923",
			hasAlias:       false,
			expectedIssuer: "https://gitlab.com",
		},
		{
			name:           "Good path with test hello OP",
			configString:   "https://issuer.hello.coop,client-id,,openid email",
			hasAlias:       false,
			expectedIssuer: "https://issuer.hello.coop",
		},
		{
			name:           "Alias set but no alias expected",
			configString:   "exampleOp,https://token.example.com/,client_id,,openid profile email,",
			hasAlias:       false,
			expectedIssuer: "https://token.example.com/",
			wantError2:     true,
			errorString2:   "invalid provider issuer value. Expected issuer to start with 'https://'",
		},
		{
			name:           "No alias set but alias expected",
			configString:   "https://token.example.com/,client_id,,openid profile email,",
			hasAlias:       true,
			expectedIssuer: "https://token.example.com/",
			wantError1:     true,
			errorString1:   "invalid provider client-ID value got ()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerConfig, err := NewProviderConfigFromString(tt.configString, tt.hasAlias)
			if tt.wantError1 {
				require.Error(t, err, "Expected error but got none")
				if tt.errorString1 != "" {
					require.ErrorContains(t, err, tt.errorString1, "Got a wrong error message")
				}

			} else {
				require.NoError(t, err)
				provider, err := providerConfig.ToProvider(false)
				if tt.wantError2 {
					require.Error(t, err, "Expected error but got none")
					if tt.errorString2 != "" {
						require.ErrorContains(t, err, tt.errorString2, "Got a wrong error message")
					}
				} else {
					require.NoError(t, err)
					require.Equal(t, tt.expectedIssuer, provider.Issuer())
				}
			}
		})
	}

}
