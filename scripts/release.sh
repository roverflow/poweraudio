#!/usr/bin/env bash
# Makes a poweraudio release from the notes under "Unreleased" in CHANGELOG.md.
#
#   scripts/release.sh 0.5.0
#
# It moves those notes into a section for the new version, commits that, and
# creates an annotated vX.Y.Z tag with the notes as its message. It does not
# push. Check the commit and the tag, then push both:
#
#   git push origin main v0.5.0
set -euo pipefail

REPO_URL=https://github.com/roverflow/poweraudio
CHANGELOG=CHANGELOG.md

fail() { printf 'release: %s\n' "$*" >&2; exit 1; }

[[ $# -eq 1 ]] || fail "usage: scripts/release.sh X.Y.Z"
version=${1#v}
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] ||
    fail "$1 is not a version like 1.2.3 or 1.2.3-rc.1"
tag=v$version

cd "$(git rev-parse --show-toplevel)"

[[ $(git branch --show-current) == main ]] || fail "releases are made from main"
[[ -z $(git status --porcelain) ]] || fail "commit or stash your changes first"
git rev-parse -q --verify "refs/tags/$tag" >/dev/null && fail "$tag already exists"

previous=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true)
if [[ -n $previous ]]; then
    newest=$(printf '%s\n%s\n' "${previous#v}" "$version" | sort -V | tail -n1)
    [[ $newest == "$version" && ${previous#v} != "$version" ]] ||
        fail "$version is not newer than the last release, ${previous#v}"
fi

# The notes are the lines between "## [Unreleased]" and the next "## [".
notes=$(awk '
    /^## \[Unreleased\]/ { inside = 1; next }
    inside && /^## \[/   { exit }
    inside               { print }
' "$CHANGELOG")
[[ -n ${notes//[[:space:]]/} ]] || fail "there is nothing under Unreleased in $CHANGELOG"

echo "Running the tests..."
go vet ./...
go test ./...

today=$(date +%Y-%m-%d)
base=${previous:-}
tmp=$(mktemp "$CHANGELOG.XXXXXX")
trap 'rm -f "$tmp"' EXIT

# Start a new, empty Unreleased section above the notes, and point the
# comparison links at the new tag.
awk -v version="$version" -v today="$today" -v url="$REPO_URL" -v base="$base" -v tag="$tag" '
    /^## \[Unreleased\]/ {
        print
        print ""
        print "## [" version "] - " today
        next
    }
    /^\[Unreleased\]: / {
        print "[Unreleased]: " url "/compare/" tag "...HEAD"
        if (base != "") {
            print "[" version "]: " url "/compare/" base "..." tag
        } else {
            print "[" version "]: " url "/releases/tag/" tag
        }
        next
    }
    { print }
' "$CHANGELOG" >"$tmp"
# Copied rather than moved, so the file keeps its mode instead of mktemp's 0600.
cat "$tmp" >"$CHANGELOG"
rm -f "$tmp"
trap - EXIT

git add "$CHANGELOG"
git commit -q -m "release: $tag"
# Verbatim, because git otherwise drops the "### Added" headings as comments.
git tag -a --cleanup=verbatim "$tag" -F - <<EOF
poweraudio $version
$notes
EOF

echo
echo "Made $tag on $(git rev-parse --short HEAD). Look it over, then push it:"
echo "  git push origin main $tag"
