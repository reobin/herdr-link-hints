// Package hints assigns short typeable codes to a list of targets.
package hints

import "unicode/utf8"

// DefaultAlphabet starts on the home row.
const DefaultAlphabet = "asdfghjklqwertyuiopzxcvbnm"

// Codes returns n distinct prefix-free codes, shortest first.
func Codes(n int, alphabet string) []string {
	if n <= 0 || len([]rune(alphabet)) < 2 {
		return nil
	}
	runes := []rune(alphabet)
	leaves := make([]string, 0, n)
	for _, r := range runes {
		leaves = append(leaves, string(r))
	}
	for len(leaves) < n {
		shallowest := 0
		for shallowest+1 < len(leaves) && depth(leaves[shallowest+1]) == depth(leaves[0]) {
			shallowest++
		}
		head := leaves[shallowest]
		leaves = append(leaves[:shallowest], leaves[shallowest+1:]...)
		for _, r := range runes {
			leaves = append(leaves, head+string(r))
		}
	}
	return leaves[:n]
}

func depth(code string) int {
	return utf8.RuneCountInString(code)
}
