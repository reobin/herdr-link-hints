package links

import (
	"slices"
	"strings"
)

// tlds is the bare-host allowlist: IANA names minus common file extensions
// (.sh, .rs, .md) and lowercase identifiers (.info, .id, .at) that would
// otherwise hint filenames and field access.
var tlds = map[string]bool{
	"ai": true, "au": true, "br": true, "ca": true, "ch": true,
	"cloud": true, "cn": true, "co": true, "com": true, "cz": true,
	"de": true, "dev": true, "dk": true, "edu": true, "es": true,
	"fi": true, "fm": true, "fr": true, "gg": true, "gov": true,
	"gr": true, "hu": true, "il": true, "io": true, "jp": true,
	"kr": true, "ly": true, "me": true, "mil": true, "mx": true,
	"net": true, "nl": true, "nz": true, "org": true, "pt": true,
	"ro": true, "ru": true, "se": true, "tech": true, "tr": true,
	"tv": true, "ua": true, "uk": true, "xyz": true, "za": true,
}

// tldAlternation is tlds as regex, longest first.
var tldAlternation = alternation(tlds)

func alternation(set map[string]bool) string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if d := len(b) - len(a); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	return strings.Join(names, "|")
}
