/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package azurefile

import "testing"

func TestSerializeTags(t *testing.T) {
	tests := []struct {
		desc     string
		tags     map[string]string
		expected string
	}{
		{
			desc:     "nil map",
			tags:     nil,
			expected: "",
		},
		{
			desc:     "empty map",
			tags:     map[string]string{},
			expected: "",
		},
		{
			desc:     "single tag",
			tags:     map[string]string{"key1": "value1"},
			expected: `{"key1":"value1"}`,
		},
		{
			desc:     "multiple tags sorted by key",
			tags:     map[string]string{"beta": "2", "alpha": "1", "gamma": "3"},
			expected: `{"alpha":"1","beta":"2","gamma":"3"}`,
		},
		{
			desc:     "special characters in values",
			tags:     map[string]string{"key": "val\"ue"},
			expected: `{"key":"val\"ue"}`,
		},
		{
			desc:     "deterministic output on repeated calls",
			tags:     map[string]string{"z": "1", "a": "2", "m": "3"},
			expected: `{"a":"2","m":"3","z":"1"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			result := serializeTags(test.tags)
			if result != test.expected {
				t.Errorf("serializeTags(%v) = %q, expected %q", test.tags, result, test.expected)
			}
		})
		// verify determinism
		if test.tags != nil {
			first := serializeTags(test.tags)
			second := serializeTags(test.tags)
			if first != second {
				t.Errorf("serializeTags not deterministic for %v: %q != %q", test.tags, first, second)
			}
		}
	}
}
