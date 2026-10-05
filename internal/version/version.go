// Package version reports which release of poweraudio this binary is.
//
// Releases are numbered x.y.z and tagged vx.y.z in git. CHANGELOG.md lists
// what each one changed. The Makefile stamps the number into the binary from
// the nearest tag, so a build three commits past v0.4.1 reports
// "0.4.1-3-gabc1234", with "-dirty" when the tree had uncommitted changes.
package version

import (
	"runtime/debug"
	"strings"
)

// stamped is set at link time:
//
//	go build -ldflags "-X github.com/roverflow/poweraudio/internal/version.stamped=0.4.1"
var stamped = ""

// String returns this binary's version without the leading v, such as
// "0.4.1". A build that skipped the Makefile reports what the Go toolchain
// recorded instead: the module version for `go install ...@v0.4.1`, or a
// pseudo-version derived from the nearest tag for a plain `go build` in a
// checkout. With neither it is "dev".
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
