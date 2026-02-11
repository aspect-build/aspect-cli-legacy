/*
 * Copyright 2023 Aspect Build Systems, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package flags

import "strings"

/**
 * Parse a set of flag modifications and apply them to a base set of flag values.
 *
 * This should align with the bazel behaviour for arguments such as `--modify_execution_info`
 * where for a given set ("base") each argument can override, add or append to the set.
 */
func ParseSet(base []string, args []string) []string {
	// The flags to be returned, initialized with the base flags.
	resultSet := make(map[string]bool)
	for _, val := range base {
		resultSet[val] = true
	}

	for _, val := range args {
		for part := range strings.SplitSeq(val, ",") {
			if part == "" { // Handle empty strings from "a,,b"
				continue
			}

			if after, ok := strings.CutPrefix(part, "+"); ok {
				resultSet[after] = true
			} else if after, ok := strings.CutPrefix(part, "-"); ok {
				delete(resultSet, after)
			} else {
				resultSet = make(map[string]bool) // Reset the set
				resultSet[part] = true
			}
		}
	}

	res := make([]string, 0, len(resultSet))

	// Maintain the original order of items from the base set
	for _, k := range base {
		if resultSet[k] {
			res = append(res, k)
			resultSet[k] = false // Mark as processed
		}
	}

	// Add any new items that were added
	for k, v := range resultSet {
		if v {
			res = append(res, k)
		}
	}

	return res
}
