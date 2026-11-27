// Package variables provides functionality for handling AutoMDM variables,
// which are template placeholders that can be substituted with actual values
// in various contexts.
package variables

import (
	"regexp"
	"sort"
	"strings"
)

// automdmVariableRegex matches AutoMDM variables in content.
// It supports two formats:
//   - $AUTOMDM_VAR_NAME - without braces
//   - ${AUTOMDM_VAR_NAME} - with braces
//
// The regex captures the variable name (without the AUTOMDM_VAR_ prefix) in named groups.
var automdmVariableRegex = regexp.MustCompile(`(\$AUTOMDM_VAR_(?P<name1>\w+))|(\${AUTOMDM_VAR_(?P<name2>\w+)})`)

// ProfileDataVariableRegex matches variables present in <data> section of Apple profile, which may cause validation issues.
// This is specific to the DigiCert and EJBCA certificate data variables.
var ProfileDataVariableRegex = regexp.MustCompile(`(\$AUTOMDM_VAR_(DIGICERT|EJBCA)_DATA_(?P<name1>\w+))|(\${AUTOMDM_VAR_(DIGICERT|EJBCA)_DATA_(?P<name2>\w+)})`)

// SecretRegex matches a $AUTOMDM_SECRET_NAME or ${AUTOMDM_SECRET_NAME} custom secret variable.
var SecretRegex = regexp.MustCompile(`\$AUTOMDM_SECRET_\w+|\$\{AUTOMDM_SECRET_\w+\}`)

// Find finds all AutoMDM variables in the given content and returns them as a map
// without the AUTOMDM_VAR_ prefix. Returns nil if no variables are found.
//
// For example, if the content contains "$AUTOMDM_VAR_HOST_UUID" and "${AUTOMDM_VAR_HOST_EMAIL}",
// this function will return map[string]struct{}{"HOST_UUID": {}, "HOST_EMAIL": {}}.
func Find(contents string) []string {
	resultSlice := FindKeepDuplicates(contents)
	if len(resultSlice) == 0 {
		return nil
	}
	return Dedupe(resultSlice)
}

// FindKeepDuplicates finds all AutoMDM variables in the given content and returns them
// as a slice without the AUTOMDM_VAR_ prefix. Duplicates are preserved in the result.
//
// This is useful when you need to know the order or frequency of variable occurrences.
func FindKeepDuplicates(contents string) []string {
	var result []string
	matches := automdmVariableRegex.FindAllStringSubmatch(contents, -1)
	if len(matches) == 0 {
		return nil
	}

	nameToIndex := make(map[string]int, 2)
	for i, name := range automdmVariableRegex.SubexpNames() {
		if name == "" {
			continue
		}
		nameToIndex[name] = i
	}

	for _, match := range matches {
		for _, i := range nameToIndex {
			if match[i] != "" {
				result = append(result, match[i])
			}
		}
	}

	// sort result array by length descending, to ensure longer variables are processed first
	sortedResults := make([]string, len(result))
	copy(sortedResults, result)
	sort.Slice(sortedResults, func(i, j int) bool {
		return len(sortedResults[i]) > len(sortedResults[j])
	})
	return sortedResults
}

// Dedupe removes duplicates from the slice and returns a slice to keep order of variables
func Dedupe(varsWithDupes []string) []string {
	if len(varsWithDupes) == 0 {
		return []string{}
	}

	seenMap := make(map[string]bool, len(varsWithDupes))
	var result []string
	for _, v := range varsWithDupes {
		if !seenMap[v] {
			result = append(result, v)
			seenMap[v] = true
		}
	}
	return result
}

// Replace replaces all occurrences of a specific AutoMDM variable with the given value.
// The variableName should be provided without the AUTOMDM_VAR_ prefix.
// This function replaces both braced and non-braced versions of the variable.
//
// For example, Replace(content, "HOST_UUID", "123-456") will replace both
// $AUTOMDM_VAR_HOST_UUID and ${AUTOMDM_VAR_HOST_UUID} with "123-456".
//
// It performs no escaping. For content an interpreter will execute, never pass a
// raw value: the quoting context at the substitution point is unknown. Use
// Preamble, or an encoding that leaves the value inert at any position, such as
// PythonEscape.
func Replace(contents string, variableName string, value string) string {
	// Replace both braced and non-braced versions
	result := strings.ReplaceAll(contents, "$AUTOMDM_VAR_"+variableName, value)
	result = strings.ReplaceAll(result, "${AUTOMDM_VAR_"+variableName+"}", value)
	return result
}

// PythonEscape returns value as Python \U escapes, one per code point, for
// substitution into Python source, which has no $VAR expansion of its own. Only
// a backslash, "U" and hex digits reach the script, so the value can't close a
// string literal, and outside one the leading backslash is a line continuation
// so the file won't parse. Escaping every character rather than just the
// dangerous ones is what buys that second property: executable Python needs no
// quotes at all.
func PythonEscape(value string) string {
	const hexDigits = "0123456789abcdef"
	var b strings.Builder
	b.Grow(len(value) * 10)
	for _, r := range value {
		b.WriteString(`\U`)
		for shift := 28; shift >= 0; shift -= 4 {
			b.WriteByte(hexDigits[(r>>shift)&0xf])
		}
	}
	return b.String()
}

// Contains checks if the given content contains any AutoMDM variables.
func Contains(contents string) bool {
	return automdmVariableRegex.MatchString(contents)
}

// ContainsBytes checks if the given content contains any AutoMDM variables (bytes version).
func ContainsBytes(contents []byte) bool {
	return automdmVariableRegex.Match(contents)
}
