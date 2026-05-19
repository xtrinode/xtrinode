package gateway

import "strings"

// singleJoiningSlash joins two URL paths, ensuring exactly one slash between them
// This prevents double slashes when concatenating paths
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		if b != "" {
			return a + "/" + b
		}
		return a
	default:
		return a + b
	}
}
