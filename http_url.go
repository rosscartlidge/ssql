package ssql

// The URL helpers every build has: pure string functions, so the slim
// build (no net/http, DFC151) still routes a URL argument to the loud
// "not in this build" refusal in http_source_slim.go instead of
// treating it as a file name.

import "strings"

// IsHTTPURL reports whether path is an http(s) URL — the routing test
// used by `from` and its subcommands.
func IsHTTPURL(path string) bool {
	return strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://")
}

// ByteRange is a span of a file: Len bytes from Off.
type ByteRange struct{ Off, Len int64 }

// HTTPURLExt returns the lowercase extension of a URL's PATH,
// ignoring query and fragment — filepath.Ext on a presigned URL
// ("…/x.csv?X-Amz-Signature=…") would return ".csv?x-amz-…".
func HTTPURLExt(rawurl string) string {
	u := rawurl
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndexByte(u, '.'); i >= 0 && i > strings.LastIndexByte(u, '/') {
		return strings.ToLower(u[i:])
	}
	return ""
}
