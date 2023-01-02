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

//go:build unit

package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yudai/gojsondiff"
)

func Test_JSONDiffWithRegex(t *testing.T) {
	expected := []byte(`{
		"a": "^0\\d+$",
		"b": {
			"c": "^[a-z]0[A-Z]$",
			"d": [
				{
					"e": "^[0-5]$"
				}
			]
		}
	}`)

	cases := []struct {
		name     string
		expected []byte
		right    string
		matches  bool
	}{
		{
			name:     "passing",
			expected: expected,
			right: `{
				"a": "0123",
				"b": {
					"c": "a0B",
					"d": [
						{"e": 1}
					]
				}
			}`,
			matches: true,
		},
		{
			name:     "failing nested",
			expected: expected,
			right: `{
				"a": "0123",
				"b": {
					"c": "a0B",
					"d": [
						{"e": 10}
					]
				}
			}`,
			matches: false,
		},
		{
			name:     "failing",
			expected: expected,
			right: `{
				"a": "123",
				"b": {
					"c": "B2a",
					"d": [
						{"e": 6},
						{"e": 10}
					]
				}
			}`,
			matches: false,
		},
		{
			name:     "literal",
			expected: expected,
			right:    string(expected),
			matches:  true,
		},
		{
			name: "similar positions",
			expected: []byte(`{
				"a": "^[a-z]$",
				"b": {
					"a": "^[0-9]$",
					"b": "^[a-z]$"
				}
			}`),
			right: `{
				"a": "a",
				"b": {
					"a": "a",
					"b": "a"
				}
			}`,
			matches: false,
		},
		{
			name: "missed cardinality - excess in expected",
			expected: []byte(`{
				"a": [1, 2, 3]
			}`),
			right: `{
				"a": [1, 2]
			}`,
			matches: false,
		},
		{
			name: "missed cardinality - excess in actual",
			expected: []byte(`{
				"a": [1, 2]
			}`),
			right: `{
				"a": [1, 2, 3]
			}`,
			matches: false,
		},
	}

	differ := gojsondiff.New()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diff, err := differ.Compare(c.expected, []byte(c.right))
			assert.NoError(t, err)

			var left any
			err = json.Unmarshal(c.expected, &left)
			assert.NoError(t, err)

			if diff.Modified() {
				assert.Equal(t, c.matches, matchesJSONRegex(left, diff))
			}
		})
	}
}
