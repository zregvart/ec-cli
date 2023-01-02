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

// Package attestation helps create SLSA provenance attestations in-toto format, simulating what
// Tekton Chains would perform.
package attestation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/in-toto/in-toto-golang/in_toto"
	"github.com/sigstore/cosign/pkg/cosign/attestation"
	"github.com/sigstore/cosign/pkg/types"
	"github.com/sigstore/sigstore/pkg/signature/dsse"
	"github.com/sigstore/sigstore/pkg/signature/options"

	"github.com/hacbs-contract/ec-cli/internal/acceptance/crypto"
)

const (
	PredicateBuilderID   = "https://tekton.dev/chains/v2"
	PredicateBuilderType = "https://tekton.dev/attestations/chains/pipelinerun@v2"
	PredicateType        = "slsaprovenance"
)

// CreateStatementFor creates an empty statement that can be further customized
// to add and subsequently signed by SignStatement.
func CreateStatementFor(imageName string, image v1.Image) (*in_toto.ProvenanceStatement, error) {
	digest, err := image.Digest()
	if err != nil {
		return nil, err
	}

	obj, err := attestation.GenerateStatement(attestation.GenerateOpts{
		Predicate: bytes.NewReader([]byte(fmt.Sprintf(`{
			"builder": {
				"id": "%s"
			},
			"buildType": "%s"
		}`, PredicateBuilderID, PredicateBuilderType))),
		Type:   PredicateType,
		Digest: digest.Hex,
		Repo:   imageName,
	})
	if err != nil {
		return nil, err
	}

	if statement, ok := obj.(in_toto.ProvenanceStatement); ok {
		return &statement, nil
	}

	return nil, fmt.Errorf("received statement of unsupported type: %v", obj)
}

// SignStatement signs the provided statement with the named key. The key needs
// to be previously generated with the functionality from the crypto package.
func SignStatement(ctx context.Context, keyName string, statement in_toto.ProvenanceStatement) ([]byte, error) {
	payload, err := json.Marshal(statement)
	if err != nil {
		return nil, err
	}

	signer, err := crypto.SignerWithKey(ctx, keyName)
	if err != nil {
		return nil, err
	}

	dsseSigner := dsse.WrapSigner(signer, types.IntotoPayloadType)

	return dsseSigner.SignMessage(bytes.NewReader(payload), options.WithContext(ctx))
}
