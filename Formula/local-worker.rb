# Homebrew formula — builds lw from source at the release tag.
#
#   brew tap ssukru/local-worker https://github.com/ssukru/local-worker.git
#   brew install local-worker
#
# Building from the git tag (instead of a release tarball) keeps this working
# while the repo is private: git goes through your normal GitHub credentials.
class LocalWorker < Formula
  desc "Turn a spare Mac or Linux box into a self-hosted GitHub Actions runner fleet"
  homepage "https://github.com/ssukru/local-worker"
  url "https://github.com/ssukru/local-worker.git", tag: "v0.2.0"
  license "MIT"
  head "https://github.com/ssukru/local-worker.git", branch: "main"

  depends_on "go" => :build
  depends_on "gh"

  def install
    system "go", "build", *std_go_args(output: bin/"lw", ldflags: "-s -w -X main.version=#{version}"), "./cmd/lw"
  end

  test do
    assert_match "local-worker", shell_output("#{bin}/lw help")
  end
end
