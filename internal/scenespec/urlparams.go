package scenespec

import (
	"net/url"
	"strings"
)

// PreserveURLParams returns settings whose "url" carries the named query
// parameters over from the live URL.
//
// A browser source's URL can have more than one writer. In this workspace the
// "Starting Soon" overlay is the case: one script owns the page and its quad
// geometry, and the streaming dashboard owns "text" and "until", which it sets
// by rewriting the same URL. Whichever writes last would otherwise drop the
// other's parameters, so a writer names the ones that are not its own and they
// survive the write.
//
// Naming a parameter means "carry it over if I did not say". A parameter the
// caller set here wins, because saying it now is more specific than whatever
// happens to be live -- and carrying both would put the key in the URL twice.
//
// settings is never modified; a copy is returned when there is anything to
// change, and settings itself when there is not.
func PreserveURLParams(settings, live map[string]interface{}, names []string) map[string]interface{} {
	if len(names) == 0 {
		return settings
	}

	// Reading a key from a nil map is fine, so the create path -- where there
	// is no live source yet -- needs no special case.
	wanted, ok := settings["url"].(string)
	if !ok {
		return settings // not a browser source, or no URL to merge into
	}
	liveURL, ok := live["url"].(string)
	if !ok || liveURL == "" {
		return settings // nothing live to carry anything over from
	}

	merged := mergeQueryParams(wanted, liveURL, names)
	if merged == wanted {
		return settings
	}

	out := make(map[string]interface{}, len(settings))
	for k, v := range settings {
		out[k] = v
	}
	out["url"] = merged
	return out
}

// mergeQueryParams appends the named parameters of live to wanted.
//
// It appends to the string rather than re-encoding the whole URL, and that is
// the point rather than an optimisation. url.Values.Encode sorts keys and
// escapes characters a URL is allowed to carry raw, so a round trip through it
// would rewrite
//
//	?quad=0,0,1920,0&grade=1&queue=0
//
// into a different string with the same meaning. A spec whose URL comes back
// different from the one it stores reads as drift on every diff, and an apply
// that always has work to do is one nobody trusts. So the caller's own query
// is copied byte for byte and only the carried parameters are encoded.
func mergeQueryParams(wanted, live string, names []string) string {
	liveValues, err := url.ParseQuery(rawQuery(live))
	if err != nil {
		return wanted // unreadable live query: carry nothing rather than guess
	}
	wantedValues, err := url.ParseQuery(rawQuery(wanted))
	if err != nil {
		return wanted
	}

	// Built in the order the caller listed the names, not the order the live
	// URL happens to use, so the result is reproducible from the spec alone.
	var carried strings.Builder
	for _, name := range names {
		if _, set := wantedValues[name]; set {
			continue // the caller said; that wins
		}
		for _, value := range liveValues[name] {
			carried.WriteString(url.QueryEscape(name))
			carried.WriteByte('=')
			carried.WriteString(url.QueryEscape(value))
			carried.WriteByte('&')
		}
	}
	if carried.Len() == 0 {
		return wanted
	}
	tail := strings.TrimSuffix(carried.String(), "&")

	// The fragment comes off first. Appending to the end of the whole string
	// would put the parameters inside "#top", where the page never sees them.
	base, fragment := wanted, ""
	if i := strings.IndexByte(base, '#'); i >= 0 {
		base, fragment = base[:i], base[i:]
	}

	separator := "?"
	if strings.IndexByte(base, '?') >= 0 {
		separator = "&"
	}
	return base + separator + tail + fragment
}

// rawQuery returns a URL's query string, without the leading "?".
func rawQuery(u string) string {
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u = u[:i]
	}
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[i+1:]
	}
	return ""
}
