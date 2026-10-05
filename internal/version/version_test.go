package version

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	built := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/roverflow/poweraudio", Version: v}}
	}
	cases := []struct {
		name    string
		stamped string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"stamped by make", "0.4.1", built("(devel)"), true, "0.4.1"},
		{"stamped with the tag's v", "v0.4.1", nil, false, "0.4.1"},
		{"past a tag", "0.4.1-3-gabc1234-dirty", nil, false, "0.4.1-3-gabc1234-dirty"},
		{"go install at a tag", "", built("v0.4.1"), true, "0.4.1"},
		{"plain go build", "", built("v0.4.2-0.20261005053000-11afb01e2b3c+dirty"), true, "0.4.2-0.20261005053000-11afb01e2b3c+dirty"},
		{"no version control", "", built("(devel)"), true, "dev"},
		{"no build info", "", nil, false, "dev"},
	}
	for _, c := range cases {
		if got := resolve(c.stamped, c.info, c.ok); got != c.want {
			t.Errorf("%s: resolve = %q, want %q", c.name, got, c.want)
		}
	}
}
