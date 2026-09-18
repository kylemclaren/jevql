#!/usr/bin/env bash
# Render Formula/jevql.rb for the Homebrew tap from a released version.
#
#   scripts/brew-formula.sh <version> <checksums.txt> > Formula/jevql.rb
#
# checksums.txt has "sha256  jevql_<version>_<os>_<arch>.tar.gz" lines, as
# produced by the release workflow (sha256sum format).
set -euo pipefail

version="${1:?version (without v)}"
checksums="${2:?path to checksums.txt}"
repo="https://github.com/kylemclaren/jevql"

sha() {
  local f="jevql_${version}_$1_$2.tar.gz"
  local s
  s=$(awk -v f="$f" '$2 == f || $2 == "*"f {print $1}' "$checksums")
  if [ -z "$s" ]; then
    echo "missing checksum for $f in $checksums" >&2
    exit 1
  fi
  printf '%s' "$s"
}

url() { printf '%s/releases/download/v%s/jevql_%s_%s_%s.tar.gz' "$repo" "$version" "$version" "$1" "$2"; }

cat <<RUBY
class Jevql < Formula
  desc "Semantic SQL for vanilla Postgres: jev() in any query, judged by TypeSafe"
  homepage "${repo}"
  version "${version}"
  license "MIT"

  on_macos do
    on_arm do
      url "$(url darwin arm64)"
      sha256 "$(sha darwin arm64)"
    end
    on_intel do
      url "$(url darwin amd64)"
      sha256 "$(sha darwin amd64)"
    end
  end

  on_linux do
    on_arm do
      url "$(url linux arm64)"
      sha256 "$(sha linux arm64)"
    end
    on_intel do
      url "$(url linux amd64)"
      sha256 "$(sha linux amd64)"
    end
  end

  livecheck do
    url :homepage
    strategy :github_latest
  end

  def install
    bin.install "jevql"
  end

  test do
    assert_match "jevql #{version}", shell_output("#{bin}/jevql --version")
  end
end
RUBY
