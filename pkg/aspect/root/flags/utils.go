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

// FindInvocationId scans the Bazel portion of args (before any bare "--") for
// --invocation_id, accepting both "--invocation_id=<id>" and "--invocation_id <id>" forms.
// Returns the last occurrence (matching Bazel's last-flag-wins precedence), or "" if not found.
func FindInvocationId(args []string) string {
	last := ""
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if after, ok := strings.CutPrefix(arg, "--invocation_id="); ok {
			last = after
		} else if arg == "--invocation_id" && i+1 < len(args) && args[i+1] != "--" {
			last = args[i+1]
		}
	}
	return last
}

func AddFlagToCommand(command []string, flag ...string) []string {
	result := make([]string, 0, len(command)+1)
	for i, c := range command {
		if c == "--" {
			// inject the flag right before a double dash if it exists
			result = append(result, flag...)
			result = append(result, command[i:len(command)]...)
			return result
		}
		result = append(result, c)
	}
	// if no double dash then add the flag at the end of the command
	result = append(result, flag...)
	return result
}

func RemoveFlag(args []string, flag string) (bool, []string) {
	for i, arg := range args {
		switch arg {
		case flag:
			return true, append(args[:i], args[i+1:]...)
		case "--":
			return false, args
		}
	}
	return false, args
}
