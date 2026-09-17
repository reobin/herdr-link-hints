package links

import (
	"slices"
	"strings"
)

// tlds gates the bare-host and ssh shapes. Without a gate the pattern marks
// every filename on screen, so the list is an allowlist rather than a denylist.
//
// It is the IANA root zone, all ~1440 delegated names, narrowed by hand under
// one rule with two clauses: a name stays out if it is a common file
// extension, or if it is a plausible lowercase field or method name. A dotted
// identifier is shaped exactly like a two-label host, and a terminal shows far
// more of them than of links.
//
// Both clauses earn their place. As extensions: .sh .rs .md .py .pl .pm .so
// .ml .cc .ps .tf .pub .fish .zip .mov .app are all real TLDs, and would mark
// install.sh, lib.rs, README.md and main.tf. As identifiers: .info .name .id
// .at .top .run .page .one .pro .team .link .click .email .store .chat .live
// .work .build .tools .int would mark log.info, user.id, arr.at and c.run.
// Short English words are both at once: .in .to .is .it .be .by .do .im .my
// .no .ie. And .test is reserved by RFC 2606, never delegated.
//
// The cost is that a host on a name left out gets no hint: bun.sh, docs.rs,
// vercel.app and any .it or .in site. The README says so.
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

// tldAlternation is the gate as regex source, longest name first so the
// generated branch reads the way it matches.
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
