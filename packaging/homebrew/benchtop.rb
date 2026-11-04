# Homebrew formula for Benchtop. RELEASING.md says how to update it.
class Benchtop < Formula
  desc "Local PHP development environment: sites, PHP versions and databases"
  homepage "https://github.com/kennlebu/benchtop"
  url "https://github.com/kennlebu/benchtop/archive/refs/tags/v1.0.0.tar.gz"
  sha256 "REPLACE_WITH_THE_SHA256_OF_THE_TARBALL"
  license "MIT"

  depends_on "go" => :build
  depends_on "caddy"
  depends_on :macos

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=#{version}"), "./cmd/benchtop"
  end

  def caveats
    <<~EOS
      Benchtop runs PHP from Homebrew. Install one or more versions:
        brew install php
      Then open the dashboard:
        benchtop ui
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/benchtop version")
  end
end
