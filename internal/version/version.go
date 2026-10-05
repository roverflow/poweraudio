// Package version reports which release of poweraudio this binary is.
package version

import (
	"runtime/debug"
	"strings"
)

// The Makefile sets stamped at link time with -X.
var stamped = ""

// String returns the version without its leading v. Without a Makefile
// stamp it falls back on Go build info, then "dev".
func String() string {
	info, ok := debug.ReadBuildInfo()
	return resolve(stamped, info, ok)
}

func resolve(stamped string, info *debug.BuildInfo, ok bool) string {
	if stamped != "" {
		return strings.TrimPrefix(stamped, "v")
	}
	if ok && info != nil {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return "dev"
}
