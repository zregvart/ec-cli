// Copyright 2022 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Package rekor is a stub implementation of Rekord
package rekor

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"

	"github.com/cucumber/godog"
	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"github.com/go-openapi/strfmt"
	"github.com/sigstore/cosign/pkg/cosign"
	"github.com/sigstore/cosign/pkg/cosign/bundle"
	"github.com/sigstore/rekor/pkg/generated/models"
	intoto "github.com/sigstore/rekor/pkg/types/intoto/v0.0.1"
	"github.com/sigstore/sigstore/pkg/cryptoutils"
	"github.com/theupdateframework/go-tuf/encrypted"
	"github.com/transparency-dev/merkle/rfc6962"

	"github.com/hacbs-contract/ec-cli/internal/acceptance/crypto"
	"github.com/hacbs-contract/ec-cli/internal/acceptance/image"
	"github.com/hacbs-contract/ec-cli/internal/acceptance/testenv"
	"github.com/hacbs-contract/ec-cli/internal/acceptance/wiremock"
)

type key int

const rekorStateKey = key(0) // we store the gitState struct under this key in Context and when persisted

type rekorState struct {
	KeyPair *cosign.KeysBytes
}

func (r rekorState) Key() any {
	return rekorStateKey
}

// stubRekordRunning starts the stub apiserver using WireMock
func stubRekordRunning(ctx context.Context) (context.Context, error) {
	var state *rekorState
	ctx, err := testenv.SetupState(ctx, &state)
	if err != nil {
		return ctx, err
	}

	if state.KeyPair == nil {
		// not used for any signing, we just need the public key in PEM for the
		// in-toto schema below
		keyPair, err := crypto.GenerateKeyPair()
		if err != nil {
			return ctx, err
		}

		state.KeyPair = keyPair
	}

	ctx, err = wiremock.StartWiremock(ctx)
	if err != nil {
		return ctx, err
	}

	if err = wiremock.StubFor(ctx, wiremock.Get(wiremock.URLPathEqualTo("/api/v1/log/publicKey")).
		WillReturn(string(state.KeyPair.PublicBytes),
			map[string]string{"Content-Type": "application/x-pem-file"},
			200,
		)); err != nil {
		return ctx, err
	}

	return ctx, nil
}

// randomHex generates a random hex string of the given length
func randomHex(len int) string {
	b := make([]byte, len/2)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}

	return hex.EncodeToString(b)
}

// computeLogID returns a hex-encoded SHA-256 digest of the
// SubjectPublicKeyInfo ASN.1 structure for the given
// PEM-encoded public key
func computeLogID(publicKey []byte) (string, error) {
	pub, err := cryptoutils.UnmarshalPEMToPublicKey(publicKey)
	if err != nil {
		return "", err
	}
	pubBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(pubBytes)
	return hex.EncodeToString(digest[:]), nil
}

// computeLogEntry constructs a Rekor log entry and provides it's UUID
// for the provided public key and data
func computeLogEntry(ctx context.Context, publicKey, data []byte) (logEntry *models.LogEntryAnon, entryUUID []byte, err error) {
	// the body of the log entry is an in-toto payload
	logBody := intoto.NewEntry()

	algorithm := models.IntotoV001SchemaContentPayloadHashAlgorithmSha256

	// the contentHash should relate to other entries but since we're faking a single
	// entry, not related to other entries -- any random contentHash-like hex will be
	// okay. Similarly, for testing purposes, any payload hash will do.
	hash := randomHex(64)

	publicKeyBase64 := strfmt.Base64(publicKey)

	// the only way to set fields of the intoto Entry
	err = logBody.Unmarshal(&models.Intoto{
		Spec: models.IntotoV001Schema{
			Content: &models.IntotoV001SchemaContent{
				Hash: &models.IntotoV001SchemaContentHash{
					Algorithm: &algorithm,
					Value:     &hash,
				},
				PayloadHash: &models.IntotoV001SchemaContentPayloadHash{
					Algorithm: &algorithm,
					Value:     &hash,
				},
			},
			PublicKey: &publicKeyBase64,
		},
	})
	if err != nil {
		return nil, nil, err
	}

	logBodyBytes, err := logBody.Canonicalize(ctx)
	if err != nil {
		return nil, nil, err
	}

	hasher := rfc6962.DefaultHasher

	// needs to match the hash over body bytes
	entryUUID = hasher.HashLeaf(logBodyBytes)

	// create simplest Merkle tree
	entryHash := hasher.HashChildren(hasher.EmptyRoot(), entryUUID)
	hashes := []string{
		hex.EncodeToString(entryHash),
	}
	rootHash := hasher.HashChildren(entryHash, entryUUID)
	rootHashHex := hex.EncodeToString(rootHash)

	// simplest possible tree has the size of 2
	logIndex := int64(1)
	treeSize := int64(2)
	time := int64(0)
	logID, err := computeLogID(publicKey)
	if err != nil {
		return nil, nil, err
	}

	// fill in the entry with the attestation and in-toto entry for the log
	// and add the verification
	logEntry = &models.LogEntryAnon{
		Attestation: &models.LogEntryAnonAttestation{
			Data: data,
		},
		Body: base64.StdEncoding.EncodeToString(logBodyBytes),
		Verification: &models.LogEntryAnonVerification{
			InclusionProof: &models.InclusionProof{
				RootHash: &rootHashHex,
				Hashes:   hashes,
				LogIndex: &logIndex,
				TreeSize: &treeSize,
			},
		},
		IntegratedTime: &time,
		LogIndex:       &logIndex,
		LogID:          &logID,
	}

	return logEntry, entryUUID, nil
}

// computeEntryTimestamp signs Rekor log entryies body, integrated timestam,
// log index and log ID with the provided private key encrypted by the given
// password
func computeEntryTimestamp(privateKey, password []byte, logEntry models.LogEntryAnon) ([]byte, error) {
	encryptedPrivateKey, _ := pem.Decode(privateKey)
	if encryptedPrivateKey == nil {
		return nil, errors.New("unable to decode PEM encoded private key")
	}

	derPrivateKey, err := encrypted.Decrypt(encryptedPrivateKey.Bytes, password)
	if err != nil {
		return nil, err
	}

	key, err := x509.ParsePKCS8PrivateKey(derPrivateKey)
	if err != nil {
		return nil, err
	}

	payload := bundle.EntryToBundle(&logEntry)

	payloadBytes, err := json.Marshal(payload.Payload)
	if err != nil {
		return nil, err
	}

	canonicalizedPayload, err := jsoncanonicalizer.Transform(payloadBytes)
	if err != nil {
		return nil, err
	}

	payloadHash := sha256.Sum256(canonicalizedPayload)

	return ecdsa.SignASN1(rand.Reader, key.(*ecdsa.PrivateKey), payloadHash[:])
}

// stubRekorEntryFor instructs WireMock to return a bespoke Rekor log entry
// for the given bytes, the entry is timestamped using the key stored in
// state.KeyPair and a fake Merkle tree is created with an empty root
// and this entries' hash
func stubRekorEntryFor(ctx context.Context, data []byte) error {
	state := testenv.FetchState[rekorState](ctx)

	logEntry, entryUUID, err := computeLogEntry(ctx, state.KeyPair.PublicBytes, data)
	if err != nil {
		return err
	}

	set, err := computeEntryTimestamp(state.KeyPair.PrivateBytes, state.KeyPair.Password(), *logEntry)
	if err != nil {
		return err
	}

	// this is what will be returned, albeit in an array
	var logEntries models.LogEntry = models.LogEntry{}
	entryID := hex.EncodeToString(entryUUID)
	logEntries[entryID] = *logEntry

	logEntry.Verification.SignedEntryTimestamp = strfmt.Base64(set)

	// the response is in application/json
	body, err := json.Marshal([]models.LogEntry{logEntries})
	if err != nil {
		return err
	}

	dataBase64 := base64.StdEncoding.EncodeToString(data)

	// return this entry for any lookup in Rekor
	return wiremock.StubFor(ctx, wiremock.Post(wiremock.URLPathEqualTo("/api/v1/log/entries/retrieve")).
		WithBodyPattern(wiremock.Contains(dataBase64)).
		WillReturn(string(body),
			map[string]string{"Content-Type": "application/json"},
			200,
		))
}

// rekorEntryForAttestation given an image name for which attestation has been
// previously performed via image.createAndPushAttestation, creates stub for a
// mostly empty attestation log entry in Rekor
func rekorEntryForAttestation(ctx context.Context, imageName string) error {
	attestation, err := image.AttestationFrom(ctx, imageName)
	if err != nil {
		return err
	}

	return stubRekorEntryFor(ctx, attestation)
}

// rekorEntryForImageSignature given an image name for which signature has been
// previously performed via image.createAndPushImageSignature, creates stub
// log entry in Rekor
func rekorEntryForImageSignature(ctx context.Context, imageName string) error {
	signature, err := image.ImageSignatureFrom(ctx, imageName)
	if err != nil {
		return err
	}

	return stubRekorEntryFor(ctx, signature)
}

// StubRekor returns the `http://host:port` of the stubbed Rekord
func StubRekor(ctx context.Context) (string, error) {
	return wiremock.Endpoint(ctx)
}

// PublicKey returns the public key of the Rekor signing key
func PublicKey(ctx context.Context) []byte {
	state := testenv.FetchState[rekorState](ctx)

	return state.KeyPair.PublicBytes
}

func IsRunning(ctx context.Context) bool {
	return wiremock.IsRunning(ctx)
}

// AddStepsTo adds Gherkin steps to the godog ScenarioContext
func AddStepsTo(sc *godog.ScenarioContext) {
	sc.Step(`^stub rekord running$`, stubRekordRunning)
	sc.Step(`^a valid Rekor entry for attestation of "([^"]*)"$`, rekorEntryForAttestation)
	sc.Step(`^a valid Rekor entry for image signature of "([^"]*)"$`, rekorEntryForImageSignature)
}
