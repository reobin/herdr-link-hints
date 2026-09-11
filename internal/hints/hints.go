// Package hints assigns short typeable codes to a list of targets.
package hints

// DefaultAlphabet keeps every hint on the home row.
const DefaultAlphabet = "asdfghjkl"

// Codes returns n distinct codes of equal width, so a fully typed code is
// never the prefix of another. It returns nil for an alphabet too small.
func Codes(n int, alphabet string) []string {
	if n <= 0 || len(alphabet) < 2 {
		return nil
	}
	base := len(alphabet)
	width, capacity := 1, base
	for capacity < n {
		width++
		capacity *= base
	}
	codes := make([]string, n)
	for i := range codes {
		code := make([]byte, width)
		value := i
		for pos := width - 1; pos >= 0; pos-- {
			code[pos] = alphabet[value%base]
			value /= base
		}
		codes[i] = string(code)
	}
	return codes
}
