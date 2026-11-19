# Homebrew formula — builds lw from the exact release commit.
#
#   brew tap ssukru/local-worker https://github.com/ssukru/local-worker.git
#   brew install local-worker
class LocalWorker < Formula
  desc "Turn a spare Mac or Linux box into a self-hosted GitHub Actions runner fleet"
  homepage "https://github.com/ssukru/local-worker"
  url "https://github.com/ssukru/local-worker.git",
      tag: "v0.2.0",
      revision: "b1a16b29b074e6c9da3a41ed8b7ea9b5ab09e3ee"
  license "MIT"
  head "https://github.com/ssukru/local-worker.git", branch: "main"

  depends_on "go" => :build
  depends_on "gh"

  def install
    system "go", "build", *std_go_args(output: bin/"lw", ldflags: "-s -w -X main.version=#{version}"), "./cmd/lw"
  end

  test do
    assert_equal "lw #{version}\n", shell_output("#{bin}/lw version")
  end
end
